import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import FilesPane from '../apps/dashboard/src/coresearch/FilesPane.vue';
import RunsPane from '../apps/dashboard/src/coresearch/RunsPane.vue';
import { keys } from '../apps/dashboard/src/api/keys';
import { workspaceId } from '../apps/dashboard/src/api/session';
import {
  guardResourceDrafts,
  discardDialog,
  resolveDiscard,
} from '../apps/dashboard/src/state/resource-draft';
import { all, createTreeApp, node, textOf } from './daemon-tree';
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
describe('Coresearch files and runs', () => {
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
    const { root, client, settle } = await mount(FilesPane);
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
    (button(root, 'Environment')!.props.onClick as () => void)();
    await settle();
    expect(textOf(root)).toContain('abcdef0123456789');
    expect(textOf(root)).toContain('3.12.8');
  });
  it('badge requires successful rerun; rerun permission belongs to original chat', async () => {
    const { root } = await mount(RunsPane, {}, (c) => {
      c.setQueryData(keys.coresearchRuns('lab', 'p'), [
        run(),
        run({ id: 'r2', key: 'R2', rerun_of: 'rid', facts: { matches_original: true } }),
        run({
          id: 'r3',
          key: 'R3',
          status: 'error',
          rerun_of: 'rid',
          facts: { matches_original: true },
        }),
      ]);
    });
    expect(textOf(root).match(/matches original/g)).toHaveLength(1);
    expect(button(root, 'Rerun R1')).toBeTruthy();
    expect(button(root, 'New run')).toBeUndefined();
  });
  it('members cannot start runs or rerun another owners original', async () => {
    const { root } = await mount(RunsPane, { userId: 'member' }, (c) =>
      c.setQueryData(keys.coresearchRuns('lab', 'p'), [run()]),
    );
    expect(button(root, 'New run')).toBeUndefined();
    expect(button(root, 'Rerun R1')).toBeUndefined();
  });
  it('output link emits immutable v1 even when same hash v2 and different head v3', async () => {
    const onOpenFile = vi.fn();
    const { root, settle } = await mount(RunsPane, { onOpenFile }, (c) => {
      c.setQueryData(keys.coresearchRuns('lab', 'p'), [run()]);
      c.setQueryData(keys.coresearchRun('lab', 'p', 'R1'), {
        ...run(),
        outputs: [{ file_id: 'f', path: 'results.txt', n: 1, sha256: 'same', size: 12 }],
      });
      c.setQueryData(keys.coresearchFiles('lab', 'p'), [
        { ...file, head: version(3, { sha256: 'different' }) },
      ]);
    });
    (button(root, 'Outputs R1')!.props.onClick as () => void)();
    await settle();
    (button(root, 'results.txt v1')!.props.onClick as () => void)();
    expect(onOpenFile).toHaveBeenCalledWith(expect.objectContaining({ file_id: 'f', n: 1 }));
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
  it('owner new run picks saved paths and rerun uses original R key', async () => {
    const fetch = vi
      .fn()
      .mockImplementation((url: string, options: RequestInit) =>
        Promise.resolve(reply(options.method === 'GET' ? [] : run())),
      );
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(
      RunsPane,
      { chat: { id: 'selected', owner_id: 'me' } },
      (c) => c.setQueryData(keys.coresearchRuns('lab', 'p'), [run()]),
    );
    (button(root, 'Rerun R1')!.props.onClick as () => void)();
    await settle();
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/coresearch/projects/p/runs/R1/rerun');
    (button(root, 'New run')!.props.onClick as () => void)();
    await settle();
    const script = all(root).find((n) => n.tag === 'select' && n.props['aria-label'] === 'Script')!;
    (script.props['onUpdate:modelValue'] as (v: string) => void)('analysis.py');
    const inputs = all(root).find((n) => n.tag === 'select' && n.props['aria-label'] === 'Inputs')!;
    (inputs.props['onUpdate:modelValue'] as (v: string[]) => void)(['results.txt']);
    await settle();
    (all(root).find((n) => n.tag === 'form')!.props.onSubmit as (e: unknown) => void)({
      preventDefault() {},
    });
    await settle();
    const post = fetch.mock.calls.find(
      (c) => c[0] === '/api/coresearch/projects/p/runs' && c[1].method === 'POST',
    );
    expect(JSON.parse(post?.[1].body)).toEqual({
      chat_id: 'selected',
      script: 'analysis.py',
      inputs: ['results.txt'],
    });
  });
  it('existing Shiki loader highlights diff and markdown and preserves plain text', async () => {
    const { highlightToolCode } = await import('../apps/dashboard/src/lib/tool-code');
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
    const { root, client, settle } = await mount(FilesPane);
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
