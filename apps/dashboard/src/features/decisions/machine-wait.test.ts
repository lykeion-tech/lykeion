import { afterEach, expect, it, vi } from 'vitest';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import MachineWait from './MachineWait.vue';
import { isMachineWait } from '../../lib/studies';
import type { DecisionItem } from '../../api/types';

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

const item = (over: Partial<DecisionItem>): DecisionItem => ({
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
  options: null,
  default_option: '',
  status: 'open',
  expires_at: null,
  chosen_option: '',
  guidance: '',
  decided_by: null,
  decided_at: null,
  raised_by: 'system',
  from_member: '',
  decision_kind: '',
  decide_by: '',
  decide_by_date: null,
  snoozed_until: null,
  kept: false,
  archived_at: null,
  can_answer: true,
  ...over,
});
function mount(i: DecisionItem) {
  const root = node('root');
  const app = createTreeApp(MachineWait, { item: i });
  apps.push(app);
  app.mount(root);
  return root;
}
const resolveButton = (root: ReturnType<typeof node>) =>
  all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Resolve');

it('resolves an open machine wait and refreshes the Decisions page', async () => {
  api.mockResolvedValue(undefined);
  const root = mount(item({}));
  const button = resolveButton(root);
  expect(button).toBeDefined();
  await (button!.props.onClick as () => Promise<void>)();
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/i1/resolve');
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['ws', 'w1', 'decisions'] });
});

it('offers no Resolve for a resolved wait or another kind', () => {
  expect(resolveButton(mount(item({ resolved_at: '2026-11-02T01:00:00Z' })))).toBeUndefined();
  expect(resolveButton(mount(item({ kind: 'blocked' })))).toBeUndefined();
});

it('offers no Resolve for a study whose agent could not do its column', () => {
  const held = item({
    kind: 'needs_decision',
    cause: 'could_not_do',
    card_id: 'c1',
    text: 'Study #1: the agent could not do this column: no data',
  });
  expect(isMachineWait(held)).toBe(false);
  expect(isMachineWait(item({ kind: 'needs_decision', cause: 'machine_failures' }))).toBe(true);
  expect(resolveButton(mount(held))).toBeUndefined();
});

it('offers no Resolve for a study held after its machines kept failing: it has options', () => {
  const root = mount(
    item({ kind: 'needs_decision', cause: 'machine_failures', card_id: 'c1', text: 'Stopped.' }),
  );
  expect(resolveButton(root)).toBeUndefined();
});
