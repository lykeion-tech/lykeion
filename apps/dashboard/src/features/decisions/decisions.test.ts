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
const cards = ref<any[]>([]);
vi.mock('vue-router', () => ({ useRoute: () => ({ params: {} }) }));
vi.mock('../../api/queries', () => ({
  useDecisionCount: () => ({ data: count }),
  useDecisions: () => ({ data: rows, error: ref(null) }),
  useCards: () => ({ data: cards }),
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
  cards.value = [];
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

it('stages a choice and submits it only after explicit confirmation', async () => {
  api.mockResolvedValue(undefined);
  const root = mount(DecisionOptions, { item: item() });
  await click(buttonFor(root, 'Park'));
  await new Promise((r) => setTimeout(r));
  expect(api).not.toHaveBeenCalled();
  expect(buttonFor(root, 'Park')?.props['aria-pressed']).toBe(true);
  await click(buttonFor(root, 'Submit'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/decide', { option: 'park' });
  expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: keys.decisionsAll('w1') });
});

it('requires redo guidance, retaining it across a failed submission', async () => {
  api.mockRejectedValueOnce(new Error('Temporary problem'));
  const root = mount(DecisionOptions, { item: item() });
  await click(buttonFor(root, 'Redo with guidance'));
  await new Promise((r) => setTimeout(r));
  const box = all(root).find((n) => n.tag === 'textarea')!;
  expect(box).toBeDefined();
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(true);
  (box.props.onInput as (e: unknown) => void)({ target: { value: '  use the 2024 cohort ' } });
  await new Promise((r) => setTimeout(r));
  await click(buttonFor(root, 'Submit'));
  await new Promise((r) => setTimeout(r));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/decide', {
    option: 'redo', guidance: 'use the 2024 cohort',
  });
  expect(textOf(root)).toContain('Temporary problem');
  expect(all(root).find((n) => n.tag === 'textarea')?.props.value).toBe('  use the 2024 cohort ');
  expect(buttonFor(root, 'Redo with guidance')?.props['aria-pressed']).toBe(true);
});

it('shows non-answerers the options and explains who can answer', () => {
  const root = mount(DecisionOptions, { item: item({ can_answer: false }) });
  expect(textOf(root)).toContain('Redo with guidance');
  expect(textOf(root)).toContain('Only the study owner or a workspace owner or admin can answer');
  expect(buttonFor(root, 'Redo with guidance')?.props.disabled).toBe(true);
  expect(buttonFor(root, 'Submit')).toBeUndefined();
});

it('allows an agent option named redo without system-stop guidance', async () => {
  api.mockResolvedValue(undefined);
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision' }) });
  await click(buttonFor(root, 'Redo with guidance'));
  await new Promise((r) => setTimeout(r));
  expect(all(root).find((n) => n.tag === 'textarea')).toBeUndefined();
  await click(buttonFor(root, 'Submit'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/decide', { option: 'redo' });
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

it('shows the study context and a link to its activity without embedding its sidebar', () => {
  rows.value = [item({ kind: 'agent_decision', text: 'Choose a population\n\nTwo readings.' })];
  cards.value = [{ id: 'c1', number: 4, title: 'Memory study', question: 'Does sleep help?' }];
  const root = mount(DecisionsView, { itemId: 'd1' });
  expect(textOf(root)).toContain('Memory study');
  expect(textOf(root)).toContain('Does sleep help?');
  expect(all(root).some((n) => n.props.to === '/studies/c1')).toBe(true);
  expect(all(root).some((n) => n.tag === 'StudyDetail')).toBe(false);
});

it('renders a machine_offline item as its text', () => {
  rows.value = [
    item({ kind: 'machine_offline', card_id: null, options: null, text: 'laptop is offline' }),
  ];
  expect(textOf(mount(DecisionsView, { itemId: 'd1' }))).toContain('laptop is offline');
});

it('retains a report after the selected row leaves the capped history', async () => {
  rows.value = [item({ kind: 'autoresearch_report', card_id: null, options: null, autoresearch_run_id: 'r1', text: 'The report is ready' })];
  const root = mount(DecisionsView, { itemId: 'd1' });
  await new Promise((r) => setTimeout(r));
  rows.value = [];
  await new Promise((r) => setTimeout(r));
  expect(textOf(root)).toContain('The report is ready');
  expect(buttonFor(root, 'Open report')).toBeDefined();
});

it('names a studyless machine item and does not duplicate its message', () => {
  rows.value = [item({ kind: 'machine_offline', card_id: null, options: null, text: 'laptop is offline' })];
  const text = textOf(mount(DecisionsView, { itemId: 'd1' }));
  expect(text).toContain('Machine offline');
  expect(text).not.toContain('Study');
  expect(text.match(/laptop is offline/g)).toHaveLength(1);
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
  const options = mount(DecisionOptions, { item: rows.value[0] });
  await click(buttonFor(options, 'Park'));
  await new Promise((r) => setTimeout(r));
  await click(buttonFor(options, 'Submit'));
  await new Promise((r) => setTimeout(r));
  expect(rowLinks().map((n) => n.props.to)).toEqual(['/decisions/d2']);
});
