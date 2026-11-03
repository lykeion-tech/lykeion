import { ref, type Component } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import DecisionBadge from './DecisionBadge.vue';
import DecisionRail from './DecisionRail.vue';
import DecisionOptions from './DecisionOptions.vue';
import DecisionsView from './DecisionsView.vue';
import { invalidationsFor } from '../../api/realtime';
import { keys } from '../../api/keys';
import type { DecisionItem } from '../../api/types';

const api = vi.fn();
const invalidateQueries = vi.fn();
const count = ref<{ open: number; due_soon: number } | undefined>({ open: 3, due_soon: 0 });
const me = ref<{ id: string } | undefined>({ id: 'me' });
vi.mock('../../api/client', () => ({ api: (...args: unknown[]) => api(...args) }));
vi.mock('@tanstack/vue-query', () => ({ useQueryClient: () => ({ invalidateQueries }) }));
const rows = ref<DecisionItem[]>([]);
vi.mock('vue-router', () => ({ useRoute: () => ({ params: {} }) }));
vi.mock('../../api/queries', () => ({
  useDecisionCount: () => ({ data: count }),
  useDecisions: () => ({ data: rows, error: ref(null) }),
  useCards: () => ({ data: ref([]) }),
  useMe: () => ({ data: me }),
  useMembers: () => ({
    data: ref([
      { user_id: 'u1', name: 'Ada', email: 'ada@lab.org', role: 'owner', created_at: '' },
    ]),
  }),
}));
vi.mock('../../api/session', async () => ({ workspaceId: (await import('vue')).ref('w1') }));
vi.mock('../studies/StudyDetail.vue', () => ({ default: { render: () => null } }));
vi.mock('../autoresearch/AutoresearchRunDialog.vue', () => ({ default: { render: () => null } }));

const apps: ReturnType<typeof createTreeApp>[] = [];
afterEach(() => {
  apps.splice(0).forEach((app) => app.unmount());
  api.mockReset();
  invalidateQueries.mockReset();
  count.value = { open: 3, due_soon: 0 };
  me.value = { id: 'me' };
  rows.value = [];
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
  reminded_at: null,
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
  expect(textOf(root)).toContain('by Ada');
});

it('shows the open count in the sidebar and refreshes it on inbox.created', () => {
  expect(textOf(mount(DecisionBadge, {}))).toBe('3');
  count.value = { open: 0, due_soon: 0 };
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

it('warns on the badge when a decision expires soon', () => {
  const badge = (root: ReturnType<typeof node>) => all(root).find((n) => n.tag === 'span')!;
  expect(badge(mount(DecisionBadge, {})).props.class).not.toContain('due');
  count.value = { open: 3, due_soon: 1 };
  const due = badge(mount(DecisionBadge, {}));
  expect(due.props.class).toContain('due');
  expect(due.props['aria-label']).toBe('Open decisions, 1 expiring soon');
});

it('marks a reminded row, offers no Archive on an open decision and shows unread only to its addressee', async () => {
  rows.value = [
    item({ id: 'd1', reminded_at: new Date().toISOString() }),
    item({ id: 'd2', user_id: 'someone-else', options: null, kind: 'done' }),
  ];
  const root = mount(DecisionRail, {});
  expect(textOf(root)).toContain('Expires soon');
  expect(all(root).filter((n) => n.props.class === 'unread')).toHaveLength(1);
  const menus = buttons(root).filter((n) => n.props['aria-label'] === 'Organise');
  await click(menus[0]);
  await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Archive')).toBeUndefined();
  await click(menus[1]);
  await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Archive')).toBeDefined();
});

it('shows an agent question with its options and only its addressee reads it', async () => {
  api.mockResolvedValue(undefined);
  rows.value = [
    item({ kind: 'agent_decision', text: 'Which population?\n\nThe question reads two ways.' }),
  ];
  const root = mount(DecisionsView, { itemId: 'd1' });
  await new Promise((r) => setTimeout(r));
  expect(textOf(root)).toContain('Which population?');
  expect(textOf(root)).toContain('The question reads two ways.');
  expect(buttonFor(root, 'Redo with guidance')).toBeDefined();
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/read');

  api.mockReset();
  rows.value = [item({ id: 'd2', user_id: 'someone-else' })];
  mount(DecisionsView, { itemId: 'd2' });
  await new Promise((r) => setTimeout(r));
  expect(api).not.toHaveBeenCalled();
});

it('renders a machine_offline item as its text', () => {
  rows.value = [
    item({ kind: 'machine_offline', card_id: null, options: null, text: 'laptop is offline' }),
  ];
  expect(textOf(mount(DecisionsView, { itemId: 'd1' }))).toContain('laptop is offline');
});

it('drops a decided row from the Open list once the list refreshes', async () => {
  api.mockResolvedValue(undefined);
  rows.value = [item(), item({ id: 'd2' })];
  // The refreshed list no longer holds the decided item.
  invalidateQueries.mockImplementation(async () => {
    rows.value = rows.value.filter((r) => r.id !== 'd1');
  });
  const rail = mount(DecisionRail, {});
  const rowLinks = () => all(rail).filter((n) => n.props.to !== undefined);
  expect(rowLinks().map((n) => n.props.to)).toEqual(['/decisions/d1', '/decisions/d2']);
  await click(buttonFor(mount(DecisionOptions, { item: rows.value[0] }), 'Park'));
  await new Promise((r) => setTimeout(r));
  expect(rowLinks().map((n) => n.props.to)).toEqual(['/decisions/d2']);
});
