import { ref, type Component } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import DecisionBadge from './DecisionBadge.vue';
import DecisionRail from './DecisionRail.vue';
import { decisionFilters } from './decisionFilters';
import DecisionOptions from './DecisionOptions.vue';
import DecisionMeta from './DecisionMeta.vue';
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
const decisionsLoading = ref(false);
const decisionsError = ref<Error | null>(null);
const replace = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: {} }),
  useRouter: () => ({ replace: (...args: unknown[]) => replace(...args) }),
}));
vi.mock('../../api/queries', () => ({
  useDecisionCount: () => ({ data: count }),
  useDecisions: () => ({ data: rows, error: decisionsError, isLoading: decisionsLoading }),
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
  Object.assign(decisionFilters, { type: 'all', date: 'all', from: '', to: '' });
  api.mockReset();
  invalidateQueries.mockReset();
  replace.mockReset();
  count.value = { open: 3, due_soon: 0 };
  me.value = { id: 'me' };
  rows.value = [];
  cards.value = [];
  decisionsLoading.value = false;
  decisionsError.value = null;
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

it('shows each option with its consequence and combines decision metadata', () => {
  const text = textOf(mount(DecisionOptions, { item: item() }));
  expect(text).toContain('Redo with guidance');
  expect(text).toContain('The column runs again.');
  expect(text).toContain('Park');
  const meta = textOf(mount(DecisionMeta, { item: item() }));
  expect(meta).toContain('Agent (Lead)');
  expect(meta).toContain('Scope');
  expect(meta).not.toContain('scope');
  expect(meta).toContain('Expires in 3d');
  expect(meta).toContain('If no one answers, the default is Park.');
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
    textOf(mount(DecisionMeta, { item: item({ decision_kind: '', ...over }) }));
  expect(text({ kind: 'waiting_for_machine' })).toContain('Waiting for machine');
  expect(text({ kind: 'waiting_for_machine' })).not.toContain('waiting_for_machine');
  expect(text({ kind: 'needs_decision' })).not.toContain('Needs a decision');
  expect(text({ kind: 'needs_decision' })).not.toContain('needs_decision');
  expect(text({ kind: 'brand_new' as DecisionItem['kind'] })).not.toContain('brand_new');
});

it('warns on the badge when a decision expires soon', () => {
  const badge = (root: ReturnType<typeof node>) => all(root).find((n) => n.tag === 'span')!;
  expect(badge(mount(DecisionBadge, {})).props.class).not.toContain('due');
  count.value = { open: 3, due_soon: 1 };
  const due = badge(mount(DecisionBadge, {}));
  expect(due.props.class).toContain('due');
  expect(due.props['aria-label']).toBe('Open decisions, 1 due soon');
});

it('marks a reminded row, offers no Archive on an open decision and shows unread only to its addressee', async () => {
  rows.value = [
    item({ id: 'd1', reminded_at: new Date().toISOString() }),
    item({ id: 'd2', user_id: 'someone-else', options: null, kind: 'done' }),
  ];
  const root = mount(DecisionRail, {});
  expect(textOf(root)).toContain('Expires soon');
  expect(all(root).filter((n) => n.props.class === 'unread')).toHaveLength(1);
  const archiveButtons = buttons(root).filter((n) => n.props['aria-label'] === 'Archive decision');
  // The Done row sits in the Reports group, above the open question.
  expect(archiveButtons[0]?.props.disabled).toBe(false);
  expect(archiveButtons[1]?.props.disabled).toBe(true);
  api.mockResolvedValue(undefined);
  await click(archiveButtons[0]);
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d2/archive');
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

it('a Done card opens its study page after its read receipt', async () => {
  api.mockResolvedValue(undefined);
  rows.value = [item({ kind: 'done', text: 'Study #4 is done.' })];
  mount(DecisionsView, { itemId: 'd1' });
  await new Promise((r) => setTimeout(r));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/read');
  expect(replace).toHaveBeenCalledWith('/studies/c1');

  replace.mockReset();
  rows.value = [item({ id: 'd2' })];
  mount(DecisionsView, { itemId: 'd2' });
  await new Promise((r) => setTimeout(r));
  expect(replace).not.toHaveBeenCalled();
});

it('shows the study context and a link to its activity without embedding its sidebar', () => {
  rows.value = [item({ kind: 'agent_decision', text: 'Choose a population\n\nTwo readings.' })];
  cards.value = [{ id: 'c1', number: 4, title: 'Memory study', question: 'Does sleep help?' }];
  const root = mount(DecisionsView, { itemId: 'd1' });
  expect(textOf(root)).toContain('Open study');
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
  const rowLinks = () => all(rail).filter((n) => n.props.to !== undefined && n.props.to !== '/decisions/new');
  expect(rowLinks().map((n) => n.props.to)).toEqual(['/decisions/d1', '/decisions/d2']);
  const options = mount(DecisionOptions, { item: rows.value[0] });
  await click(buttonFor(options, 'Park'));
  await new Promise((r) => setTimeout(r));
  await click(buttonFor(options, 'Submit'));
  await new Promise((r) => setTimeout(r));
  expect(rowLinks().map((n) => n.props.to)).toEqual(['/decisions/d2']);
});

const questionSet = {
  questions: [
    { id: 'single', prompt: 'Which group?', help_text: 'Choose one', required: true, mode: 'single', allow_other: true, options: [
      { id: 'a', label: 'Children', consequence: 'Study children.' }, { id: 'b', label: 'Adults', consequence: 'Study adults.' },
    ] },
    { id: 'multi', prompt: 'Which outcomes?', help_text: null, required: true, mode: 'multiple', allow_other: true, options: [
      { id: 'x', label: 'Recall', consequence: 'Measure recall.' }, { id: 'y', label: 'Attention', consequence: 'Measure attention.' },
    ] },
    { id: 'note', prompt: 'Additional context?', help_text: null, required: false, mode: 'text', allow_other: false, options: [] },
  ],
};

it('stages a complete question set, with custom answer replacing multiple selections, and submits atomically', async () => {
  api.mockResolvedValue(undefined);
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: questionSet } as never) });
  expect(textOf(root)).toContain('Which group?');
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(true);
  await click(buttonFor(root, 'Children')); await click(buttonFor(root, 'Recall')); await click(buttonFor(root, 'Attention'));
  await new Promise((r) => setTimeout(r));
  expect(api).not.toHaveBeenCalled();
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(false);
  const custom = buttons(root).filter((n) => textOf(n).includes('Something else'))[1];
  await click(custom); await new Promise((r) => setTimeout(r));
  const customBox = all(root).find((n) => n.props['aria-label'] === 'Custom answer for Which outcomes?')!;
  (customBox.props.onInput as (e: unknown) => void)({ target: { value: 'Another outcome' } });
  await new Promise((r) => setTimeout(r));
  await click(buttonFor(root, 'Submit'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/answers', { answers: [
    { question_id: 'single', option_ids: ['a'], text: null },
    { question_id: 'multi', option_ids: [], text: 'Another outcome' },
  ] });
});

it('keeps question set input after a failed submit and shows history to non-answerers', async () => {
  api.mockRejectedValueOnce(new Error('Save failed'));
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: { questions: [questionSet.questions[2]] } } as never) });
  const box = all(root).find((n) => n.props['aria-label'] === 'Additional context?')!;
  (box.props.onInput as (e: unknown) => void)({ target: { value: 'Some context' } });
  await new Promise((r) => setTimeout(r)); await click(buttonFor(root, 'Submit')); await new Promise((r) => setTimeout(r));
  expect(textOf(root)).toContain('Save failed');
  expect(all(root).find((n) => n.props['aria-label'] === 'Additional context?')?.props.value).toBe('Some context');
  const history = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], can_answer: false, status: 'decided', question_set: questionSet, answers: [{ question_id: 'single', option_ids: ['a'], text: null }, { question_id: 'multi', option_ids: [], text: 'Another outcome' }] } as never) });
  expect(textOf(history)).toContain('Children'); expect(textOf(history)).toContain('Another outcome'); expect(textOf(history)).toContain('Not answered');
});

it('folds archived decisions separately and allows restoring them', async () => {
  rows.value = [item({ id: 'current', kind: 'done', options: null }), item({ id: 'old', kind: 'done', options: null, archived_at: '2026-11-04T08:00:00Z' })];
  const root = mount(DecisionRail, {});
  expect(textOf(root)).toContain('Archived');
  expect(all(root).filter((n) => n.props.to !== undefined && n.props.to !== '/decisions/new').map((n) => n.props.to)).toEqual(['/decisions/current']);
  const archived = buttons(root).find((n) => n.props['aria-expanded'] === false);
  expect(archived).toBeDefined();
  await click(archived);
  await new Promise((r) => setTimeout(r));
  expect(all(root).filter((n) => n.props.to !== undefined && n.props.to !== '/decisions/new').map((n) => n.props.to)).toEqual(['/decisions/current', '/decisions/old']);
  expect(buttons(root).find((n) => n.props['aria-expanded'] === true)).toBeDefined();
  api.mockResolvedValue(undefined);
  await click(buttons(root).find((n) => n.props['aria-label'] === 'Restore decision'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/old/revive');
});

it('does not offer archive for an open text-only question set', async () => {
  rows.value = [item({ kind: 'agent_decision', options: [], question_set: { questions: [questionSet.questions[2]] } } as never)];
  const root = mount(DecisionRail, {});
  expect(buttons(root).find((n) => n.props['aria-label'] === 'Archive decision')?.props.disabled).toBe(true);
});

it('requires a text answer, permits skipped optional questions and disables controls for non-answerers', async () => {
  const set = { questions: [{ ...questionSet.questions[2], required: true }] };
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: set } as never) });
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(true);
  const box = all(root).find((n) => n.props['aria-label'] === 'Additional context?')!;
  (box.props.onInput as (e: unknown) => void)({ target: { value: '  Answer with spaces  ' } });
  await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(false);
  api.mockResolvedValue(undefined); await click(buttonFor(root, 'Submit'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/answers', { answers: [{ question_id: 'note', option_ids: [], text: '  Answer with spaces  ' }] });
  const other = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: questionSet, can_answer: false } as never) });
  expect(textOf(other)).toContain('Only the study owner');
  expect(buttonFor(other, 'Children')?.props.disabled).toBe(true);
  expect(buttonFor(other, 'Submit')).toBeUndefined();
});

it('renders question sets in the central view and attributes answered history', () => {
  rows.value = [item({ kind: 'agent_decision', text: 'Choose\n\nPlease answer.', options: [], question_set: questionSet,
    answers: [{ question_id: 'single', option_ids: ['a'], text: null }], status: 'decided', decided_by: 'u1' } as never)];
  const root = mount(DecisionsView, { itemId: 'd1' });
  expect(textOf(root)).toContain('Which group?'); expect(textOf(root)).toContain('Children');
  expect(textOf(root)).toContain('by Ada');
});

it('switches multiple selections and custom answers exclusively and enforces the text limit', async () => {
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: { questions: [questionSet.questions[1]] } } as never) });
  await click(buttonFor(root, 'Recall')); await click(buttonFor(root, 'Attention')); await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Recall')?.props['aria-pressed']).toBe(true);
  expect(buttonFor(root, 'Attention')?.props['aria-pressed']).toBe(true);
  await click(buttonFor(root, 'Something else')); await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Recall')?.props['aria-pressed']).toBe(false);
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(true);
  const custom = all(root).find((n) => n.props['aria-label'] === 'Custom answer for Which outcomes?')!;
  (custom.props.onInput as (e: unknown) => void)({ target: { value: 'x'.repeat(100001) } }); await new Promise((r) => setTimeout(r));
  expect(textOf(root)).toContain('100,000 characters'); expect(buttonFor(root, 'Submit')?.props.disabled).toBe(true);
  await click(buttonFor(root, 'Attention')); await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Attention')?.props['aria-pressed']).toBe(true);
  expect(buttonFor(root, 'Something else')?.props['aria-pressed']).toBe(false);
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(false);
});

it('lets optional single and custom choices return to unanswered without losing another answer', async () => {
  const set = { questions: [{ ...questionSet.questions[0], required: false }, questionSet.questions[1]] };
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: set } as never) });
  await click(buttonFor(root, 'Recall')); await click(buttonFor(root, 'Children')); await new Promise((r) => setTimeout(r));
  await click(buttonFor(root, 'Children')); await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Children')?.props['aria-pressed']).toBe(false);
  expect(buttonFor(root, 'Recall')?.props['aria-pressed']).toBe(true);
  const other = buttons(root).filter((n) => textOf(n).includes('Something else'))[0];
  await click(other); await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(true);
  await click(other); await new Promise((r) => setTimeout(r));
  expect(buttonFor(root, 'Submit')?.props.disabled).toBe(false);
  api.mockResolvedValue(undefined); await click(buttonFor(root, 'Submit'));
  expect(api).toHaveBeenCalledWith('POST', '/api/decisions/d1/answers', { answers: [{ question_id: 'multi', option_ids: ['x'], text: null }] });
});

it('clearly identifies single and multiple choice modes', () => {
  const root = mount(DecisionOptions, { item: item({ kind: 'agent_decision', options: [], question_set: questionSet } as never) });
  expect(textOf(root)).toContain('Choose one');
  expect(textOf(root)).toContain('Choose one or more');
});

it('distinguishes a requested decision loading, query failure, missing item and no selection', async () => {
  decisionsLoading.value = true;
  const requested = mount(DecisionsView, { itemId: 'missing' });
  expect(textOf(requested)).toContain('Loading decision');
  decisionsLoading.value = false; decisionsError.value = new Error('Network error');
  await new Promise((r) => setTimeout(r));
  expect(textOf(requested)).toContain('Unable to load decisions');
  decisionsError.value = null; await new Promise((r) => setTimeout(r));
  expect(textOf(requested)).toContain('Decision not found');
  const empty = mount(DecisionsView, {});
  expect(textOf(empty)).toContain('Select a decision');
});

it('applies the same date filter to the rail and selected activity', async () => {
  rows.value = [item({ created_at: new Date().toISOString() })];
  const rail = mount(DecisionRail, {});
  const activity = mount(DecisionsView, { itemId: 'd1' });
  expect(textOf(rail)).toContain('Study');
  expect(textOf(activity)).toContain('scored low twice');
  decisionFilters.date = 'yesterday';
  await new Promise((r) => setTimeout(r));
  expect(textOf(rail)).toContain('No decisions match these filters');
  expect(textOf(activity)).toContain('does not match the current filters');
});


it('links Add to creation and renders a private manual note without expiry wording', () => {
  rows.value = [item({ kind: 'manual_decision', title: 'Private title', context: 'Private context', card_id: null, deadline: '2020-01-01', reminded_at: '2020-01-01', options: null })];
  const root = mount(DecisionRail, {});
  expect(all(root).find(n => n.props['aria-label'] === 'Add decision')?.props.to).toBe('/decisions/new');
  expect(textOf(root)).toContain('Private title');
  expect(textOf(root)).toContain('Private context');
  expect(textOf(root)).toContain('Overdue');
  expect(textOf(root)).toContain('Reminder sent');
  expect(textOf(root)).not.toContain('Expires');
});
it('never renders cached manual rows for a foreign creator or after access failure', async () => {
  rows.value = [item({ kind: 'manual_decision', title: 'Private title', options: null })];
  const rail = mount(DecisionRail, {}), detail = mount(DecisionsView, { itemId: 'd1' });
  me.value = { id: 'other' }; await new Promise(r => setTimeout(r));
  expect(textOf(rail)).not.toContain('Private title'); expect(textOf(detail)).not.toContain('Private title');
  me.value = { id: 'me' }; decisionsError.value = new Error('Forbidden'); await new Promise(r => setTimeout(r));
  expect(textOf(rail)).not.toContain('Private title'); expect(textOf(detail)).not.toContain('Private title');
});
