import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, ref, type Component } from 'vue';
import { useCoresearchContent } from './api';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import FilesPane from './FilesPane.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { guardResourceDrafts, discardDialog, resolveDiscard } from '../../state/resource-draft';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
vi.hoisted(() =>
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, setItem() {}, removeItem() {} },
  }),
);
const cleanups: (() => void)[] = [];
afterEach(() => {
  resolveDiscard(false);
  cleanups.splice(0).forEach((f) => f());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const version = (n = 1, extra = {}) => ({
  file_id: 'f',
  n,
  size: 12,
  sha256: 'same',
  created_at: '2026-10-26',
  agent_run_id: null,
  run_id: null,
  author_user_id: 'me',
  restored_from: null,
  overrode: null,
  ...extra,
});
const file = { id: 'f', path: 'results.txt', head: version() };
const project = { id: 'p', workspace_id: 'lab', python_version: '3.12.8', archived_at: null };
const run = (extra = {}) => ({
  id: 'rid',
  key: 'R1',
  chat_id: 'original',
  script_file_id: 'script',
  script_n: 1,
  inputs: [],
  status: 'ok',
  env_sha: 'abcdef0123456789',
  facts: {},
  rerun_of: null,
  finished_at: '2026-10-26',
  ...extra,
});
const button = (root: ReturnType<typeof node>, label: string) =>
  all(root).find(
    (n) => n.tag === 'button' && (textOf(n).trim() === label || n.props['aria-label'] === label),
  );
async function mount(component: Component, extra = {}, seed?: (c: QueryClient) => void) {
  vi.stubGlobal('document', { activeElement: null });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:controlled');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.coresearchFiles('lab', 'p'), [
    file,
    { id: 'script', path: 'analysis.py', head: version(3) },
  ]);
  client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version()]);
  client.setQueryData(keys.coresearchContent('lab', 'p', 'f', 1), new Blob(['original v1']));
  client.setQueryData(keys.coresearchRuns('lab', 'p'), []);
  client.setQueryData(keys.coresearchChats('lab', 'p'), [{ id: 'original', owner_id: 'me' }]);
  seed?.(client);
  const app = createTreeApp({
    render: () =>
      h(component, {
        project,
        file,
        userId: 'me',
        chat: { id: 'selected', owner_id: 'someone' },
        ...extra,
      }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  const root = node('root');
  app.mount(root);
  const settle = async () => {
    await new Promise((r) => setTimeout(r, 10));
    await nextTick();
  };
  await settle();
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  return { root, client, settle };
}
const reply = (data: unknown, status = 200) => ({
  ok: status < 400,
  status,
  text: async () => JSON.stringify(data),
  blob: async () => new Blob([String(data)]),
});
describe('Coresearch files', () => {
  it('keeps preview actions minimal in the right pane while retaining editing in a workspace tab', async () => {
    const pane = await mount(FilesPane);
    expect(button(pane.root, 'Open file in tab')).toBeUndefined();
    expect(button(pane.root, 'Edit content')).toBeUndefined();
    const download = button(pane.root, 'Download')!;
    expect(download.props.class).toContain('art-download');
    const tab = await mount(FilesPane, { inWorkspaceTab: true });
    expect(button(tab.root, 'Edit content')).toBeTruthy();
  });
  it('downloads a normally previewed selected version', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { root } = await mount(FilesPane, { selectedN: 1 }, (client) => {
      client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version(2), version(1)]);
      client.setQueryData(keys.coresearchFiles('lab', 'p'), [{ ...file, head: version(2) }]);
    });
    const link = { href: '', download: '', click: vi.fn() };
    vi.stubGlobal('document', { activeElement: null, createElement: () => link });
    expect(button(root, 'Download')).toBeTruthy();
    (button(root, 'Download')!.props.onClick as () => void)();
    expect(link.href).toBe(
      '/api/coresearch/projects/p/files/f/versions/1/content?download=1&workspace=lab',
    );
    expect(link.click).toHaveBeenCalledOnce();
    expect(fetch).not.toHaveBeenCalled();
  });
  it.each(['result.fits', 'result.FIT', 'result.fts', 'array.npy'])(
    'fetches a 40 MiB %s preview without reusing a smaller-limit cache entry',
    async (path) => {
      const fetch = vi.fn(async () => new Response(new Uint8Array([0])));
      vi.stubGlobal('fetch', fetch);
      const big = { ...file, path, head: version(1, { size: 40 * 1024 * 1024 }) };
      const { root } = await mount(FilesPane, { file: big }, (client) => {
        client.setQueryData(keys.coresearchFiles('lab', 'p'), [big]);
        client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [big.head]);
        client.setQueryData(keys.coresearchContent('lab', 'p', 'f', 1), new Blob(['stale text']));
      });
      await vi.waitFor(() => expect(fetch).toHaveBeenCalled());
      expect(textOf(root)).not.toContain('too large to preview');
      expect(textOf(root)).not.toContain('stale text');
      expect(button(root, 'Edit content')).toBeUndefined();
    },
  );
  it.each(['result.fits', 'array.npy'])('refuses metadata above 50 MiB for %s', async (path) => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const big = { ...file, path, head: version(1, { size: 50 * 1024 * 1024 + 1 }) };
    const { root } = await mount(FilesPane, { file: big }, (client) => {
      client.setQueryData(keys.coresearchFiles('lab', 'p'), [big]);
      client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [big.head]);
    });
    expect(textOf(root)).toContain('too large to preview');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('accepts scientific preview bytes above 32 MiB and keeps them in a separate bounded cache entry', async () => {
    const bytes = new Uint8Array(33 * 1024 * 1024);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(bytes)),
    );
    const big = { ...file, path: 'data.npy', head: version(1, { size: bytes.length }) };
    const { root, client } = await mount(FilesPane, { file: big }, (cache) => {
      cache.setQueryData(keys.coresearchFiles('lab', 'p'), [big]);
      cache.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [big.head]);
    });
    const key = [...keys.coresearchContent('lab', 'p', 'f', 1), 'maxBytes', 50 * 1024 * 1024];
    await vi.waitFor(() => expect(client.getQueryData<Blob>(key)?.size).toBe(bytes.length));
    expect(button(root, 'Edit content')).toBeUndefined();
  });
  it('cancels an oversized scientific response despite understated headers and metadata', async () => {
    let cancelled = false;
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array(26 * 1024 * 1024));
        controller.enqueue(new Uint8Array(25 * 1024 * 1024));
      },
      cancel() {
        cancelled = true;
      },
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(stream, { headers: { 'Content-Length': '1' } })),
    );
    const big = { ...file, path: 'data.npy', head: version(1, { size: 40 * 1024 * 1024 }) };
    const { root } = await mount(FilesPane, { file: big }, (client) => {
      client.setQueryData(keys.coresearchFiles('lab', 'p'), [big]);
      client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [big.head]);
    });
    await vi.waitFor(() => expect(textOf(root)).toContain('too large to preview'));
    expect(cancelled).toBe(true);
    expect(button(root, 'Edit content')).toBeUndefined();
  });
  it('isolates large preview versions when an older request finishes late', async () => {
    workspaceId.value = 'lab';
    const pending = new Map<string, (response: Response) => void>();
    const signals = new Map<string, AbortSignal>();
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init: RequestInit) => {
        signals.set(url, init.signal as AbortSignal);
        return new Promise<Response>((resolve) => pending.set(url, resolve));
      }),
    );
    const selected = ref(1);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
    });
    const app = createTreeApp({
      setup() {
        const content = useCoresearchContent('p', 'f', selected, 50 * 1024 * 1024);
        return () => h('div', String(content.data.value?.size ?? 'waiting'));
      },
    });
    app.use(VueQueryPlugin, { queryClient: client });
    const root = node('root');
    app.mount(root);
    const first = '/api/coresearch/projects/p/files/f/versions/1/content';
    const second = '/api/coresearch/projects/p/files/f/versions/2/content';
    await vi.waitFor(() => expect(pending.has(first)).toBe(true));
    selected.value = 2;
    await vi.waitFor(() => expect(pending.has(second)).toBe(true));
    expect(signals.get(first)?.aborted).toBe(true);
    pending.get(second)!(new Response('new'));
    await vi.waitFor(() => expect(textOf(root)).toBe('3'));
    pending.get(first)!(new Response('outdated'));
    await nextTick();
    expect(textOf(root)).toBe('3');
    app.unmount();
    client.clear();
  });
  it('keeps compressed FITS under the regular 32 MiB limit', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const big = { ...file, path: 'result.fits.gz', head: version(1, { size: 40 * 1024 * 1024 }) };
    const { root } = await mount(FilesPane, { file: big }, (client) => {
      client.setQueryData(keys.coresearchFiles('lab', 'p'), [big]);
      client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [big.head]);
    });
    expect(textOf(root)).toContain('too large to preview');
    expect(fetch).not.toHaveBeenCalled();
  });
  it('offers a download without fetching a file too large to preview', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const big = { ...file, head: version(1, { size: 40 * 1024 * 1024 }) };
    const { root } = await mount(FilesPane, { file: big }, (client) => {
      client.setQueryData(keys.coresearchFiles('lab', 'p'), [big]);
      client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [big.head]);
      client.removeQueries({ queryKey: keys.coresearchContent('lab', 'p', 'f', 1) });
    });
    expect(fetch).not.toHaveBeenCalled();
    expect(textOf(root)).toContain('too large to preview');
    const link = { href: '', download: '', click: vi.fn() };
    vi.stubGlobal('document', { activeElement: null, createElement: () => link });
    (button(root, 'Download')!.props.onClick as () => void)();
    expect(fetch).not.toHaveBeenCalled();
    expect(link.href).toBe(
      '/api/coresearch/projects/p/files/f/versions/1/content?download=1&workspace=lab',
    );
    expect(link.click).toHaveBeenCalledOnce();
  });
  it('offers download when the response exceeds its reported file size', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () => new Response('x', { headers: { 'Content-Length': String(40 * 1024 * 1024) } }),
      ),
    );
    const { root } = await mount(FilesPane, {}, (client) => {
      client.removeQueries({ queryKey: keys.coresearchContent('lab', 'p', 'f', 1) });
    });
    await vi.waitFor(() => expect(textOf(root)).toContain('too large to preview'));
    expect(button(root, 'Download')).toBeTruthy();
  });
  it('remote head update after typing pins draft bytes and base; stale save offers force and reload', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(reply({ error: 'stale_base', base: 1, current: 2 }, 409))
      .mockImplementation((url: string, options: RequestInit) =>
        Promise.resolve(
          reply(
            options.method === 'GET'
              ? url.endsWith('/versions')
                ? [version(3), version(2), version(1)]
                : url.endsWith('/files')
                  ? [{ ...file, head: version(3) }]
                  : 'saved'
              : version(3),
          ),
        ),
      );
    vi.stubGlobal('fetch', fetch);
    const { root, client, settle } = await mount(FilesPane, { inWorkspaceTab: true });
    (button(root, 'Edit content')!.props.onClick as () => void)();
    await settle();
    const draft = 'my exact draft\n';
    const area = all(root).find((n) => n.tag === 'textarea')!;
    (area.props['onUpdate:modelValue'] as (s: string) => void)(draft);
    await settle();
    client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version(2), version(1)]);
    client.setQueryData(keys.coresearchFiles('lab', 'p'), [{ ...file, head: version(2) }]);
    await settle();
    expect(all(root).find((n) => n.tag === 'textarea')!.value).toBe(draft);
    expect(textOf(root)).toContain('v1 (old)');
    (button(root, 'Save')!.props.onClick as () => void)();
    await settle();
    expect(textOf(root)).toContain('version 1 is not the latest (2 is)');
    expect(button(root, 'Reload')).toBeTruthy();
    const first = fetch.mock.calls[0]![1].body as FormData;
    expect(first.get('base')).toBe('1');
    expect(await (first.get('file') as Blob).text()).toBe(draft);
    (button(root, 'Save anyway')!.props.onClick as () => void)();
    await settle();
    const forced = fetch.mock.calls[1]![1].body as FormData;
    expect(forced.get('force')).toBe('true');
    expect(forced.get('base')).toBe('1');
    expect(await (forced.get('file') as Blob).text()).toBe(draft);
  });
  it('restore creates new version using latest head base', async () => {
    const fetch = vi
      .fn()
      .mockImplementation((url: string, options: RequestInit) =>
        Promise.resolve(
          reply(
            options.method === 'GET'
              ? url.endsWith('/versions')
                ? [version(4, { restored_from: 1 }), version(3), version(1)]
                : url.endsWith('/files')
                  ? [{ ...file, head: version(4) }]
                  : 'restored'
              : version(4, { restored_from: 1 }),
          ),
        ),
      );
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(FilesPane, { selectedN: 1 }, (c) =>
      c.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [
        version(3),
        version(2),
        version(1),
      ]),
    );
    (button(root, 'Restore this version')!.props.onClick as () => void)();
    await settle();
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual({ n: 1, base: 3 });
  });
  it('svg preview is an img from controlled blob and revoked', async () => {
    const { root } = await mount(FilesPane, { file: { ...file, path: 'figure.svg' } }, (c) =>
      c.setQueryData(
        keys.coresearchContent('lab', 'p', 'f', 1),
        new Blob(['<svg onload="bad()"></svg>']),
      ),
    );
    expect(all(root).find((n) => n.tag === 'img')?.props.src).toBe('blob:controlled');
    expect(all(root).filter((n) => n.tag === 'svg')).toHaveLength(0);
    cleanups.splice(0).forEach((f) => f());
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:controlled');
  });
  it('unknown binary offers download', async () => {
    const { root } = await mount(FilesPane, { file: { ...file, path: 'unknown.bin' } }, (c) =>
      c.setQueryData(
        keys.coresearchContent('lab', 'p', 'f', 1),
        new Blob([new Uint8Array([0, 255, 1])]),
      ),
    );
    expect(textOf(root)).toContain('Download');
    expect(all(root).some((n) => n.tag === 'textarea')).toBe(false);
  });
  it('recorded run provenance resolves id then R key and exact script_n', async () => {
    const fetch = vi
      .fn()
      .mockImplementation((url: string) =>
        Promise.resolve(
          url.endsWith('/runs/R1') ? reply({ ...run(), outputs: [] }) : reply('script v1'),
        ),
      );
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(FilesPane, {}, (c) => {
      c.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version(1, { run_id: 'rid' })]);
      c.setQueryData(keys.coresearchRuns('lab', 'p'), [run()]);
    });
    (button(root, 'Code')!.props.onClick as () => void)();
    await settle();
    await settle();
    expect(
      fetch.mock.calls.some(
        (c) => c[0] === '/api/coresearch/projects/p/files/script/versions/1/content',
      ),
    ).toBe(true);
    expect(textOf(root)).toContain('analysis.py v1');
    expect(button(root, 'Environment')).toBeUndefined();
  });
  it('clean reads follow remote head while explicit selected versions stay pinned', async () => {
    for (const selectedN of [undefined, 1]) {
      const { root, client, settle } = await mount(FilesPane, { selectedN });
      client.setQueryData(keys.coresearchContent('lab', 'p', 'f', 2), new Blob(['new head bytes']));
      client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version(2), version(1)]);
      await settle();
      expect(textOf(root)).toContain(selectedN ? 'original v1' : 'new head bytes');
      if (selectedN) expect(textOf(root)).toContain('v1 (old)');
    }
  });
  it('diff refuses both the source byte and line bounds', async () => {
    for (const byteLimit of [true, false]) {
      const { root, settle } = await mount(FilesPane, {}, (c) => {
        c.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [
          version(2, { size: byteLimit ? 262145 : 10000 }),
          version(1),
        ]);
        c.setQueryData(
          keys.coresearchContent('lab', 'p', 'f', 2),
          new Blob([
            byteLimit ? 'small fixture' : Array.from({ length: 2501 }, () => 'line').join('\n'),
          ]),
        );
      });
      (button(root, 'Show changes')!.props.onClick as () => void)();
      await settle();
      expect(textOf(root)).toContain('Too large to compare.');
    }
  });
  it('existing Shiki loader highlights diff and markdown and preserves plain text', async () => {
    const { highlightToolCode } = await import('../../lib/tool-code');
    const diff = await highlightToolCode('-before\n+after', 'diff');
    expect(diff?.[0]?.map((t) => t.content).join('')).toBe('-before');
    expect(diff?.[0]?.[0]?.color).not.toBe(diff?.[1]?.[0]?.color);
    expect((await highlightToolCode('# Heading', 'markdown'))?.length).toBe(1);
    expect(
      (await highlightToolCode('unchanged <text>', 'text'))?.[0]?.map((t) => t.content).join(''),
    ).toBe('unchanged <text>');
  });
  it('comparison with an older binary version is honestly unavailable', async () => {
    const { root, settle } = await mount(FilesPane, {}, (c) => {
      c.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version(2), version(1)]);
      c.setQueryData(keys.coresearchContent('lab', 'p', 'f', 2), new Blob(['new text']));
      c.setQueryData(
        keys.coresearchContent('lab', 'p', 'f', 1),
        new Blob([new Uint8Array([0, 255])]),
      );
    });
    (button(root, 'Show changes')!.props.onClick as () => void)();
    await settle();
    expect(textOf(root)).toContain('These versions cannot be compared as text.');
  });
  it('registered file draft shares navigation refusal and accepted discard without advancing base', async () => {
    const { root, client, settle } = await mount(FilesPane, { inWorkspaceTab: true });
    (button(root, 'Edit content')!.props.onClick as () => void)();
    await settle();
    const bytes = 'navigation draft\n';
    const area = all(root).find((n) => n.tag === 'textarea')!;
    (area.props['onUpdate:modelValue'] as (v: string) => void)(bytes);
    await settle();
    client.setQueryData(keys.coresearchVersions('lab', 'p', 'f'), [version(2), version(1)]);
    await settle();
    const declined = guardResourceDrafts();
    expect(discardDialog.value).toBe(true);
    resolveDiscard(false);
    expect(await declined).toBe(false);
    await settle();
    expect(all(root).find((n) => n.tag === 'textarea')!.value).toBe(bytes);
    expect(textOf(root)).toContain('v1 (old)');
    const accepted = guardResourceDrafts();
    expect(discardDialog.value).toBe(true);
    resolveDiscard(true);
    expect(await accepted).toBe(true);
    await settle();
    expect(all(root).filter((n) => n.tag === 'textarea')).toHaveLength(0);
    expect(await guardResourceDrafts()).toBe(true);
    expect(discardDialog.value).toBe(false);
  });
});
