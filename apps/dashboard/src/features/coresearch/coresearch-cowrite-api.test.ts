import { afterEach, expect, it, vi } from 'vitest';
import { h } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import * as cowrite from './cowrite/api';
import { keys } from '../../api/keys';
import { invalidationsFor } from '../../api/realtime';
import { workspaceId } from '../../api/session';
import { createTreeApp, node } from '../../../../../tests/daemon-tree';
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
      return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = createTreeApp({
    setup() {
      cowrite.useComments('p', 'file');
      cowrite.useBuilds('p');
      cowrite.useVersions('p');
      cowrite.useCompute('p');
      cowrite.useTemplates('p');
      return () => h('div');
    },
  });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(node('root'));
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  await vi.waitFor(() => expect(requests).toHaveLength(5));
  expect(requests.map((r) => r.url).sort()).toEqual(
    [
      '/api/coresearch/projects/p/docs/file/comments',
      '/api/coresearch/projects/p/builds',
      '/api/coresearch/projects/p/versions',
      '/api/coresearch/projects/p/compute',
      '/api/coresearch/templates',
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
// An event reaches only the queries whose data it can change: a keystroke batch
// refetches that file's document, but no viewer's chats, messages or compute.
it.each([
  [
    'coresearch.doc.stepped',
    [
      keys.cowriteDoc('lab', 'p', 'file'),
      keys.cowriteChanges('lab', 'p', 'file'),
      keys.cowriteComments('lab', 'p', 'file'),
      keys.cowriteDocSteps('lab', 'p', 'file', 7),
    ],
    [keys.cowriteBuilds('lab', 'p'), keys.cowriteDoc('lab', 'p', 'other')],
  ],
  [
    'coresearch.comment.changed',
    [keys.cowriteComments('lab', 'p', 'file')],
    [keys.cowriteDoc('lab', 'p', 'file'), keys.cowriteComments('lab', 'p', 'other')],
  ],
  [
    'coresearch.build.changed',
    [keys.cowriteBuilds('lab', 'p'), keys.cowriteVersions('lab', 'p')],
    [keys.cowriteDoc('lab', 'p', 'file'), keys.cowriteChanges('lab', 'p', 'file')],
  ],
] as const)('invalidates only the affected Cowrite queries on %s', (type, hit, missed) => {
  const event = {
    id: 1,
    workspace_id: 'lab',
    type,
    card_id: null,
    payload: { project_id: 'p', file_id: 'file', build_id: 'b' },
  };
  const affected = invalidationsFor('lab', event as never);
  const stale = (key: readonly unknown[]) =>
    affected.some((prefix) => prefix.every((part, i) => part === key[i]));
  for (const key of hit) expect(stale(key), key.join('/')).toBe(true);
  for (const key of [
    ...missed,
    keys.coresearchChats('lab', 'p'),
    keys.coresearchMessages('lab', 'p', 'chat'),
    keys.cowriteCompute('lab', 'p'),
    keys.coresearchProjects('lab'),
  ])
    expect(stale(key), key.join('/')).toBe(false);
  expect(invalidationsFor('other', event as never)).toEqual([]);
});

it('polls builds only while one is queued or running', () => {
  const poll = (...status: string[]) =>
    cowrite.buildPollInterval(status.map((s, i) => ({ id: String(i), status: s }) as never));
  expect(poll()).toBe(false);
  expect(poll('ok', 'failed', 'aborted')).toBe(false);
  expect(poll('ok', 'queued')).toBe(1500);
  expect(poll('running')).toBe(1500);
});

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

