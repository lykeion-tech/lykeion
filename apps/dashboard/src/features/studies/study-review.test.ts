import { afterEach, expect, it } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyReview from './StudyReview.vue';
import type { Column, ReviewAttempt, ReviewColumn } from '../../api/types';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const disposals: (() => void)[] = [];
afterEach(() => disposals.splice(0).forEach((dispose) => dispose()));

function grounding(): ReviewAttempt {
  return {
    column: 'grounding',
    attempt: 1,
    round: 1,
    at: '2026-11-03T10:00:00Z',
    decision: 'redo',
    weakest: { key: 'p2', p: 0.2 },
    items: [
      {
        key: 'p1',
        kind: 'paper',
        label: 'First paper',
        status: 'pass',
        quote: 'The first paper found a benefit.',
        cites: ['doi:first'],
        checks: [
          {
            id: 'supported',
            by: 'jev',
            status: 'pass',
            p: 0.8,
            question: 'Does the first source support it?',
          },
          {
            id: 'criterion:1',
            by: 'jev',
            status: 'pass',
            p: 0.9,
            question: 'Does the first paper qualify?',
          },
        ],
      },
      {
        key: 'p2',
        kind: 'paper',
        label: 'Second paper',
        status: 'fail',
        quote: 'The second paper found no benefit.',
        cites: ['doi:second', 'r2'],
        checks: [
          {
            id: 'supported',
            by: 'jev',
            status: 'unclear',
            p: 0.5,
            question: 'Does the second source support it?',
          },
          {
            id: 'criterion:1',
            by: 'jev',
            status: 'fail',
            p: 0.2,
            question: 'Does the second paper qualify?',
          },
        ],
      },
    ],
  };
}
function columns(a = grounding()): ReviewColumn[] {
  return [
    { column: 'grounding', attempts: [a] },
    {
      column: 'hypotheses',
      attempts: [{ ...grounding(), column: 'hypotheses', at: '2026-11-03T09:00:00Z' }],
    },
    { column: 'experimentation', attempts: [] },
    { column: 'validation', attempts: [] },
  ];
}
function setup(reviews = columns(), current: Column = 'experimentation') {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData([...keys.recordsAll('w', 'c'), 'review'], reviews);
  client.setQueryData(keys.card('w', 'c'), { id: 'c', board_column: current });
  const root = node('root');
  const app = createTreeApp({ render: () => h(StudyReview, { cardId: 'c' }) });
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(root);
  disposals.push(() => {
    app.unmount();
    client.clear();
  });
  return { root, client };
}
const spokeButtons = (root: ReturnType<typeof node>) =>
  all(root).filter((n) => n.props.role === 'button' && String(n.props['aria-label']).endsWith('%'));
const selected = (root: ReturnType<typeof node>) =>
  all(root).find((n) => n.props['aria-label'] === 'Selected check')!;

it("draws one spoke per Jev check and selects the weakest item's lowest check", async () => {
  const { root } = setup();
  await nextTick();
  expect(spokeButtons(root)).toHaveLength(4);
  expect(textOf(selected(root))).toContain('The second paper found no benefit.');
  expect(textOf(selected(root))).toContain('Does the second paper qualify?');
  expect(textOf(selected(root))).toContain('cites doi:second, r2 · Grounding · try 1');
  expect(all(root).find((n) => n.props['aria-label'] === 'Support index')?.text).toBe('60');
});

it('selects a clicked spoke', async () => {
  const { root } = setup();
  await nextTick();
  (spokeButtons(root)[0]!.props.onClick as () => void)();
  await nextTick();
  expect(textOf(selected(root))).toContain('The first paper found a benefit.');
});

it('says when the server stopped every item', async () => {
  const a = grounding();
  a.items = a.items.map((item) => ({
    ...item,
    checks: [{ id: 'exists', by: 'server', status: 'fail' }],
  }));
  const { root } = setup(columns(a));
  await nextTick();
  expect(textOf(root)).toContain("No Jev checks this try: the server's checks stopped it.");
  expect(spokeButtons(root)).toHaveLength(0);
  const tile = all(root).find(
    (n) => n.tag === 'button' && String(n.props['aria-label']) === 'Show Grounding review',
  )!;
  expect(textOf(tile)).toContain('—');
});

it('shows a tile per column', async () => {
  const { root } = setup();
  await nextTick();
  const tiles = all(root).filter(
    (n) => n.tag === 'button' && String(n.props['aria-label']).startsWith('Show '),
  );
  expect(tiles.map(textOf)).toEqual([
    'Grounding601 of 2 items pass · try 1',
    'Hypotheses601 of 2 items pass · try 1',
    'Experimentation—being worked on',
    'Validation—not reached',
  ]);
});

it('switches the shown column from a tile', async () => {
  const { root } = setup();
  await nextTick();
  const tile = all(root).find((n) => n.props['aria-label'] === 'Show Hypotheses review')!;
  (tile.props.onClick as () => void)();
  await nextTick();
  expect(textOf(all(root).find((n) => n.props['aria-label'] === 'Fingerprint caption')!)).toContain(
    'Hypotheses · try 1 · 4 checks',
  );
});

it('shows the newest attempt by time, including after a review arrives', async () => {
  const { root, client } = setup();
  await nextTick();
  const latest = {
    ...grounding(),
    column: 'validation' as const,
    attempt: 2,
    at: '2026-11-03T11:00:00Z',
  };
  client.setQueryData(
    [...keys.recordsAll('w', 'c'), 'review'],
    [
      ...columns().slice(0, 3),
      {
        column: 'validation',
        attempts: [latest, { ...latest, attempt: 1, at: '2026-11-03T08:00:00Z' }],
      },
    ],
  );
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(textOf(all(root).find((n) => n.props['aria-label'] === 'Fingerprint caption')!)).toContain(
    'Validation · try 2',
  );
});

it('shows an empty review and the current stage before any attempts', async () => {
  const { root } = setup(columns().map((column) => ({ ...column, attempts: [] })));
  await nextTick();
  expect(textOf(root)).toContain(
    'Jev reviews each column when its agents finish. Nothing to review yet.',
  );
  expect(
    all(root).find((n) => n.props['aria-label'] === 'Show Experimentation review')?.props[
      'aria-pressed'
    ],
  ).toBe(true);
});

it('draws Experimentation warnings dashed and keeps the stored status', async () => {
  const a = grounding();
  a.column = 'experimentation';
  a.items = [
    {
      ...a.items[0]!,
      checks: [
        {
          id: 'described',
          by: 'jev',
          p: 0.9,
          status: 'unclear',
          warning: true,
          question: 'Is the result described correctly?',
        },
      ],
    },
  ];
  const { root } = setup([{ column: 'experimentation', attempts: [a] }]);
  await nextTick();
  expect(textOf(root)).toContain('· warnings only');
  expect(textOf(selected(root))).toContain('Unclear');
  expect(all(root).some((n) => n.tag === 'line' && n.props['stroke-dasharray'] === '4 4')).toBe(
    true,
  );
});
