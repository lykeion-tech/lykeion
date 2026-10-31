import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyOutputs from './StudyOutputs.vue';
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
function setup() {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData([...keys.recordsAll('w', 'c'), 'kind', 'output'], [output]);
  const id = ref('c'),
    root = node('root');
  const app = createTreeApp({ render: () => h(StudyOutputs, { cardId: id.value }) });
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
it('downloads the exact saved output using the scoped endpoint', async () => {
  const clicked = vi.fn(),
    fetch = vi.fn(async (_url: string) => new Response(new Blob(['data']), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('document', { createElement: () => ({ click: clicked }) });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const t = setup();
  await nextTick();
  const button = all(t.root).find((n) => n.tag === 'button' && textOf(n) === 'results/a.csv')!;
  await (button.props.onClick as () => Promise<void>)();
  expect(fetch.mock.calls[0]?.[0]).toBe('/api/cards/c/outputs/o1');
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
  const button = all(t.root).find((n) => n.tag === 'button' && textOf(n) === 'results/a.csv')!;
  const pending = (button.props.onClick as () => Promise<void>)();
  t.id.value = 'other';
  await nextTick();
  resolve(new Response(new Blob(['data']), { status: 200 }));
  await pending;
  expect(clicked).not.toHaveBeenCalled();
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
  app.unmount();
  client.clear();
});

it('reports a failed output download', async () => {
  const { notice } = await import('../../state/notice');
  notice.value = '';
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify({ error: 'Download denied' }), { status: 403 })),
  );
  const t = setup();
  await nextTick();
  const button = all(t.root).find((n) => n.tag === 'button' && textOf(n) === 'results/a.csv')!;
  await (button.props.onClick as () => Promise<void>)();
  expect(notice.value).toContain('Download denied');
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
