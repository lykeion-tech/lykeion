import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createApp,
  ssrContextKey,
  effectScope,
  ref,
  type App,
  type EffectScope,
  type MaybeRefOrGetter,
} from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { useRunMessages, useInbox, useAutoresearchRun } from './queries';
import { keys } from './keys';
import type { AgentRun, RunMessage } from './types';
import { api, ApiError } from './client';
import { workspaceId } from './session';

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
    agent_id: null,
    agent_name: null,
    assignment: '',
    latest_text: '',
    last_activity_at: null,
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
        cursors.map(
          (after) => `/api/agent-runs/r1/messages?after_attempt=${after ? 2 : 0}&after=${after}`,
        ),
      );
    },
  );

  it('reads every attempt in order, its cursor crossing from one attempt to the next', async () => {
    const first = messages(500, 1);
    const second = messages(1, 2);
    let recorded = first;
    const fetch = vi.fn(async (path: string) => {
      const params = new URL(path, 'http://localhost').searchParams;
      const from = [Number(params.get('after_attempt')), Number(params.get('after'))] as const;
      const after = (m: RunMessage) =>
        m.attempt > from[0] || (m.attempt === from[0] && m.seq > from[1]);
      return Response.json(recorded.filter(after).slice(0, 500));
    });
    vi.stubGlobal('fetch', fetch);
    const current = ref(run(1));
    const query = observe(current);
    await vi.waitFor(() => expect(query.isSuccess.value).toBe(true));
    expect(query.data.value).toEqual(first);
    recorded = [...first, ...second];
    current.value = run(2);
    await vi.waitFor(() => expect(query.data.value).toEqual(recorded));
    expect(client.getQueryData(keys.runMessages('w1', 'r1', 1))).toEqual(first);
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/api/agent-runs/r1/messages?after_attempt=0&after=0',
      '/api/agent-runs/r1/messages?after_attempt=1&after=1000',
      '/api/agent-runs/r1/messages?after_attempt=0&after=0',
      '/api/agent-runs/r1/messages?after_attempt=1&after=1000',
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

describe('shared capability HTTP client', () => {
  it('sends folder FormData with relative filenames and browser-managed content type', async () => {
    workspaceId.value = 'w2';
    const fetch = respond(201, '{}');
    const body = new FormData();
    body.append('files', new Blob([new Uint8Array([0, 255, 7])]), 'folder/data/a.bin');
    await api('POST', '/api/skills/import', body, undefined, { workspace: 'w1' });
    expect(fetch.mock.calls[0]![1].body).toBe(body);
    expect(headers(fetch)).toEqual({ 'X-Workspace-ID': 'w1' });
    expect((body.get('files') as File).name).toBe('folder/data/a.bin');
  });
  it('reads exact binary bytes through the same error boundary', async () => {
    const signal = new AbortController().signal;
    const fetch = vi.fn(
      async (_path: string, _init: RequestInit) =>
        new Response(new Uint8Array([0, 255, 7]), {
          headers: { 'Content-Type': 'application/octet-stream' },
        }),
    );
    vi.stubGlobal('fetch', fetch);
    const file = await api<Blob>('GET', '/api/skills/s1/files?path=x', undefined, undefined, {
      workspace: 'w1',
      signal,
      response: 'blob',
    });
    expect(new Uint8Array(await file.arrayBuffer())).toEqual(new Uint8Array([0, 255, 7]));
    expect(file.type).toBe('application/octet-stream');
    expect(fetch.mock.calls[0]![1]).toMatchObject({
      signal,
      credentials: 'same-origin',
      headers: { 'X-Workspace-ID': 'w1' },
    });
    respond(404, '{"error":"gone"}');
    await expect(
      api('GET', '/api/skills/s1/files', undefined, undefined, { response: 'blob' }),
    ).rejects.toMatchObject({ status: 404, message: 'gone' });
  });
});

describe('shared library queries and mutations', () => {
  let scope: EffectScope;
  let client: QueryClient;
  let app: App;
  const detail = {
    id: 's1',
    policy_enabled: true,
    policy_revision: 0,
    name: 'method',
    domain: 'Workspace',
    description: 'A method',
    origin: 'imported',
    revision: 2,
    created_by: 'u1',
    source: { kind: 'local', location: 'method.zip' },
    contains_scripts: false,
    editable: true,
    content: 'instructions',
    files: [
      {
        path: 'data/a.bin',
        size: 3,
        sha256: 'c1b36a4fef8a99c5fc89a5ba2fd1c2b3c37f50de590ad4f4c5edc1242d1a3528',
        binary: true,
      },
    ],
  };
  const observe = <T>(make: () => T): T => app.runWithContext(() => scope.run(make))!;
  beforeEach(() => {
    workspaceId.value = 'w1';
    ({ scope, client, app } = queryHarness());
  });
  afterEach(() => {
    scope.stop();
    client.clear();
  });
  it('keeps bundled ID colon literal while escaping other skill ID characters', async () => {
    const library = await import('./library');
    const fetch = vi.fn(async (_path: string) => Response.json({ ...detail, id: 'bundle:aeon' }));
    vi.stubGlobal('fetch', fetch);
    const bundled = observe(() => library.useSkillDetail('bundle:aeon'));
    await vi.waitFor(() => expect(bundled.data.value?.id).toBe('bundle:aeon'));
    const mutations = observe(library.useLibraryMutations);
    await mutations.updateSkill('bundle:aeon', { revision: 1, content: 'read-only' });
    await mutations.deleteSkill('a/b?#', 1);
    expect(fetch.mock.calls.map(([path]) => path)).toContain('/api/skills/bundle:aeon');
    expect(fetch.mock.calls.map(([path]) => path)).not.toContain('/api/skills/bundle%3Aaeon');
    expect(fetch.mock.calls.map(([path]) => path)).toContain('/api/skills/a%2Fb%3F%23');
  });
  it('preserves nullable Inbox identities and resolves a report without a card', async () => {
    const item = {
      id: 'i1',
      card_id: null,
      autoresearch_run_id: 'r1',
      kind: 'autoresearch_report',
    };
    const response = {
      autoresearch_id: 'a1',
      autoresearch_title: 'Research',
      execution_mode: 'run_only',
      run: {
        id: 'r1',
        card_id: null,
        source: 'manual',
        status: 'running',
        report: '# Report\nFull text',
        planned_at: '2026-10-18T12:00:00Z',
      },
    };
    const fetch = vi.fn(async (path: string) =>
      Response.json(path === '/api/inbox' ? [item] : response),
    );
    vi.stubGlobal('fetch', fetch);
    const inbox = observe(() => useInbox(false));
    const run = observe(() => useAutoresearchRun('r1'));
    await vi.waitFor(() => expect(inbox.data.value?.[0]?.card_id).toBeNull());
    expect(inbox.data.value?.[0]?.autoresearch_run_id).toBe('r1');
    await vi.waitFor(() => expect(run.data.value).toEqual(response));
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/api/inbox',
      '/api/autoresearch-runs/r1',
    ]);
    expect(client.getQueryData(keys.autoresearchRun('w1', 'r1'))).toEqual(response);
  });
  it('uses shared authoritative list/detail keys and keeps bodies out of list projections', async () => {
    const library = await import('./library');
    const { content: _content, files: _files, ...summary } = detail;
    const catalogue = {
      name: 'geo',
      domain: 'Biology',
      purpose: 'Data',
      tier: 'open',
      terms: 'Public',
      enabled: false,
      policy_enabled: true,
      reason: 'key required',
      key_set: true,
      takes_key: true,
      licence_confirmed: true,
      revision: 7,
    };
    const mcp = {
      id: 'm1',
      name: 'lab',
      transport: 'url',
      revision: 4,
      created_by: 'u1',
      created_at: 'now',
      updated_at: 'now',
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string) =>
        Response.json(
          path === '/api/skills'
            ? [summary]
            : path === '/api/skills/s1'
              ? detail
              : path === '/api/connectors'
                ? [catalogue]
                : [mcp],
        ),
      ),
    );
    const skills = observe(library.useSkillLibrary);
    const skill = observe(() => library.useSkillDetail('s1'));
    const connectors = observe(library.useConnectorLibrary);
    const custom = observe(library.useWorkspaceMCPServers);
    await vi.waitFor(() => expect(skills.data.value).toEqual([summary]));
    await vi.waitFor(() => expect(skill.data.value).toEqual(detail));
    await vi.waitFor(() => expect(connectors.data.value).toEqual([catalogue]));
    await vi.waitFor(() => expect(custom.data.value).toEqual([mcp]));
    expect(client.getQueryData(keys.skills('w1'))).toEqual([summary]);
    expect(client.getQueryData(keys.connectors('w1'))).toEqual([catalogue]);
    expect(client.getQueryData(keys.workspaceMCP('w1'))).toEqual([mcp]);
    expect(skills.data.value?.[0]).not.toHaveProperty('content');
    expect(custom.data.value?.[0]).not.toHaveProperty('config');
  });
  it('verifies byte-exact files against captured size and SHA256', async () => {
    const library = await import('./library');
    const bytes = new Uint8Array([0, 255, 7]);
    const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), (b) =>
      b.toString(16).padStart(2, '0'),
    ).join('');
    const saved = { ...detail, files: [{ ...detail.files[0]!, sha256: hash }] };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string) =>
        path.includes('/files?') ? new Response(bytes) : Response.json(saved),
      ),
    );
    const file = observe(() => library.useSkillFile('s1', 'data/a.bin'));
    await vi.waitFor(() => expect(file.data.value).toBeInstanceOf(Blob));
    expect(new Uint8Array(await file.data.value!.arrayBuffer())).toEqual(bytes);
    expect(client.getQueryData(keys.skillFile('w1', 's1', 2, 'data/a.bin'))).toBe(file.data.value);
  });
  it('rejects bytes that changed after metadata was read and refreshes only captured metadata', async () => {
    const library = await import('./library');
    client.setQueryData(keys.skill('w1', 's1'), detail);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string) =>
        path.includes('/files?') ? new Response(new Uint8Array([0, 1, 2])) : Response.json(detail),
      ),
    );
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const file = observe(() => library.useSkillFile('s1', 'data/a.bin'));
    await vi.waitFor(() => expect(file.error.value?.message).toMatch(/changed/));
    expect(file.data.value).toBeUndefined();
    expect(client.getQueryData(keys.skillFile('w1', 's1', 2, 'data/a.bin'))).toBeUndefined();
    expect(
      invalidate.mock.calls.map(([f]) => (f as { queryKey: unknown }).queryKey),
    ).toContainEqual(keys.skill('w1', 's1'));
    expect(
      invalidate.mock.calls.map(([f]) => (f as { queryKey: unknown }).queryKey),
    ).not.toContainEqual(keys.skills('w2'));
  });
  it('captures mutation workspace and identity and does not populate another workspace on late success', async () => {
    const library = await import('./library');
    let resolve!: (value: Response) => void;
    const fetch = vi.fn(
      (_path: string, _init: RequestInit) =>
        new Promise<Response>((done) => {
          resolve = done;
        }),
    );
    vi.stubGlobal('fetch', fetch);
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const mutations = observe(library.useLibraryMutations);
    const pending = mutations.updateSkill('s1', { revision: 1, content: 'draft' });
    workspaceId.value = 'w2';
    resolve(Response.json(detail));
    await expect(pending).resolves.toEqual(detail);
    expect(headers(fetch)['X-Workspace-ID']).toBe('w1');
    expect(fetch.mock.calls[0]![0]).toBe('/api/skills/s1');
    expect(client.getQueryData(keys.skill('w2', 's1'))).toBeUndefined();
    expect(invalidate.mock.calls.map(([f]) => (f as { queryKey: unknown }).queryKey)).toEqual([
      keys.skills('w1'),
      keys.skill('w1', 's1'),
    ]);
  });
  it('sends exact CRUD/import and catalogue revision bodies without caching write-only config', async () => {
    const library = await import('./library');
    const fetch = respond(200, JSON.stringify(detail));
    const mutations = observe(library.useLibraryMutations);
    await mutations.createSkill({ name: 'method', description: 'Description' });
    await mutations.updateSkill('s1', { revision: 2, content: 'new' });
    await mutations.deleteSkill('s1', 2);
    await mutations.importSkillURL('https://github.com/org/repo/tree/main/skill');
    await mutations.importSkillArchive(new Blob(['zip']), 'method.skill');
    await mutations.importSkillFolder([
      { path: 'method/SKILL.md', data: new Blob(['main']) },
      { path: 'method/data/a.bin', data: new Blob([new Uint8Array([0, 255])]) },
    ]);
    await mutations.setConnector('geo', {
      revision: 7,
      enabled: true,
      key: 'secret',
      licence_confirmed: true,
    });
    const config = { url: 'https://mcp.example', headers: { Authorization: 'secret' } };
    await mutations.createWorkspaceMCP({ name: 'lab', config });
    await mutations.updateWorkspaceMCP('m1', { name: 'lab', revision: 4, config });
    await mutations.deleteWorkspaceMCP('m1', 4);
    expect(fetch.mock.calls.map(([path, init]) => [init.method, path])).toEqual([
      ['POST', '/api/skills'],
      ['PATCH', '/api/skills/s1'],
      ['DELETE', '/api/skills/s1'],
      ['POST', '/api/skills/import'],
      ['POST', '/api/skills/import'],
      ['POST', '/api/skills/import'],
      ['PUT', '/api/connectors/geo'],
      ['POST', '/api/mcp-servers'],
      ['PUT', '/api/mcp-servers/m1'],
      ['DELETE', '/api/mcp-servers/m1'],
    ]);
    expect(JSON.parse(fetch.mock.calls[1]![1].body as string)).toEqual({
      revision: 2,
      content: 'new',
    });
    expect(JSON.parse(fetch.mock.calls[2]![1].body as string)).toEqual({ revision: 2 });
    expect(JSON.parse(fetch.mock.calls[3]![1].body as string)).toEqual({
      url: 'https://github.com/org/repo/tree/main/skill',
    });
    expect((fetch.mock.calls[4]![1].body as FormData).get('file')).toMatchObject({
      name: 'method.skill',
    });
    expect(
      (fetch.mock.calls[5]![1].body as FormData).getAll('files').map((f) => (f as File).name),
    ).toEqual(['method/SKILL.md', 'method/data/a.bin']);
    expect(JSON.parse(fetch.mock.calls[6]![1].body as string)).toEqual({
      revision: 7,
      enabled: true,
      key: 'secret',
      licence_confirmed: true,
    });
    expect(JSON.parse(fetch.mock.calls[8]![1].body as string)).toEqual({
      name: 'lab',
      revision: 4,
      config,
    });
    expect(JSON.parse(fetch.mock.calls[9]![1].body as string)).toEqual({ revision: 4 });
    expect(
      JSON.stringify(
        client
          .getQueryCache()
          .getAll()
          .map((q) => q.state.data),
      ),
    ).not.toContain('secret');
  });
  it('starts and reads runtime jobs through operation-specific endpoints and shared job identity', async () => {
    const library = await import('./library');
    const job = {
      id: 'j1',
      runtime_id: 'rt1',
      operation: 'import',
      status: 'running',
      keys: ['opaque'],
      result: { items: [{ key: 'opaque', status: 'conflict', error: 'duplicate' }] },
    };
    const fetch = respond(200, JSON.stringify(job));
    const mutations = observe(library.useLibraryMutations);
    await mutations.listRuntimeSkills('rt1');
    await mutations.importRuntimeSkills('rt1', ['opaque']);
    const query = observe(() => library.useRuntimeSkillRequest('rt1', 'j1', 'import'));
    await vi.waitFor(() => expect(query.data.value).toEqual(job));
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/api/runtimes/rt1/local-skills',
      '/api/runtimes/rt1/local-skills/import',
      '/api/runtimes/rt1/local-skills/import/j1',
    ]);
    expect(JSON.parse(fetch.mock.calls[1]![1].body as string)).toEqual({ keys: ['opaque'] });
    expect(client.getQueryData(keys.runtimeSkillRequest('w1', 'rt1', 'j1'))).toEqual(job);
  });
  it('does not let old-workspace list or file responses populate the new library', async () => {
    const library = await import('./library');
    const bytes = new Uint8Array([0, 255, 7]);
    const sha256 = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), (b) =>
      b.toString(16).padStart(2, '0'),
    ).join('');
    const saved = { ...detail, files: [{ ...detail.files[0]!, sha256 }] };
    let finishFile!: (value: Response) => void;
    let finishList!: (value: Response) => void;
    const fetch = vi.fn(async (path: string, init: RequestInit) => {
      const workspace = (init.headers as Record<string, string>)['X-Workspace-ID'];
      if (workspace === 'w2')
        return Response.json(path === '/api/skills' ? [] : { ...saved, id: 's2', files: [] });
      if (path.includes('/files?'))
        return new Promise<Response>((done) => {
          finishFile = done;
        });
      if (path === '/api/skills')
        return new Promise<Response>((done) => {
          finishList = done;
        });
      return Response.json(saved);
    });
    vi.stubGlobal('fetch', fetch);
    const list = observe(library.useSkillLibrary);
    const file = observe(() => library.useSkillFile('s1', 'data/a.bin'));
    await vi.waitFor(() => expect(finishFile).toBeTypeOf('function'));
    workspaceId.value = 'w2';
    await vi.waitFor(() => expect(list.data.value).toEqual([]));
    finishFile(new Response(bytes));
    finishList(Response.json([saved]));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(list.data.value).toEqual([]);
    expect(file.data.value).toBeUndefined();
    expect(client.getQueryData(keys.skillFile('w2', 's1', 2, 'data/a.bin'))).toBeUndefined();
    expect(client.getQueryData(keys.skillFile('w1', 's1', 2, 'data/a.bin'))).toBeUndefined();
  });
  it('Team drafts retain missing selections and ignore old availability policy', async () => {
    const { default: TeamForm } = await import('../features/teams/TeamForm.vue');
    const { createMemoryHistory, createRouter } = await import('vue-router');
    app.use(createRouter({ history: createMemoryHistory(), routes: [] }));
    app.provide(ssrContextKey, { modules: new Set() });
    const agents = [
      { id: 'a1', name: 'Leader', visibility: 'workspace', harness: 'codex', model: 'one' },
      { id: 'a2', name: 'Auditor', visibility: 'workspace', harness: 'claude', model: 'two' },
    ];
    const responses: Record<string, unknown> = {
      '/api/me': { id: 'u1' },
      '/api/agents': agents,
      '/api/skills': [detail],
      '/api/connectors': [{ name: 'geo' }],
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (path: string) => Response.json(responses[path])),
    );
    type Form = {
      name: { value: string };
      leader: { value: { agent: string; role: string } };
      auditor: { value: { agent: string; role: string } };
      members: {
        value: Array<{
          name: string;
          agent: string;
          role: string;
          skills: string[];
          connectors: Array<{ kind: string; name: string; value: string; env: string }>;
        }>;
      };
      canCreate: { value: boolean };
      problem: { value: string };
    };
    const setup = (TeamForm as unknown as { setup: (props: object, context: object) => Form })
      .setup;
    const form = observe(() => setup({}, { expose: () => {} }));
    form.name.value = 'Team';
    form.leader.value = { agent: 'a1', role: 'Lead' };
    form.auditor.value = { agent: 'a2', role: 'Audit' };
    form.members.value = [
      {
        name: 'researcher',
        agent: 'a1',
        role: 'Research',
        skills: ['method'],
        connectors: [{ kind: 'catalogue', name: 'geo', value: '', env: '' }],
      },
    ];
    await vi.waitFor(() => expect(form.canCreate.value).toBe(true));
    client.setQueryData(keys.skills('w1'), []);
    await vi.waitFor(() => expect(form.canCreate.value).toBe(false));
    expect(form.members.value[0]!.skills).toEqual(['method']);
    client.setQueryData(keys.skills('w1'), [detail]);
    client.setQueryData(keys.connectors('w1'), []);
    await vi.waitFor(() => expect(form.canCreate.value).toBe(false));
    expect(form.members.value[0]!.connectors[0]!.name).toBe('geo');
    client.setQueryData(keys.connectors('w1'), [{ name: 'geo' }]);
    await vi.waitFor(() => expect(form.canCreate.value).toBe(true));
    client.setQueryData(keys.skills('w1'), [{ ...detail, policy_enabled: false }]);
    await vi.waitFor(() => expect(form.canCreate.value).toBe(true));
    expect(form.members.value[0]!.skills).toEqual(['method']);
    client.setQueryData(keys.skills('w1'), [detail]);
    client.setQueryData(keys.workspaceMCP('w1'), [
      { id: 'm1', name: 'custom', policy_enabled: true, policy_revision: 0 },
    ]);
    form.members.value[0]!.connectors = [
      { kind: 'workspace', name: 'custom', value: 'm1', env: '' },
    ];
    await vi.waitFor(() => expect(form.canCreate.value).toBe(true));
    client.setQueryData(keys.workspaceMCP('w1'), [
      { id: 'm1', name: 'custom', policy_enabled: false, policy_revision: 1 },
    ]);
    await vi.waitFor(() => expect(form.canCreate.value).toBe(true));
    expect(form.members.value[0]!.connectors[0]!.value).toBe('m1');
  });
  it('retains real stale revision errors and does not publish a failed draft', async () => {
    const library = await import('./library');
    respond(409, '{"error":"stale revision","code":"stale_revision","revision":3}');
    const mutations = observe(library.useLibraryMutations);
    await expect(
      mutations.updateSkill('s1', { revision: 2, content: 'draft' }),
    ).rejects.toMatchObject({ status: 409, body: { code: 'stale_revision', revision: 3 } });
    expect(client.getQueryData(keys.skill('w1', 's1'))).toBeUndefined();
  });
});
