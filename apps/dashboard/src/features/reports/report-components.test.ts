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

it('Back returns to the previous node, and Back to the overview clears the selection', async () => {
  const { root, emitted } = pane('rec:r5');
  await nextTick();
  for (const fold of folds(root)) press(fold);
  await nextTick();
  press(byText(root, 'doi:10.1/a'));
  await nextTick();
  press(byText(root, '← Back to Sleep helps memory'));
  await nextTick();
  expect(emitted).toEqual(['rec:r1', 'rec:r5']);
  press(byText(root, 'Back to the overview'));
  await nextTick();
  expect(emitted.at(-1)).toBeNull();
  expect(textOf(root)).toContain('Click a node to see its section');
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

it('Back does not stick when the parent ignores the select', async () => {
  const selected = ref<string | null>('rec:r5');
  const emitted: (string | null)[] = [];
  let ignore = false;
  const root = mount(ReportPane, () => ({
    report,
    selected: selected.value,
    onSelect: (id: string | null) => {
      emitted.push(id);
      if (!ignore) selected.value = id;
    },
  }));
  await nextTick();
  for (const fold of folds(root)) press(fold);
  await nextTick();
  press(byText(root, 'doi:10.1/a'));
  await nextTick();
  ignore = true;
  press(byText(root, '← Back to Sleep helps memory'));
  await nextTick();
  await nextTick();
  ignore = false;
  // A later move forward must push, so Back still names the node just left.
  selected.value = 'rec:r2';
  await nextTick();
  expect(textOf(byText(root, '← Back to'))).toContain('doi:10.1/a');
});
