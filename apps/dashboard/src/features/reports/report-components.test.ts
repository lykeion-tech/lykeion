import { afterEach, expect, it } from 'vitest';
import { h, nextTick, ref, type Component } from 'vue';
import type { ReportNode as Node, StudyReport } from '../../api/types';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import ReportNode from './ReportNode.vue';
import ReportPane from './ReportPane.vue';

const disposals: (() => void)[] = [];
afterEach(() => disposals.splice(0).forEach((dispose) => dispose()));

function mount(component: Component, props: () => Record<string, unknown>) {
  const root = node('root');
  const app = createTreeApp({ render: () => h(component, props()) });
  app.mount(root);
  disposals.push(() => app.unmount());
  return root;
}
const press = (n: ReturnType<typeof node>) => (n.props.onClick as () => void)();

const reportNode = (n: Partial<Node> & Pick<Node, 'id' | 'kind'>): Node => ({
  label: n.id,
  status: '',
  reasons: [],
  quote: '',
  checks: [],
  run_id: null,
  agent: '',
  column: '',
  ...n,
});
const report: StudyReport = {
  card_id: 'c',
  number: 1,
  title: 't',
  question: 'Does sleep help memory?',
  stages: [],
  finished_at: '2026-11-10T10:00:00Z',
  hand_in: null,
  nodes: [
    reportNode({ id: 'question', kind: 'question', label: 'Does sleep help memory?' }),
    reportNode({ id: 'rec:r1', kind: 'paper', label: 'doi:10.1/a', status: 'pass' }),
    reportNode({ id: 'rec:r2', kind: 'hypothesis', label: 'Sleep helps', status: 'kept' }),
    reportNode({
      id: 'rec:r5',
      kind: 'finding',
      label: 'Sleep helps memory',
      status: 'supported',
      quote: 'Recall rose after sleep.',
      checks: [
        { id: 'supported', by: 'jev', status: 'pass', p: 0.8, question: 'Does the run back it?' },
      ],
    }),
  ],
  edges: [
    { from: 'question', to: 'rec:r2', kind: 'tests' },
    { from: 'rec:r1', to: 'rec:r2', kind: 'grounds' },
    { from: 'rec:r2', to: 'rec:r5', kind: 'tests' },
  ],
  steering: [],
  links: [],
  extras: 'none',
  checks: 'none',
  machine: '',
};

it('report-node is focusable and Enter selects it', async () => {
  const picked: string[] = [];
  const root = mount(ReportNode, () => ({
    id: 'rec:r2',
    data: { kind: 'hypothesis', label: 'Sleep helps', status: 'kept', selected: false },
    onSelect: (id: string) => picked.push(id),
  }));
  const button = all(root).find((n) => n.props.role === 'button')!;
  expect(button.props.tabindex).toBe(0);
  expect(button.props['aria-pressed']).toBe(false);
  (button.props.onKeydown as (e: unknown) => void)({ key: 'Enter', preventDefault() {} });
  expect(picked).toEqual(['rec:r2']);
});

it('report-node truncates long labels but keeps the full aria label', () => {
  const long = 'x'.repeat(2000);
  const root = mount(ReportNode, () => ({
    id: 'rec:r2',
    data: { kind: 'hypothesis', label: long, status: 'rejected', selected: true },
  }));
  const button = all(root).find((n) => n.props.role === 'button')!;
  expect(button.props['aria-label']).toBe(`Hypothesis: ${long}`);
  expect(button.props['aria-pressed']).toBe(true);
  const label = all(root).find((n) => n.props.class === 'report-node-label')!;
  expect(textOf(label).length).toBeLessThan(200);
});

function pane(start: string | null) {
  const selected = ref<string | null>(start);
  const emitted: (string | null)[] = [];
  const root = mount(ReportPane, () => ({
    report,
    selected: selected.value,
    onSelect: (id: string | null) => {
      emitted.push(id);
      selected.value = id;
    },
  }));
  return { root, selected, emitted };
}
const folds = (root: ReturnType<typeof node>) =>
  all(root).filter((n) => n.props.class === 'fold-toggle');
const byText = (root: ReturnType<typeof node>, text: string) =>
  all(root).find((n) => n.tag === 'button' && textOf(n).includes(text))!;

it('pane fold headings toggle aria-expanded', async () => {
  const { root } = pane('rec:r5');
  await nextTick();
  const fold = folds(root).find((n) => textOf(n).includes('Checks · evidence'))!;
  expect(fold.props['aria-expanded']).toBe(false);
  press(fold);
  await nextTick();
  expect(
    folds(root).find((n) => textOf(n).includes('Checks · evidence'))!.props['aria-expanded'],
  ).toBe(true);
});

it('a detail row opens its detail and a link row emits select with its node id', async () => {
  const { root, emitted } = pane('rec:r5');
  await nextTick();
  for (const fold of folds(root)) press(fold);
  await nextTick();
  expect(textOf(root)).not.toContain('Does the run back it?');
  press(byText(root, 'Evidence supports it'));
  await nextTick();
  expect(textOf(root)).toContain('Does the run back it?');
  press(byText(root, 'doi:10.1/a'));
  await nextTick();
  expect(emitted).toEqual(['rec:r1']);
});

it('the checks summary glyphs are hidden from screen readers and read as words', async () => {
  const { root } = pane('rec:r5');
  await nextTick();
  const fold = folds(root).find((n) => textOf(n).includes('Checks · evidence'))!;
  const glyphs = all(fold).find((n) => textOf(n) === '✓ 1 · ◐ 0 · ○ 0' && n.tag === 'span')!;
  expect(glyphs.props['aria-hidden']).toBe('true');
  const words = all(fold).find((n) => n.props.class === 'sr-only')!;
  expect(textOf(words)).toBe('1 supported, 0 unclear, 0 unsupported');
});

it("a fact's source is a link that selects its node", async () => {
  const selected = ref<string | null>('concept:insulin');
  const emitted: (string | null)[] = [];
  const withConcept: StudyReport = {
    ...report,
    extras: 'ready',
    nodes: [
      ...report.nodes,
      reportNode({
        id: 'concept:insulin',
        kind: 'concept',
        label: 'Insulin',
        status: 'protein',
        facts: [
          {
            subject: 'insulin',
            relation: 'mentioned_in',
            object: 'rec:r5',
            quote: 'Insulin rose after sleep.',
            source_node: 'rec:r5',
            p: 0.9,
          },
        ],
      }),
    ],
  };
  const root = mount(ReportPane, () => ({
    report: withConcept,
    selected: selected.value,
    onSelect: (id: string | null) => emitted.push(id),
  }));
  await nextTick();
  for (const fold of folds(root)) press(fold);
  await nextTick();
  press(byText(root, 'Insulin is mentioned in Sleep helps memory'));
  await nextTick();
  expect(textOf(root)).toContain('Insulin rose after sleep.');
  const source = all(root).find((n) => n.tag === 'button' && n.props.class === 'detail-link')!;
  // The link sets its text whole (v-text), so no template space reaches it.
  expect(source.props.textContent).toBe('Sleep helps memory');
  press(source);
  expect(emitted).toEqual(['rec:r5']);
});

it('tabs list the report, and a focused node narrows them to what touches it', async () => {
  const { root } = pane(null);
  await nextTick();
  expect(textOf(byText(root, 'Findings'))).toContain('Findings2');
  press(byText(root, 'Sources'));
  await nextTick();
  expect(textOf(root)).toContain('doi:10.1/a');
  const focused = pane('rec:r5');
  await nextTick();
  expect(textOf(byText(focused.root, 'Findings'))).toContain('Findings2');
});

it('a row in a tab selects its node', async () => {
  const { root, emitted } = pane(null);
  await nextTick();
  press(byText(root, 'Findings'));
  await nextTick();
  press(byText(root, 'Sleep helps memory'));
  expect(emitted).toEqual(['rec:r5']);
});

it('expands the recorded hand-in and collapses it again', async () => {
  const handIn = {
    ...report,
    hand_in: {
      text: '**Evidence**\n\n' + 'detail '.repeat(50),
      agent_name: 'Agent',
      run_id: 'r',
      at: '2026-11-10T10:00:00Z',
    },
  };
  const root = mount(ReportPane, () => ({ report: handIn, selected: null }));
  const toggle = byText(root, 'Show full hand-in');
  expect(toggle.props['aria-expanded']).toBe(false);
  press(toggle);
  await nextTick();
  expect(byText(root, 'Show less').props['aria-expanded']).toBe(true);
  expect(textOf(root)).toContain('Evidence');
  press(byText(root, 'Show less'));
  await nextTick();
  expect(byText(root, 'Show full hand-in').props['aria-expanded']).toBe(false);
});

it('opens the matching review tab when a graph node is selected', async () => {
  const selected = ref<string | null>('rec:r1');
  const graphSelection = ref(1);
  const root = mount(ReportPane, () => ({ report, selected: selected.value, graphSelection: graphSelection.value }));
  await nextTick();
  expect(byText(root, 'Sources').props['aria-selected']).toBe(true);
  selected.value = 'rec:r5';
  graphSelection.value++;
  await nextTick();
  expect(byText(root, 'Findings').props['aria-selected']).toBe(true);
  expect(textOf(root)).toContain('Sleep helps memory');
  press(byText(root, 'Sleep helps memory'));
  await nextTick();
  expect(byText(root, 'Summary').props['aria-selected']).toBe(true);
  selected.value = 'question';
  graphSelection.value++;
  await nextTick();
  expect(byText(root, 'Summary').props['aria-selected']).toBe(true);
});

it('shows who found a source without a duplicate expandable agent row', async () => {
  const r: StudyReport = { ...report, nodes: report.nodes.map((n) => n.id === 'rec:r1' ? { ...n, agent: 'Researcher', run_id: 'run-123', column: 'grounding' } : n) };
  const root = mount(ReportPane, () => ({ report: r, selected: 'rec:r1' }));
  press(folds(root).find((n) => textOf(n).includes('Who found it'))!);
  await nextTick();
  expect(textOf(root)).toContain('Studyt');
  expect(textOf(root)).toContain('StageGrounding');
  expect(textOf(root)).not.toContain('run-123');
  expect(all(root).some((n) => n.tag === 'button' && textOf(n).trim() === 'Researcher')).toBe(false);
});
