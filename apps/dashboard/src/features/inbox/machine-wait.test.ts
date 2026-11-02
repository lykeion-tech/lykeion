import { afterEach, expect, it, vi } from 'vitest';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import MachineWait from './MachineWait.vue';
import type { InboxItem } from '../../api/types';

const api = vi.fn();
const invalidateQueries = vi.fn();
vi.mock('../../api/client', () => ({ api: (...args: unknown[]) => api(...args) }));
vi.mock('@tanstack/vue-query', () => ({ useQueryClient: () => ({ invalidateQueries }) }));

const apps: ReturnType<typeof createTreeApp>[] = [];
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount());
  api.mockReset();
  invalidateQueries.mockReset();
});

const item = (over: Partial<InboxItem>): InboxItem => ({
  id: 'i1',
  workspace_id: 'w1',
  card_id: null,
  autoresearch_run_id: null,
  user_id: 'me',
  kind: 'waiting_for_machine',
  text: 'A run on laptop used WebFetch.',
  read_at: null,
  resolved_at: null,
  created_at: '2026-11-02T00:00:00Z',
  ...over,
});
function mount(i: InboxItem) {
  const root = node('root');
  const app = createTreeApp(MachineWait, { item: i });
  apps.push(app);
  app.mount(root);
  return root;
}
const resolveButton = (root: ReturnType<typeof node>) =>
  all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Resolve');

it('resolves an open machine wait and refreshes the Inbox', async () => {
  api.mockResolvedValue(undefined);
  const root = mount(item({}));
  const button = resolveButton(root);
  expect(button).toBeDefined();
  await (button!.props.onClick as () => Promise<void>)();
  expect(api).toHaveBeenCalledWith('POST', '/api/inbox/i1/resolve');
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['ws', 'w1', 'inbox'] });
});

it('offers no Resolve for a resolved wait or another kind', () => {
  expect(resolveButton(mount(item({ resolved_at: '2026-11-02T01:00:00Z' })))).toBeUndefined();
  expect(resolveButton(mount(item({ kind: 'blocked' })))).toBeUndefined();
});

it('resolves a study held after its machines kept failing its run', async () => {
  api.mockResolvedValue(undefined);
  const root = mount(item({ kind: 'needs_decision', card_id: 'c1', text: 'Study #1 has stopped trying.' }));
  const button = resolveButton(root);
  expect(button).toBeDefined();
  await (button!.props.onClick as () => Promise<void>)();
  expect(api).toHaveBeenCalledWith('POST', '/api/inbox/i1/resolve');
});
