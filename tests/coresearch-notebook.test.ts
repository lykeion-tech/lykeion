import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import NotebookPane from '../apps/dashboard/src/coresearch/NotebookPane.vue';
import ComputePane from '../apps/dashboard/src/coresearch/ComputePane.vue';
import { keys } from '../apps/dashboard/src/api/keys';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { all, createTreeApp, node, textOf } from './daemon-tree';
import { chatRuntime } from '../apps/dashboard/src/coresearch/notebook-model';
import type { Runtime } from '../apps/dashboard/src/api/types';
import type { ChatRow } from '../apps/dashboard/src/coresearch/types';
vi.hoisted(() => {
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, setItem() {}, removeItem() {} },
  });
});
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const chat = {
  id: 'c',
  workspace_id: 'lab',
  project_id: 'p',
  owner_id: 'other',
  cli: 'claude',
  daemon_id: 'd',
  kernel_state: 'busy',
  kernel_seen_at: '2026-10-26T12:00:00Z',
};
const project = {
  id: 'p',
  workspace_id: 'lab',
  name: 'Residuals',
  python_version: '3.12.8',
  role: 'member',
  archived_at: null,
};
const cell = (id: string, seq: number, outputs: unknown, status = 'ok') => ({
  id,
  seq,
  kernel: 'python3',
  source: 'print(1)',
  status,
  outputs,
  execution_count: seq,
  outputs_blob: null,
  author_user_id: 'me',
  agent_run_id: null,
});
const button = (root: ReturnType<typeof node>, label: string) =>
  all(root).find(
    (n) => n.tag === 'button' && (textOf(n).trim() === label || n.props['aria-label'] === label),
  );
async function mount(
  component: Component,
  owner = false,
  seed?: (c: QueryClient) => void,
  extra = {},
) {
  vi.stubGlobal('document', { activeElement: null });
  vi.stubGlobal('Document', class {});
  vi.stubGlobal('ShadowRoot', class {});
  workspaceId.value = 'lab';
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, enabled: false, staleTime: Infinity, gcTime: Infinity },
    },
  });
  client.setQueryData(keys.coresearchCells('lab', 'p', 'c'), []);
  client.setQueryData(keys.coresearchEnvironment('lab', 'p'), {
    python_version: '3.12.8',
    setups: [],
  });
  client.setQueryData(keys.runtimes('lab'), [
    {
      id: 'r',
      workspace_id: 'lab',
      owner_id: 'other',
      daemon_id: 'd',
      harness: 'claude',
      device_name: 'Private workstation',
      custom_name: null,
      status: 'offline',
      last_seen_at: '2026-10-26T11:00:00Z',
      visibility: 'private',
      active_runs: 9,
    },
  ]);
  seed?.(client);
  const app = createTreeApp({ render: () => h(component, { project, chat, owner, ...extra }) });
  app.use(VueQueryPlugin, { queryClient: client });
  const root = node('root');
  app.mount(root);
  const settle = async () => {
    await new Promise((r) => setTimeout(r, 0));
    await nextTick();
  };
  await settle();
  cleanups.push(() => {
    app.unmount();
    client.clear();
  });
  return { root, client, settle };
}
describe('Coresearch notebook and compute', () => {
  it('machine identity requires every recorded binding field', () => {
    const exact = {
      workspace_id: 'lab',
      owner_id: 'other',
      daemon_id: 'd',
      harness: 'claude',
      status: 'offline',
    } as Runtime;
    expect(chatRuntime(chat as ChatRow, [exact])).toBe(exact);
    for (const field of ['workspace_id', 'owner_id', 'daemon_id', 'harness'])
      expect(chatRuntime(chat as ChatRow, [{ ...exact, [field]: 'wrong' }])).toBeUndefined();
  });
  it('renders png and svg outputs safely and revokes blob URLs', async () => {
    const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:svg-output');
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
    const { root, settle } = await mount(NotebookPane, false, (c) =>
      c.setQueryData(keys.coresearchCells('lab', 'p', 'c'), [
        cell('image', 1, [
          { type: 'image', mime: 'image/png', data: 'cG5n' },
          { type: 'image', mime: 'image/svg+xml', data: 'PHN2Zz48L3N2Zz4=' },
        ]),
      ]),
    );
    (button(root, 'Show output')!.props.onClick as () => void)();
    await settle();
    expect(
      all(root)
        .filter((n) => n.tag === 'img')
        .map((n) => n.props.src),
    ).toEqual(['data:image/png;base64,cG5n', 'blob:svg-output']);
    const outputContainer = all(root).find((n) => n.props.class === 'nbp-outputs')!;
    expect(all(outputContainer).filter((n) => n.tag === 'svg')).toHaveLength(0);
    expect(create).toHaveBeenCalledOnce();
    cleanups.splice(0).forEach((fn) => fn());
    expect(revoke).toHaveBeenCalledWith('blob:svg-output');
  });
  it('error output shows traceback and cells render in seq order', async () => {
    const { root } = await mount(NotebookPane, false, (c) =>
      c.setQueryData(keys.coresearchCells('lab', 'p', 'c'), [
        cell(
          'late',
          2,
          [
            {
              type: 'error',
              ename: 'Error',
              evalue: 'bad',
              traceback: ['Traceback', 'exact failure'],
            },
          ],
          'error',
        ),
        cell('first', 1, []),
      ]),
    );
    expect(
      all(root)
        .filter((n) => String(n.props.class ?? '').includes('nbp-cell') && n.props.id)
        .map((n) => n.props.id),
    ).toEqual(['cell-first', 'cell-late']);
    expect(textOf(root)).toContain('Traceback\nexact failure');
  });
  it('cell editor and all execution/setup controls only for chat owner', async () => {
    const member = await mount(NotebookPane);
    expect(all(member.root).filter((n) => n.tag === 'textarea')).toHaveLength(0);
    for (const label of ['Run', 'Interrupt', 'Restart', 'Set up'])
      expect(button(member.root, label)).toBeUndefined();
    const owner = await mount(NotebookPane, true);
    expect(
      all(owner.root).filter(
        (n) => n.tag === 'textarea' && n.props['aria-label'] === 'Cell source',
      ),
    ).toHaveLength(1);
    for (const label of ['Run', 'Interrupt', 'Restart', 'Set up'])
      expect(button(owner.root, label)).toBeTruthy();
    const runtimeMember = await mount(ComputePane);
    expect(button(runtimeMember.root, 'Interrupt')).toBeUndefined();
    expect(button(runtimeMember.root, 'Restart')).toBeUndefined();
  });
  it('interrupt posts kernel op and owner can submit a free kernel name', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(NotebookPane, true);
    (button(root, 'Interrupt')!.props.onClick as () => void)();
    await settle();
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/coresearch/projects/p/chats/c/kernel');
    expect(JSON.parse(fetch.mock.calls[0]?.[1].body)).toEqual({ op: 'interrupt' });
    const kernel = all(root).find((n) => n.tag === 'input' && n.props['aria-label'] === 'Kernel')!;
    (kernel.props['onUpdate:modelValue'] as (v: string) => void)('custom-kernel');
    const area = all(root).find((n) => n.tag === 'textarea')!;
    (area.props['onUpdate:modelValue'] as (v: string) => void)('print(2)');
    await settle();
    (button(root, 'Run')!.props.onClick as () => void)();
    await settle();
    expect(JSON.parse(fetch.mock.calls[1]?.[1].body)).toEqual({
      kernel: 'custom-kernel',
      source: 'print(2)',
    });
  });
  it('environment bar shows recorded python and latest setup status/log; setup request uses actual fields', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true, status: 201, text: async () => '{}' });
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(NotebookPane, true, (c) =>
      c.setQueryData(keys.coresearchEnvironment('lab', 'p'), {
        python_version: '3.12.8',
        setups: [
          {
            id: 'setup',
            created_at: '2026-10-26T12:00:00Z',
            status: 'failed',
            log: 'solver failed',
            python: '3.13',
            conda: ['numpy'],
          },
        ],
      }),
    );
    expect(textOf(root)).toContain('Python 3.12.8');
    expect(textOf(root)).toContain('failed');
    expect(textOf(root)).toContain('solver failed');
    (button(root, 'Set up')!.props.onClick as () => void)();
    await settle();
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/coresearch/projects/p/environment');
    expect(JSON.parse(fetch.mock.calls[0]?.[1].body)).toEqual({
      chat_id: 'c',
      python: '3.12.8',
      conda: [],
    });
  });
  it('compute shows another owners private offline machine separately from kernel state and queued cells', async () => {
    const { root } = await mount(ComputePane, false, (c) =>
      c.setQueryData(keys.coresearchCells('lab', 'p', 'c'), [cell('q', 1, [], 'queued')]),
    );
    const text = textOf(root);
    expect(text).toContain('Private workstation');
    expect(text).toContain('offline');
    expect(text).toContain('busy');
    expect(text).toContain(chat.kernel_seen_at);
    expect(text).toContain('2026-10-26T11:00:00Z');
    expect(text).toContain('1 queued cell');
    expect(text).not.toMatch(/CPU|Memory|GPU/);
  });
  it('compute matches workspace owner daemon and harness and distinguishes missing and unbound', async () => {
    const missing = await mount(ComputePane, false, (c) =>
      c.setQueryData(keys.runtimes('lab'), [
        {
          workspace_id: 'lab',
          owner_id: 'other',
          daemon_id: 'd',
          harness: 'codex',
          status: 'online',
          device_name: 'Wrong CLI',
        },
      ]),
    );
    expect(textOf(missing.root)).toContain('Machine unavailable');
    expect(textOf(missing.root)).not.toContain('offline');
    const unbound = await mount(ComputePane, false, undefined, {
      chat: { ...chat, daemon_id: null },
    });
    expect(textOf(unbound.root)).toContain('No machine bound');
    expect(textOf(unbound.root)).not.toContain('offline');
  });
  it('loads outputs-only blob endpoint on disclosure with project-scoped query key', async () => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: async () => JSON.stringify([{ type: 'text', text: 'blob result' }]),
    });
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(NotebookPane, false, (c) =>
      c.setQueryData(keys.coresearchCells('lab', 'p', 'c'), [
        { ...cell('blob', 1, null), outputs_blob: 'private-key' },
      ]),
    );
    (button(root, 'Show output')!.props.onClick as () => void)();
    await settle();
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/coresearch/projects/p/chats/c/cells/blob/outputs');
    expect(textOf(root)).toContain('blob result');
  });
});
