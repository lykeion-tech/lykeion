import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import * as cowrite from '../apps/dashboard/src/coresearch/cowrite/api';
import { keys } from '../apps/dashboard/src/api/keys';
import { invalidationsFor } from '../apps/dashboard/src/api/realtime';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { createTreeApp, node } from './daemon-tree';
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
});
it('reads all Cowrite resources through project-scoped member routes and keys', async () => {
  workspaceId.value = 'lab';
  const requests: { url: string; workspace: string | null }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      requests.push({ url, workspace: new Headers(init.headers).get('X-Workspace-ID') });
      return new Response(
        JSON.stringify(url.endsWith('/docs/file') ? { version: 7, text: 'paper' } : []),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let doc!: ReturnType<typeof cowrite.useDoc>;
  const app = createTreeApp({
    setup() {
      doc = cowrite.useDoc('p', 'file');
      cowrite.useDocSteps('p', 'file', 7);
      cowrite.useChanges('p', 'file');
      cowrite.useComments('p', 'file');
      cowrite.useBuilds('p');
      cowrite.useVersions('p');
      cowrite.useCompute('p');
      cowrite.useTemplates('p');
      cowrite.usePresence('p', 'file', ref(true));
      return () => h('div');
    },
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(node('root'));
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  await vi.waitFor(() => expect(requests).toHaveLength(9));
  await nextTick();
  await vi.waitFor(() => expect(doc.data.value).toEqual({ version: 7, text: 'paper' }));
  expect(requests.map((r) => r.url).sort()).toEqual(
    [
      '/api/coresearch/projects/p/docs/file',
      '/api/coresearch/projects/p/docs/file/steps?after=7',
      '/api/coresearch/projects/p/docs/file/changes',
      '/api/coresearch/projects/p/docs/file/comments',
      '/api/coresearch/projects/p/builds',
      '/api/coresearch/projects/p/versions',
      '/api/coresearch/projects/p/compute',
      '/api/coresearch/templates',
      '/api/coresearch/projects/p/docs/file/presence',
    ].sort(),
  );
  expect(requests.every((r) => r.workspace === 'lab')).toBe(true);
  expect(
    client
      .getQueryCache()
      .getAll()
      .every((q) => q.queryKey.slice(0, 4).join('/') === 'ws/lab/coresearch/p'),
  ).toBe(true);
});
it.each(['coresearch.doc.stepped', 'coresearch.comment.changed', 'coresearch.build.changed'])(
  'invalidates Cowrite queries on %s using ids only',
  (type) => {
    const event = {
      id: 1,
      workspace_id: 'lab',
      type,
      card_id: null,
      payload: { project_id: 'p', file_id: 'file' },
    };
    const affected = invalidationsFor('lab', event as never);
    expect(affected).toContainEqual(['ws', 'lab', 'coresearch', 'p']);
    expect(invalidationsFor('other', event as never)).toEqual([]);
  },
);

it('loads draft templates in a reserved new-project namespace before a session exists', async () => {
  workspaceId.value = 'lab';
  const fetch = vi.fn(
    async () =>
      new Response(JSON.stringify([{ id: 'blank', label: 'Blank' }]), {
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = createTreeApp({
    setup() {
      cowrite.useTemplates();
      return () => h('div');
    },
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(node('root'));
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  await vi.waitFor(() =>
    expect(client.getQueryData(keys.cowriteTemplates('lab', 'new'))).toEqual([
      { id: 'blank', label: 'Blank' },
    ]),
  );
});

it('fences reactive document keys, disabled transitions and cancellation across scopes', async () => {
  workspaceId.value = 'lab-a';
  const project = ref('p-a'),
    file = ref('f-a'),
    after = ref(0),
    open = ref(false);
  const requests: { url: string; workspace: string | null; signal: AbortSignal }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (url: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init.signal as AbortSignal;
          requests.push({
            url,
            workspace: new Headers(init.headers).get('X-Workspace-ID'),
            signal,
          });
          signal.addEventListener(
            'abort',
            () => reject(new DOMException('Aborted', 'AbortError')),
            { once: true },
          );
        }),
    ),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = createTreeApp({
    setup() {
      cowrite.useDocSteps(project, file, after, open);
      return () => h('div');
    },
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(node('root'));
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  await nextTick();
  expect(requests).toHaveLength(0);
  open.value = true;
  await vi.waitFor(() => expect(requests).toHaveLength(1));
  after.value = 500;
  await vi.waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[0]!.signal.aborted).toBe(true);
  file.value = 'f-b';
  await vi.waitFor(() => expect(requests).toHaveLength(3));
  expect(requests[1]!.signal.aborted).toBe(true);
  project.value = 'p-b';
  workspaceId.value = 'lab-b';
  await vi.waitFor(() => expect(requests).toHaveLength(4));
  expect(requests[2]!.signal.aborted).toBe(true);
  expect(requests.map((r) => [r.workspace, r.url])).toEqual([
    ['lab-a', '/api/coresearch/projects/p-a/docs/f-a/steps?after=0'],
    ['lab-a', '/api/coresearch/projects/p-a/docs/f-a/steps?after=500'],
    ['lab-a', '/api/coresearch/projects/p-a/docs/f-b/steps?after=500'],
    ['lab-b', '/api/coresearch/projects/p-b/docs/f-b/steps?after=500'],
  ]);
  open.value = false;
  file.value = 'f-c';
  await nextTick();
  expect(requests).toHaveLength(4);
  expect(requests[3]!.signal.aborted).toBe(true);
  expect(
    new Set(
      client
        .getQueryCache()
        .getAll()
        .map((q) => JSON.stringify(q.queryKey)),
    ).size,
  ).toBe(5);
  open.value = true;
  await vi.waitFor(() => expect(requests).toHaveLength(5));
  app.unmount();
  expect(requests[4]!.signal.aborted).toBe(true);
});
