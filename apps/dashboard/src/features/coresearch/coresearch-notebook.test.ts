import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, type Component } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import NotebookPane from './NotebookPane.vue';
import ComputePane from './ComputePane.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import { chatRuntime } from './notebook-model';
import type { Runtime } from '../../api/types';
import type { ChatRow } from './types';
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
  agent_run_id: 'run',
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
  it('keeps agent cells in the notebook and user cells in the bottom code console', async () => {
    const { root, settle } = await mount(NotebookPane, true, (c) =>
      c.setQueryData(keys.coresearchCells('lab', 'p', 'c'), [
        { ...cell('agent', 1, []), agent_run_id: 'run' },
        { ...cell('user', 2, []), agent_run_id: null },
      ]),
    );
    const main = all(root).find((n) => n.props['data-testid'] === 'notebook-cells')!;
    expect(all(main).some((n) => n.props.id === 'cell-agent')).toBe(true);
    expect(all(main).some((n) => n.props.id === 'cell-user')).toBe(false);
    expect(button(root, 'Run code')?.props['aria-expanded']).toBe(false);
    expect(all(root).some((n) => n.props['data-testid'] === 'cell-editor')).toBe(false);
    (button(root, 'Run code')!.props.onClick as () => void)();
    await settle();
    const console = all(root).find((n) => n.props['data-testid'] === 'cell-editor')!;
    expect(all(console).some((n) => n.props.id === 'cell-user')).toBe(true);
    expect(all(console).some((n) => n.props.id === 'cell-agent')).toBe(false);
    expect(all(console).some((n) => n.props['aria-label'] === 'Cell source')).toBe(true);
  });
  it('lets a member read user cell history without offering execution', async () => {
    const { root, settle } = await mount(NotebookPane, false, (c) =>
      c.setQueryData(keys.coresearchCells('lab', 'p', 'c'), [
        { ...cell('user', 1, [{ type: 'text', text: 'user result' }]), agent_run_id: null },
      ]),
    );
    (button(root, 'Code history')!.props.onClick as () => void)();
    await settle();
    expect(all(root).some((n) => n.props.id === 'cell-user')).toBe(true);
    expect(all(root).some((n) => n.props['aria-label'] === 'Cell source')).toBe(false);
    expect(button(root, 'Run')).toBeUndefined();
  });
  it('cell editor and all execution/setup controls only for chat owner', async () => {
    const member = await mount(NotebookPane);
    expect(all(member.root).filter((n) => n.tag === 'textarea')).toHaveLength(0);
    for (const label of ['Run', 'Interrupt', 'Restart', 'Set up'])
      expect(button(member.root, label)).toBeUndefined();
    const owner = await mount(NotebookPane, true);
    (button(owner.root, 'Run code')!.props.onClick as () => void)();
    await owner.settle();
    expect(
      all(owner.root).filter(
        (n) => n.tag === 'textarea' && n.props['aria-label'] === 'Cell source',
      ),
    ).toHaveLength(1);
    for (const label of ['Run', 'Set up']) expect(button(owner.root, label)).toBeTruthy();
    for (const label of ['Interrupt', 'Restart']) expect(button(owner.root, label)).toBeUndefined();
    const runtimeMember = await mount(ComputePane);
    expect(button(runtimeMember.root, 'Interrupt')).toBeUndefined();
    expect(button(runtimeMember.root, 'Restart')).toBeUndefined();
  });
  it('owner can submit a free kernel name', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal('fetch', fetch);
    const { root, settle } = await mount(NotebookPane, true);
    (button(root, 'Run code')!.props.onClick as () => void)();
    await settle();
    const kernel = all(root).find((n) => n.tag === 'input' && n.props['aria-label'] === 'Kernel')!;
    (kernel.props['onUpdate:modelValue'] as (v: string) => void)('custom-kernel');
    const area = all(root).find(
      (n) => n.tag === 'textarea' && n.props['aria-label'] === 'Cell source',
    )!;
    (area.props['onUpdate:modelValue'] as (v: string) => void)('print(2)');
    await settle();
    (button(root, 'Run')!.props.onClick as () => void)();
    await settle();
    const sent = fetch.mock.calls.find(
      ([path, options]) => String(path).endsWith('/cells') && options?.method === 'POST',
    );
    expect(JSON.parse(sent?.[1].body)).toEqual({
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
  it('compute is the L2 compute panel fed only by the caller own compute rows', async () => {
    const empty = await mount(ComputePane, true, (c) =>
      c.setQueryData(keys.cowriteCompute('lab', 'p'), { kernels: [], machines: [] }),
    );
    expect(textOf(empty.root)).toContain('No live kernels');
    expect(textOf(empty.root)).not.toContain('Private workstation');
    const mine = await mount(ComputePane, true, (c) =>
      c.setQueryData(keys.cowriteCompute('lab', 'p'), {
        kernels: [
          {
            chat_id: 'c',
            kernel: 'python3',
            state: 'busy',
            runtime_id: 'r',
            device_name: 'My Mac',
            samples: [{ t: '2026-10-28T10:00:00Z', rss: 1048576, cpu: 150 }],
          },
        ],
        machines: [
          {
            runtime_id: 'r',
            device_name: 'My Mac',
            metrics: {
              mem_used: 1,
              mem_total: 2,
              cpu_used: 150,
              cores: 8,
              disk_free: 10,
              disk_total: 20,
            },
          },
        ],
      }),
    );
    const text = textOf(mine.root);
    expect(text).toContain('My Mac');
    expect(text).toContain('1.5 of 8 cores');
    expect(text).toContain('Memory');
    expect(text).not.toContain('GPU');
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
