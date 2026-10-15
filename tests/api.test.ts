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
import { useGates, useRunMessages } from '../apps/dashboard/src/api/queries';
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

describe('gate queries', () => {
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

  it('returns an empty collection when the server has no gate records', async () => {
    const fetch = respond(200, 'null');
    const query = app.runWithContext(() => scope.run(() => useGates('c1'))!);
    await vi.waitFor(() => expect(query.isSuccess.value).toBe(true));
    expect(query.data.value).toEqual([]);
    expect(fetch.mock.calls[0]![0]).toBe('/api/cards/c1/gates');
    expect(headers(fetch)['X-Workspace-ID']).toBe('w1');
  });

  it('preserves nonempty gate records', async () => {
    const record = {
      id: 'g1',
      workspace_id: 'w1',
      card_id: 'c1',
      board_column: 'grounding',
      round: 1,
      kind: 'gate',
      role: 'system',
      key: 'gate',
      body: { column: 'grounding', round: 1, input_digest: 'digest', checks: [], verdict: 'pass' },
      blob_key: '',
      agent_run_id: null,
      superseded_at: null,
      created_at: '2026-10-15T00:00:00Z',
    };
    respond(200, JSON.stringify([record]));
    const query = app.runWithContext(() => scope.run(() => useGates('c1'))!);
    await vi.waitFor(() => expect(query.isSuccess.value).toBe(true));
    expect(query.data.value).toEqual([record]);
  });
});
