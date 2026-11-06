import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyOutputs from './StudyOutputs.vue';
vi.mock('./StudyFileThumbnail.vue', () => ({ default: { template: '<span />' } }));
vi.mock('../coresearch/viewers/ArtifactViewer.vue', () => ({ default: { template: '<div />' } }));
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const output = {
  id: 'id',
  key: 'o1',
  board_column: 'grounding',
  round: 1,
  role: 'agent',
  agent_run_id: 'run-1',
  created_at: '2026-10-30T10:00:00Z',
  body: { name: 'results/a.csv', size_bytes: 4 },
};
function setup(
  attachments: { id: string; name: string; size_bytes: number; content_type: string }[] = [],
  runs: InstanceType<typeof StudyOutputs>['$props']['runs'] = [],
) {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData([...keys.recordsAll('w', 'c'), 'kind', 'output'], [output]);
  const id = ref('c'),
    root = node('root');
  const app = createTreeApp({ render: () => h(StudyOutputs, { cardId: id.value, attachments, runs }) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  return {
    client,
    id,
    root,
    dispose: () => {
      app.unmount();
      client.clear();
    },
  };
}
it('shows the producing agent name without exposing the run ID', async () => {
  const t = setup([], [{ id: 'run-1', agent_name: 'leader · Codex Same Model Test', role: 'leader', harness: 'codex' } as NonNullable<InstanceType<typeof StudyOutputs>['$props']['runs']>[number]]);
  await nextTick();
  expect(textOf(t.root)).toContain('Leader · Codex Same Model Test');
  expect(textOf(t.root)).not.toContain('run-1');
  t.dispose();
});
it('downloads the exact saved output using the scoped endpoint', async () => {
  const clicked = vi.fn(),
    fetch = vi.fn(async (_url: string) => new Response(new Blob(['data']), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('document', { createElement: () => ({ click: clicked }) });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const t = setup();
  await nextTick();
  const button = all(t.root).find(
    (n) => n.tag === 'button' && textOf(n).includes('results/a.csv'),
  )!;
  await (button.props.onClick as () => Promise<void>)();
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/cards/c/outputs/o1');
  await nextTick();
  const download = all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Download')!;
  (download.props.onClick as () => void)();
  expect(clicked).toHaveBeenCalledOnce();
  t.dispose();
});
it('does not download a stale response after the study changes', async () => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) =>
      url.includes('/outputs/')
        ? new Promise<Response>((r) => {
            resolve = r;
          })
        : Promise.resolve(new Response('[]', { status: 200 })),
    ),
  );
  const clicked = vi.fn();
  vi.stubGlobal('document', { createElement: () => ({ click: clicked }) });
  const t = setup();
  await nextTick();
  const button = all(t.root).find(
    (n) => n.tag === 'button' && textOf(n).includes('results/a.csv'),
  )!;
  const pending = (button.props.onClick as () => Promise<void>)();
  t.id.value = 'other';
  await nextTick();
  resolve(new Response(new Blob(['data']), { status: 200 }));
  await pending;
  expect(clicked).not.toHaveBeenCalled();
  expect(textOf(t.root)).not.toContain('Download');
  t.dispose();
});

it('shows a list error and retries to an empty state', async () => {
  let finish!: (response: Response) => void;
  const fetch = vi
    .fn()
    .mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          finish = r;
        }),
    )
    .mockResolvedValue(new Response('[]', { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  workspaceId.value = 'w';
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = node('root');
  const app = createTreeApp({ render: () => h(StudyOutputs, { cardId: 'c' }) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  await nextTick();
  expect(textOf(root)).toContain('Loading files');
  finish(new Response(JSON.stringify({ error: 'Output list failed' }), { status: 500 }));
  await vi.waitFor(() => expect(textOf(root)).toContain('Output list failed'));
  const retry = all(root).find((n) => n.tag === 'button' && textOf(n) === 'Retry')!;
  (retry.props.onClick as () => void)();
  await vi.waitFor(() => expect(textOf(root)).toContain('No files yet'));
  expect(all(root).some((n) => n.tag === 'h2' && textOf(n).trim() === 'No files yet')).toBe(true);
  app.unmount();
  client.clear();
});

it('reports a failed output download', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify({ error: 'Download denied' }), { status: 403 })),
  );
  const t = setup();
  await nextTick();
  const button = all(t.root).find(
    (n) => n.tag === 'button' && textOf(n).includes('results/a.csv'),
  )!;
  await (button.props.onClick as () => Promise<void>)();
  await nextTick();
  expect(textOf(t.root)).toContain('Download denied');
  t.dispose();
});

it('refreshes the output list when its record cache is invalidated', async () => {
  const t = setup();
  await nextTick();
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          JSON.stringify([
            output,
            { ...output, id: 'new', key: 'o2', body: { name: 'later.csv' } },
          ]),
          { status: 200 },
        ),
    ),
  );
  await t.client.invalidateQueries({ queryKey: keys.recordsAll('w', 'c') });
  await vi.waitFor(() => expect(textOf(t.root)).toContain('later.csv'));
  t.dispose();
});

it('opens a saved output as a preview and offers an explicit download', async () => {
  const fetch = vi.fn(
    async (_url: string) => new Response(new Blob(['name,value\na,1']), { status: 200 }),
  );
  vi.stubGlobal('fetch', fetch);
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const t = setup();
  await nextTick();
  const button = all(t.root).find(
    (n) => n.tag === 'button' && textOf(n).includes('results/a.csv'),
  )!;
  await (button.props.onClick as () => Promise<void>)();
  await vi.waitFor(() => expect(textOf(t.root)).toContain('Download'));
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/cards/c/outputs/o1');
  expect(textOf(t.root)).toContain('a.csv');
  expect(all(t.root).some((n) => n.tag === 'button' && textOf(n).includes('← Files'))).toBe(false);
  t.dispose();
});

it('previews an attachment with the study-scoped attachment endpoint', async () => {
  const fetch = vi.fn(
    async (_url: string) => new Response(new Blob(['synthetic']), { status: 200 }),
  );
  vi.stubGlobal('fetch', fetch);
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const t = setup([
    { id: 'attach-1', name: 'input.txt', size_bytes: 9, content_type: 'text/plain' },
  ]);
  await nextTick();
  expect(textOf(t.root)).toContain('Your uploads');
  const button = all(t.root).find((n) => n.tag === 'button' && textOf(n).includes('input.txt'))!;
  await (button.props.onClick as () => Promise<void>)();
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/cards/c/attachments/attach-1');
  await nextTick();
  expect(textOf(t.root)).toContain('Download');
  t.dispose();
});

it('streams large attachments directly through the browser download', async () => {
  const fetch = vi.fn(async (_url: string) => new Response(new Blob(['large']), { status: 200 }));
  const clicked = vi.fn();
  const anchor = { href: '', download: '', click: clicked };
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('document', { createElement: () => anchor });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const t = setup([
    {
      id: 'large',
      name: 'large.bin',
      size_bytes: 1_000_000_000,
      content_type: 'application/octet-stream',
    },
  ]);
  await nextTick();
  const button = all(t.root).find((n) => n.tag === 'button' && textOf(n).includes('large.bin'))!;
  await (button.props.onClick as () => Promise<void>)();
  await nextTick();
  expect(fetch).not.toHaveBeenCalled();
  expect(textOf(t.root)).toContain('too large to preview');
  const download = all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Download')!;
  await (download.props.onClick as () => Promise<void>)();
  expect(fetch).not.toHaveBeenCalled();
  expect(anchor.href).toBe('/api/cards/c/attachments/large?download=1&workspace=w');
  expect(clicked).toHaveBeenCalledOnce();
  t.dispose();
});
