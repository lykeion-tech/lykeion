import { expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';
import { File } from 'node:buffer';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const request = vi.hoisted(() => vi.fn());
vi.mock('../../api/client', () => ({ api: request }));
vi.mock('../../api/session', () => ({ workspaceId: { value: 'workspace' } }));
vi.mock('@tanstack/vue-query', () => ({
  useQuery: () => ({ data: ref([{ emoji: '👍', count: 1, mine: true }]) }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));
import StudyDescriptionControls from './StudyDescriptionControls.vue';

it('lets people remove their reaction', async () => {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  const root = node('root');
  const app = createTreeApp(StudyDescriptionControls, { cardId: 'card', attachments: [] });
  app.mount(root);
  const reaction = all(root).find((n) => n.tag === 'button' && textOf(n).includes('👍'))!;
  request.mockResolvedValue([]);
  (reaction.props.onClick as () => void)();
  await nextTick();
  expect(request).toHaveBeenCalledWith(
    'DELETE',
    '/api/cards/card/reactions',
    { emoji: '👍' },
    undefined,
    { workspace: 'workspace' },
  );
  app.unmount();
  vi.unstubAllGlobals();
});

it('uploads a selected file then links it to the study', async () => {
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  request.mockReset();
  request.mockResolvedValueOnce({ id: 'file-id' }).mockResolvedValueOnce({});
  const root = node('root');
  const app = createTreeApp(StudyDescriptionControls, { cardId: 'card', attachments: [] });
  app.mount(root);
  const input = all(root).find((n) => n.tag === 'input' && n.props.type === 'file')!;
  const handler = input.props.onChange as (event: unknown) => Promise<void>;
  await handler({ target: { files: [new File(['data'], 'data.csv')], value: '' } });
  expect(request).toHaveBeenNthCalledWith(
    1,
    'POST',
    '/api/attachments',
    expect.any(FormData),
    undefined,
    { workspace: 'workspace' },
  );
  expect(request).toHaveBeenNthCalledWith(
    2,
    'POST',
    '/api/cards/card/attachments',
    { attachment_ids: ['file-id'] },
    undefined,
    { workspace: 'workspace' },
  );
  app.unmount();
  vi.unstubAllGlobals();
});
