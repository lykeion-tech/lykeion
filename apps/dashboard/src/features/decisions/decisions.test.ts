import { ref, type Component } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import DecisionBadge from './DecisionBadge.vue';
import DecisionRail from './DecisionRail.vue';
import DecisionOptions from './DecisionOptions.vue';
import { invalidationsFor } from '../../api/realtime';
import { keys } from '../../api/keys';
import type { DecisionItem } from '../../api/types';

const api = vi.fn();
const invalidateQueries = vi.fn();
const count = ref<number | undefined>(3);
vi.mock('../../api/client', () => ({ api: (...args: unknown[]) => api(...args) }));
vi.mock('@tanstack/vue-query', () => ({ useQueryClient: () => ({ invalidateQueries }) }));
const rows = ref<DecisionItem[]>([]);
vi.mock('vue-router', () => ({ useRoute: () => ({ params: {} }) }));
vi.mock('../../api/queries', () => ({
  useDecisionCount: () => ({ data: count }),
  useDecisions: () => ({ data: rows, error: ref(null) }),
  useCards: () => ({ data: ref([]) }),
}));

const apps: ReturnType<typeof createTreeApp>[] = [];
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount());
  api.mockReset();
  invalidateQueries.mockReset();
  count.value = 3;
});

const item = (over: Partial<DecisionItem> = {}): DecisionItem => ({
  id: 'd1',
  workspace_id: 'w1',
  card_id: 'c1',
  autoresearch_run_id: null,
  user_id: 'me',
  kind: 'blocked',
  text: 'Study #1 scored low twice.',
  read_at: null,
  resolved_at: null,
  created_at: '2026-11-03T00:00:00Z',
  options: [
    { id: 'redo', label: 'Redo with guidance', consequence: 'The column runs again.' },
    { id: 'park', label: 'Park', consequence: 'The study stops here.' },
  ],
  default_option: 'park',
  status: 'open',
  expires_at: new Date(Date.now() + 3 * 86_400_000).toISOString(),
  chosen_option: '',
  guidance: '',
  decided_by: null,
  decided_at: null,
  raised_by: 'agent',
  from_member: 'Lead',
  decision_kind: 'scope',
  decide_by: '',
  decide_by_date: null,
  snoozed_until: null,
  kept: false,
  archived_at: null,
  can_answer: true,
  ...over,
});
function mount(component: Component, props: Record<string, unknown>) {
  const root = node('root');
  const app = createTreeApp(component, props);
  apps.push(app);
  app.mount(root);
  return root;
}
const buttons = (root: ReturnType<typeof node>) => all(root).filter((n) => n.tag === 'button');
const buttonFor = (root: ReturnType<typeof node>, label: string) =>
  buttons(root).find((n) => textOf(n).includes(label));
const click = async (n: ReturnType<typeof node> | undefined) =>
  await (n!.props.onClick as () => Promise<void>)();

it('shows each option with its consequence, the raiser and the expiry', () => {
  const text = textOf(mount(DecisionOptions, { item: item() }));
  expect(text).toContain('Redo with guidance');
  expect(text).toContain('The column runs again.');
  expect(text).toContain('Park');
  expect(text).toContain('Agent (Lead)');
  expect(text).toContain('Scope');
  expect(text).not.toContain('scope');
  expect(text).toContain('Expires in 3d');
});

it('asks for guidance on redo and will not send it empty', async () => {
  const root = mount(DecisionOptions, { item: item() });
  await click(buttonFor(root, 'Redo with guidance'));
  const box = all(root).find((n) => n.tag === 'textarea')!;
  expect(box).toBeDefined();
  await click(buttonFor(root, 'Redo with guidance'));
  expect(api).not.toHaveBeenCalled();
  (box.props.onInput as (e: unknown) => void)({ target: { value: '  use the 2024 cohort ' } });
  await new Promise((r) => setTimeout(r));
  await click(buttonFor(root, 'Redo with guidance'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/decide', {
    option: 'redo',
    guidance: 'use the 2024 cohort',
  });
});

it('sends an option that needs no guidance straight away', async () => {
  api.mockResolvedValue(undefined);
  await click(buttonFor(mount(DecisionOptions, { item: item() }), 'Park'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/decide', { option: 'park' });
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: keys.decisionsAll('w1') });
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: keys.decisionCount('w1') });
});

it('shows a non-answerer no buttons', () => {
  expect(buttons(mount(DecisionOptions, { item: item({ can_answer: false }) }))).toHaveLength(0);
});

it('shows a decided item its choice and who made it, without buttons', () => {
  const root = mount(DecisionOptions, {
    item: item({
      status: 'decided',
      chosen_option: 'redo',
      guidance: 'use the 2024 cohort',
      decided_by: 'u1',
      decided_at: new Date().toISOString(),
      can_answer: false,
    }),
  });
  expect(buttons(root)).toHaveLength(0);
  expect(textOf(root)).toContain('Redo with guidance');
  expect(textOf(root)).toContain('use the 2024 cohort');
});

it('shows the open count in the sidebar and refreshes it on inbox.created', () => {
  expect(textOf(mount(DecisionBadge, {}))).toBe('3');
  count.value = 0;
  expect(textOf(mount(DecisionBadge, {}))).toBe('');
  const event = {
    type: 'inbox.created',
    workspace_id: 'w1',
    card_id: null,
    payload: {},
  } as never;
  expect(invalidationsFor('w1', event)).toContainEqual(keys.decisionCount('w1'));
  expect(invalidationsFor('w1', event)).toContainEqual(keys.decisionsAll('w1'));
});

it('renders the rail for machine_offline and for a kind it does not know', () => {
  rows.value = [
    item({
      id: 'm1',
      kind: 'machine_offline',
      card_id: null,
      options: null,
      text: 'laptop went offline',
    }),
    item({ id: 'x1', kind: 'brand_new' as DecisionItem['kind'], options: null }),
  ];
  const text = textOf(mount(DecisionRail, {}));
  expect(text).toContain('Machine offline');
  expect(text).toContain('Decision: Study #1 scored low twice.');
});

it('names the item kind on a decision instead of showing its id', () => {
  const text = (over: Partial<DecisionItem>) =>
    textOf(mount(DecisionOptions, { item: item({ decision_kind: '', ...over }) }));
  expect(text({ kind: 'waiting_for_machine' })).toContain('Waiting for machine');
  expect(text({ kind: 'waiting_for_machine' })).not.toContain('waiting_for_machine');
  expect(text({ kind: 'needs_decision' })).toContain('Needs a decision');
  expect(text({ kind: 'needs_decision' })).not.toContain('needs_decision');
  expect(text({ kind: 'brand_new' as DecisionItem['kind'] })).not.toContain('brand_new');
});
