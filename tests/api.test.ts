import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createApp,
  effectScope,
  ref,
  type App,
  type EffectScope,
  type MaybeRefOrGetter,
} from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { useRunMessages } from '../apps/dashboard/src/api/queries';
import { keys } from '../apps/dashboard/src/api/keys';
import type { AgentRun, RunMessage } from '../apps/dashboard/src/api/types';
import { api, ApiError } from '../apps/dashboard/src/api/client';
import { workspaceId } from '../apps/dashboard/src/api/session';

function respond(status: number, body?: string, statusText = '') {
  const fetch = vi.fn(
    async (_path: string, _init: RequestInit) =>
      new Response(status === 204 ? null : (body ?? null), { status, statusText }),
  );
  vi.stubGlobal('fetch', fetch);
  return fetch;
}
const headers = (fetch: ReturnType<typeof respond>) =>
  fetch.mock.calls[0]![1].headers as Record<string, string>;

function queryHarness() {
  const scope = effectScope();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  });
  const app = createApp({});
  app.use(VueQueryPlugin, { queryClient: client });
  return { scope, client, app };
}

afterEach(() => {
  vi.unstubAllGlobals();
  workspaceId.value = '';
});

describe('api', () => {
  it('names the current workspace on /api routes only', async () => {
    workspaceId.value = 'w1';
    let fetch = respond(200, '[]');
    await api('GET', '/api/cards');
    expect(headers(fetch)['X-Workspace-ID']).toBe('w1');
    fetch = respond(204);
    await api('POST', '/auth/logout');
    expect(headers(fetch)['X-Workspace-ID']).toBeUndefined();
  });

  it('sends JSON and reads JSON', async () => {
    const fetch = respond(201, '{"id":"c1","number":3}');
    const out = await api<{ id: string; number: number }>('POST', '/api/cards', { question: 'q' });
    expect(out).toEqual({ id: 'c1', number: 3 });
    const init = fetch.mock.calls[0]![1];
    expect(init.body).toBe('{"question":"q"}');
    expect(headers(fetch)['Content-Type']).toBe('application/json');
    expect(init.credentials).toBe('same-origin');
  });

  it('answers undefined for 204', async () => {
    respond(204);
    await expect(api('DELETE', '/api/cards/c1')).resolves.toBeUndefined();
  });

  it('turns a JSON error into an ApiError carrying the body', async () => {
    respond(409, '{"error":"an active card already asks this: #4","card_id":"c4","number":4}');
    const err = await api('POST', '/api/cards', { question: 'q' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    expect((err as ApiError).message).toBe('an active card already asks this: #4');
    expect((err as ApiError).body.card_id).toBe('c4');
  });

  it('falls back to the status text when the error is not JSON', async () => {
    respond(502, '<html>bad gateway</html>', 'Bad Gateway');
    await expect(api('GET', '/api/me')).rejects.toMatchObject({
      status: 502,
      message: 'Bad Gateway',
    });
  });

  it('sends a raw body with its own type', async () => {
    const fetch = respond(204);
    const logo = new Blob(['png'], { type: 'image/png' });
    await api('PUT', '/api/workspaces/w1/logo', undefined, { data: logo, type: 'image/png' });
    expect(fetch.mock.calls[0]![1].body).toBe(logo);
    expect(headers(fetch)['Content-Type']).toBe('image/png');
  });
});

// The HTTP boundary is stubbed here; the query and Vue Query cache are real.
// Real-server transcript coverage belongs to the live dashboard checks.
describe('run message pagination', () => {
  let scope: EffectScope;
  let client: QueryClient;
  let app: App;

  beforeEach(() => {
    workspaceId.value = 'w1';
    ({ scope, client, app } = queryHarness());
  });
  afterEach(() => {
    scope.stop();
    client.clear();
  });

  const run = (attempt = 2): AgentRun => ({
    id: 'r1',
    workspace_id: 'w1',
    card_id: 'c1',
    responsible_user_id: 'u1',
    board_column: 'grounding',
    round: 1,
    role: 'grounder',
    harness: 'codex',
    status: 'completed',
    queue_reason: null,
    runtime_id: 'rt1',
    dispatched_at: null,
    started_at: null,
    completed_at: null,
    attempt,
    max_attempts: 3,
    model: '',
    error: '',
    failure_reason: null,
    cost_usd: 0,
    created_at: '2026-10-15T00:00:00Z',
    handoff_note: '',
  });
  const messages = (count: number, attempt = 2): RunMessage[] =>
    Array.from({ length: count }, (_, index) => ({
      workspace_id: 'w1',
      agent_run_id: 'r1',
      attempt,
      seq: (index + 1) * 2,
      type: 'text',
      tool: '',
      content: `message ${index + 1}`,
      created_at: '2026-10-15T00:00:00Z',
    }));
  function observe(source: MaybeRefOrGetter<AgentRun | undefined>) {
    return app.runWithContext(() => scope.run(() => useRunMessages(source, true))!);
  }

  it.each([
    { count: 0, cursors: [0] },
    { count: 499, cursors: [0] },
    { count: 500, cursors: [0, 1000] },
    { count: 501, cursors: [0, 1000] },
    { count: 1000, cursors: [0, 1000, 2000] },
  ])(
    'loads $count messages, following full pages and stopping on short or empty pages',
    async ({ count, cursors }) => {
      const all = messages(count);
      const fetch = vi.fn(async (path: string) => {
        const query = new URL(path, 'http://localhost').searchParams;
        return Response.json(all.filter((m) => m.seq > Number(query.get('after'))).slice(0, 500));
      });
      vi.stubGlobal('fetch', fetch);
      const query = observe(run());
      await vi.waitFor(() => expect(query.isSuccess.value).toBe(true));
      expect(query.data.value).toEqual(all);
      expect(fetch.mock.calls.map(([path]) => path)).toEqual(
        cursors.map((after) => `/api/agent-runs/r1/messages?attempt=2&after=${after}`),
      );
    },
  );

  it('keeps attempts in separate caches and restarts their sequence cursors', async () => {
    const first = messages(500, 1);
    const second = messages(1, 2);
    const fetch = vi.fn(async (path: string) => {
      const params = new URL(path, 'http://localhost').searchParams;
      const all = params.get('attempt') === '1' ? first : second;
      return Response.json(all.filter((m) => m.seq > Number(params.get('after'))).slice(0, 500));
    });
    vi.stubGlobal('fetch', fetch);
    const current = ref(run(1));
    const query = observe(current);
    await vi.waitFor(() => expect(query.isSuccess.value).toBe(true));
    expect(query.data.value).toEqual(first);
    current.value = run(2);
    await vi.waitFor(() => expect(query.data.value).toEqual(second));
    expect(client.getQueryData(keys.runMessages('w1', 'r1', 1))).toEqual(first);
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/api/agent-runs/r1/messages?attempt=1&after=0',
      '/api/agent-runs/r1/messages?attempt=1&after=1000',
      '/api/agent-runs/r1/messages?attempt=2&after=0',
    ]);
  });

  it.each(['completed', 'failed', 'cancelled'] as const)(
    'fetches final output when the current attempt becomes %s before its next poll',
    async (status) => {
      let final = false;
      const fetch = vi.fn(async () => Response.json(final ? messages(2) : []));
      vi.stubGlobal('fetch', fetch);
      const current = ref<AgentRun>({ ...run(), status: 'running' });
      const query = observe(current);
      await vi.waitFor(() => expect(query.data.value).toEqual([]));
      final = true;
      current.value = { ...current.value, status };
      await vi.waitFor(() => expect(query.data.value).toEqual(messages(2)));
      expect(fetch).toHaveBeenCalledTimes(2);
    },
  );

  it('cancels an unfinished live snapshot before fetching the terminal output', async () => {
    let resolveOld!: (value: Response) => void;
    const old = new Promise<Response>((resolve) => {
      resolveOld = resolve;
    });
    const fetch = vi
      .fn()
      .mockImplementationOnce(() => old)
      .mockImplementation(async () => Response.json(messages(2)));
    vi.stubGlobal('fetch', fetch);
    const current = ref<AgentRun>({ ...run(), status: 'running' });
    const query = observe(current);
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    current.value = { ...current.value, status: 'completed' };
    await vi.waitFor(() => expect(query.data.value).toEqual(messages(2)));
    resolveOld(Response.json([]));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(query.data.value).toEqual(messages(2));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it.each(['attempt', 'workspace', 'dispose'] as const)(
    'fences a terminal refresh after %s changes during cancellation',
    async (change) => {
      const fetch = respond(200, '[]');
      const current = ref<AgentRun>({ ...run(1), status: 'running' });
      observe(current);
      await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      const cancel = client.cancelQueries.bind(client);
      const cancelling = vi.spyOn(client, 'cancelQueries').mockImplementation(async (...args) => {
        await cancel(...args);
        await gate;
      });
      current.value = { ...current.value, status: 'completed' };
      await vi.waitFor(() => expect(cancelling).toHaveBeenCalledTimes(1));
      if (change === 'attempt') current.value = run(2);
      else if (change === 'workspace') workspaceId.value = 'w2';
      else scope.stop();
      if (change !== 'dispose') await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
      release();
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(fetch).toHaveBeenCalledTimes(change === 'dispose' ? 1 : 2);
      cancelling.mockRestore();
    },
  );

  it('reports a later-page error instead of presenting a truncated transcript as complete', async () => {
    const fetch = vi.fn(async (path: string) =>
      path.includes('after=1000')
        ? Response.json({ error: 'temporarily unavailable' }, { status: 503 })
        : Response.json(messages(500)),
    );
    vi.stubGlobal('fetch', fetch);
    const query = observe(run());
    await vi.waitFor(() => expect(query.isFetching.value).toBe(false));
    expect(query.error.value).toMatchObject({ status: 503, message: 'temporarily unavailable' });
    expect(query.data.value).toBeUndefined();
  });
});
