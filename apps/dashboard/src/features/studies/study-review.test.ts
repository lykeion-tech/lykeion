import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import StudyReview from './StudyReview.vue';
import type { Column, ReviewAttempt, ReviewColumn, StudyRecord } from '../../api/types';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const disposals: (() => void)[] = [];
afterEach(() => {
  disposals.splice(0).forEach((dispose) => dispose());
  vi.unstubAllGlobals();
});

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
function setup(
  reviews = columns(),
  current: Column = 'experimentation',
  legacy?: { scores: StudyRecord[]; outcomes: StudyRecord[] },
) {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  client.setQueryData([...keys.recordsAll('w', 'c'), 'review'], reviews);
  client.setQueryData(keys.card('w', 'c'), { id: 'c', board_column: current });
  if (legacy) {
    client.setQueryData([...keys.recordsAll('w', 'c'), 'kind', 'score'], legacy.scores);
    client.setQueryData([...keys.recordsAll('w', 'c'), 'kind', 'outcome'], legacy.outcomes);
  }
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
  expect(tile.props['aria-pressed']).toBe(true);
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
  expect(labelled(root, 'Show Validation review').props['aria-pressed']).toBe(true);
  expect(textOf(root)).toContain('Validation · try 2');
});

it('preserves a selected historical stage when the card or review updates', async () => {
  const reviews = columns();
  reviews[1]!.attempts[0]!.at = '2026-11-03T11:00:00Z';
  const { root, client } = setup(reviews);
  await nextTick();
  const groundingTile = all(root).find((n) => n.props['aria-label'] === 'Show Grounding review')!;
  (groundingTile.props.onClick as () => void)();
  await nextTick();
  client.setQueryData(keys.card('w', 'c'), { id: 'c', board_column: 'validation' });
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(groundingTile.props['aria-pressed']).toBe(true);
  client.setQueryData(
    [...keys.recordsAll('w', 'c'), 'review'],
    [
      ...reviews.slice(0, 3),
      {
        column: 'validation',
        attempts: [{ ...grounding(), column: 'validation', at: '2026-11-03T12:00:00Z' }],
      },
    ],
  );
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  expect(groundingTile.props['aria-pressed']).toBe(true);
});

it('bounds long fingerprint labels and their weakest marker within the chart', async () => {
  const a = grounding();
  a.items.forEach((item) => {
    item.label = 'WWWWWWWWWWWWWWWWWWWWWWWWextra';
  });
  const { root } = setup(columns(a));
  await nextTick();
  const labels = all(root).filter((n) => n.tag === 'foreignObject');
  expect(labels).toHaveLength(2);
  for (const label of labels) {
    expect(Number(label.props.x)).toBeGreaterThanOrEqual(0);
    expect(Number(label.props.x) + Number(label.props.width)).toBeLessThanOrEqual(430);
    expect(Number(label.props.y)).toBeGreaterThanOrEqual(0);
    expect(Number(label.props.y) + Number(label.props.height)).toBeLessThanOrEqual(330);
    expect(textOf(label)).toContain('WWWWWWWWWWWWWWWWWWWWWWWW');
    expect(textOf(label)).not.toContain('extra');
  }
  expect(textOf(labels[1]!)).toContain('· weakest');
});

it('keeps eight long item labels, weakest markers and indexes from overlapping', async () => {
  const a = grounding();
  a.items = Array.from({ length: 8 }, (_, i) => ({
    ...a.items[0]!,
    key: `p${i + 1}`,
    label: 'WWWWWWWWWWWWWWWWWWWWWWWWextra',
  }));
  const { root } = setup(columns(a));
  await nextTick();
  const labels = all(root).filter((n) => n.tag === 'foreignObject');
  expect(labels).toHaveLength(8);
  for (const [i, label] of labels.entries()) {
    const x = Number(label.props.x),
      y = Number(label.props.y);
    const width = Number(label.props.width),
      height = Number(label.props.height);
    expect(x).toBeGreaterThanOrEqual(0);
    expect(x + width).toBeLessThanOrEqual(430);
    expect(y).toBeGreaterThanOrEqual(0);
    expect(y + height).toBeLessThanOrEqual(330);
    expect(textOf(label)).toContain('WWWWWWWWWWWWWWWWWWWWWWWW');
    for (const other of labels.slice(i + 1)) {
      expect(
        x + width <= Number(other.props.x) ||
          Number(other.props.x) + Number(other.props.width) <= x ||
          y + height <= Number(other.props.y) ||
          Number(other.props.y) + Number(other.props.height) <= y,
      ).toBe(true);
    }
  }
  expect(labels.some((label) => textOf(label).includes('· weakest'))).toBe(true);
});

it('shows an empty review and the current stage before any attempts', async () => {
  const { root } = setup(
    columns().map((column) => ({ ...column, attempts: [] })),
    'experimentation',
    { scores: [], outcomes: [] },
  );
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

const labelled = (root: ReturnType<typeof node>, label: string) => {
  const found = all(root).find((n) => n.props['aria-label'] === label);
  expect(found, `Expected a node labelled ${label}`).toBeDefined();
  return found!;
};
const click = async (n: ReturnType<typeof node>) => {
  (n.props.onClick as () => void)();
  await nextTick();
};

it('folds the checks to counts and filters them by status', async () => {
  const a = grounding();
  a.items[1]!.carried = true;
  const { root } = setup(columns(a));
  await nextTick();
  const toggle = labelled(root, 'Checks · evidence');
  expect(textOf(toggle)).toContain('✓ 2');
  expect(textOf(toggle)).toContain('◐ 1');
  expect(textOf(toggle)).toContain('○ 1');
  expect(all(root).filter((n) => n.props.class === 'check-row')).toHaveLength(0);
  await click(toggle);
  expect(all(root).filter((n) => n.props.class === 'check-row')).toHaveLength(4);
  expect(textOf(root)).toContain('weakest · Jev 20% sure');
  expect(textOf(root)).toContain('carried');
  expect(textOf(labelled(root, 'Unsupported checks'))).toContain('1');
  await click(labelled(root, 'Unsupported checks'));
  const rows = all(root).filter((n) => n.props.class === 'check-row');
  expect(rows).toHaveLength(1);
  expect(textOf(rows[0]!)).toContain('Criterion 1');
  expect(textOf(rows[0]!)).toContain('20%');
  await click(labelled(root, 'Supported checks'));
  await click(all(root).find((n) => n.props.class === 'check-row')!);
  expect(textOf(selected(root))).toContain('The first paper found a benefit.');
});

it('opens the server checks when one failed', async () => {
  const a = grounding();
  a.items[0]!.checks.push({
    id: 'exists',
    by: 'server',
    status: 'fail',
    detail: 'DOI was not found',
  });
  a.items[1]!.checks.push({ id: 'exists', by: 'server', status: 'pass', detail: 'DOI resolved' });
  a.recall = { query: 'seedling growth', hits: 14, screened: 6 };
  const { root } = setup(columns(a));
  await nextTick();
  const server = labelled(root, 'Checked by the server');
  expect(textOf(server)).toContain('1 passed · 1 failed');
  expect(server.props['aria-expanded']).toBe(true);
  expect(textOf(root)).toContain('DOI was not found');
  expect(all(root).filter((n) => n.props.class === 'server-family')).toHaveLength(1);
  expect(textOf(root)).toContain('Searched “seedling growth” · 14 found · 6 screened by Jev');
});

it('puts Experimentation server checks open above the fingerprint and marks warning rows', async () => {
  const a = grounding();
  a.column = 'experimentation';
  a.items[0]!.checks = [
    { id: 'declared', by: 'server', status: 'pass', detail: 'Output was declared' },
    { id: 'described', by: 'jev', status: 'unclear', p: 0.5, warning: true },
  ];
  const { root } = setup([{ column: 'experimentation', attempts: [a] }]);
  await nextTick();
  const nodes = all(root);
  expect(labelled(root, 'Checked by the server').props['aria-expanded']).toBe(true);
  expect(nodes.indexOf(labelled(root, 'Checked by the server'))).toBeLessThan(
    nodes.indexOf(nodes.find((n) => n.props.class === 'fingerprint-panel')!),
  );
  expect(textOf(root)).toContain('Output was declared');
  await click(labelled(root, 'Checks · evidence'));
  expect(textOf(root)).toContain('warning only');
});

function validation(extra: Partial<ReviewColumn> = {}): ReviewColumn[] {
  return [
    {
      column: 'validation',
      attempts: [{ ...grounding(), column: 'validation' }],
      mode: 'runs',
      ...extra,
    },
  ];
}

it('reports the recorded Validation statuses without implying the server stopped a passing try', async () => {
  const a: ReviewAttempt = {
    ...grounding(),
    column: 'validation',
    decision: 'pass',
    items: [
      { ...grounding().items[0]!, status: 'supported', checks: [] },
      { ...grounding().items[1]!, status: 'caveated', checks: [] },
    ],
  };
  const { root } = setup(validation({ attempts: [a] }));
  await nextTick();
  const tile = labelled(root, 'Show Validation review');
  expect(textOf(tile)).toContain('1 supported · 1 caveated');
  expect(textOf(tile)).not.toContain('items pass');
  expect(textOf(root)).not.toContain("server's checks stopped it");
  expect(textOf(root)).toContain('Hypothesis statuses were determined from recorded runs.');
});

it('shows Validation statuses with the objections upheld against them', async () => {
  const { root } = setup(
    validation({
      statuses: [
        {
          hypothesis: 'Seedlings grow taller',
          status: 'caveated',
          reasons: ['Small sample', 'Second reason'],
          cites: ['r9'],
        },
      ],
      objections: [
        { target_key: 'r9', type: 'wrong_test', problem: 'p', upheld: true },
        { target_key: 'r9', type: 'missing_control', problem: 'dropped objection', upheld: false },
        { target_key: 'other', type: 'wrong_test', problem: 'unrelated objection', upheld: true },
      ],
      route_back: { to: 'experimentation' },
    }),
  );
  await nextTick();
  const row = all(root).find((n) => n.props.class === 'validation-status');
  expect(row).toBeDefined();
  expect(textOf(row!)).toContain('Seedlings grow taller');
  expect(textOf(row!)).toContain('caveated');
  expect(textOf(row!)).toContain('Small sample');
  expect(textOf(row!)).toContain('wrong test: p');
  expect(textOf(row!)).not.toContain('Second reason');
  expect(textOf(row!)).not.toContain('dropped objection');
  expect(textOf(row!)).not.toContain('unrelated objection');
  expect(textOf(root)).toContain('Sent back to Experimentation once');
});

it('links final Validation objections by their record keys without guessing uncited targets', async () => {
  const { root } = setup(
    validation({
      statuses: [
        {
          hypothesis: 'First hypothesis',
          status: 'caveated',
          reasons: ['an upheld wrong test objection'],
          cites: ['value-1', 'objection-9'],
        },
        {
          hypothesis: 'Second hypothesis',
          status: 'invalid',
          reasons: ['No valid test'],
          cites: ['value-2'],
        },
      ],
      objections: [
        {
          key: 'objection-9',
          target_key: 'hypothesis-record',
          type: 'wrong_test',
          problem: 'Incorrect comparison',
          upheld: true,
        },
        {
          key: 'objection-10',
          target_key: 'value-2',
          type: 'wrong_test',
          problem: 'Uncited and did not lower the status',
          upheld: true,
        },
        {
          key: 'objection-11',
          target_key: 'hypothesis-record',
          type: 'wrong_test',
          problem: 'Dropped objection',
          upheld: false,
        },
      ],
    }),
  );
  await nextTick();
  const rows = all(root).filter((n) => n.props.class === 'validation-status');
  expect(textOf(rows[0]!)).toContain('wrong test: Incorrect comparison');
  expect(textOf(rows[0]!)).not.toContain('Dropped objection');
  expect(textOf(rows[1]!)).not.toContain('Uncited and did not lower the status');
});

it('shows uncited upheld Validation objections separately from hypotheses', async () => {
  const { root } = setup(
    validation({
      statuses: [
        {
          hypothesis: 'No reliable effect',
          status: 'invalid',
          reasons: ['No valid test'],
          cites: ['value-2', 'objection-9'],
        },
      ],
      objections: [
        {
          key: 'objection-9',
          target_key: 'hypothesis-record',
          type: 'wrong_test',
          problem: 'Already linked objection',
          upheld: true,
        },
        {
          key: 'objection-10',
          target_key: 'value-2',
          type: 'missing_control',
          problem: 'A control is still missing',
          upheld: true,
        },
        {
          key: 'objection-11',
          target_key: 'value-2',
          type: 'wrong_test',
          problem: 'Rejected concern',
          upheld: false,
        },
      ],
    }),
  );
  await nextTick();
  const other = labelled(root, 'Other upheld objections');
  expect(textOf(other)).toContain('missing control: A control is still missing');
  expect(textOf(other)).not.toContain('Already linked objection');
  expect(textOf(other)).not.toContain('Rejected concern');
  const status = all(root).find((n) => n.props.class === 'validation-status')!;
  expect(textOf(status)).toContain('Already linked objection');
  expect(textOf(status)).not.toContain('A control is still missing');
});

it('preserves distinct missed item groups and selection when status filters change', async () => {
  const a = grounding();
  a.items = [];
  a.weakest = undefined;
  a.missed = [
    {
      key: '',
      kind: 'paper',
      label: 'Missed first',
      quote: 'First missing source',
      cites: [],
      status: 'pass',
      checks: [{ id: 'missed', by: 'jev', status: 'pass', p: 0.9 }],
    },
    {
      key: '',
      kind: 'paper',
      label: 'Missed second',
      quote: 'Second missing source',
      cites: [],
      status: 'fail',
      checks: [{ id: 'missed', by: 'jev', status: 'fail', p: 0.1 }],
    },
    {
      key: '',
      kind: 'paper',
      label: 'Missed third',
      quote: 'Third missing source',
      cites: [],
      status: 'unclear',
      checks: [{ id: 'missed', by: 'jev', status: 'unclear', p: 0.5 }],
    },
  ];
  const { root } = setup([{ column: 'grounding', attempts: [a] }]);
  await nextTick();
  await click(labelled(root, 'Checks · evidence'));
  const groups = () => all(root).filter((n) => n.props.class === 'check-group');
  const second = groups()[1]!;
  expect(groups()).toHaveLength(3);
  await click(labelled(root, 'Unsupported checks'));
  expect(groups()).toHaveLength(1);
  expect(groups()[0] === second).toBe(true);
  await click(all(groups()[0]!).find((n) => n.props.class === 'check-row')!);
  expect(textOf(selected(root))).toContain('Second missing source');
  await click(labelled(root, 'All checks'));
  expect(groups().map((group) => textOf(group).includes('Missed'))).toEqual([true, true, true]);
  expect(groups()[1] === second).toBe(true);
  await click(labelled(root, 'Unclear checks'));
  expect(groups()).toHaveLength(1);
  expect(textOf(groups()[0]!)).toContain('Missed third');
  await click(all(groups()[0]!).find((n) => n.props.class === 'check-row')!);
  expect(textOf(selected(root))).toContain('Third missing source');
});

it('says why the objector did not run and marks a same-family objector', async () => {
  const skipped = setup(
    validation({
      objector: { ran: false, reason: 'No independent backend available', same_family: false },
    }),
  );
  await nextTick();
  expect(textOf(skipped.root)).toContain('No independent backend available');
  const same = setup(validation({ same_family: true }));
  await nextTick();
  expect(textOf(same.root)).toContain('same-family objector');
});

it('uses generic item checks for Validation without runs', async () => {
  const { root } = setup(
    validation({
      mode: 'sources',
      statuses: [{ hypothesis: 'Run-only status', status: 'supported' }],
    }),
  );
  await nextTick();
  await click(labelled(root, 'Checks · evidence'));
  expect(all(root).filter((n) => n.props.class === 'check-row')).toHaveLength(4);
  expect(textOf(root)).not.toContain('Run-only status');
});

it('shows a finished legacy study in the old form', async () => {
  const record = (kind: string, body: Record<string, unknown>): StudyRecord => ({
    id: kind,
    card_id: 'c',
    board_column: 'grounding',
    round: 1,
    kind,
    role: 'server',
    key: kind,
    body,
    blob_key: '',
    agent_run_id: null,
    superseded_at: null,
    created_at: '2026-11-03T10:00:00Z',
  });
  const { root } = setup([], 'done', {
    scores: [
      record('score', { criterion: 'Relevance', level: 4 }),
      record('score', { criterion: 'Coverage', level: 2 }),
    ],
    outcomes: [record('outcome', { verdict: 'middle' })],
  });
  await nextTick();
  expect(textOf(root)).toContain('Reviewed under the old scoring');
  expect(textOf(root)).toContain('Grounding · middle');
  expect(textOf(root)).toContain('Relevance — Mostly');
  expect(textOf(root)).toContain('Coverage — Barely');
  expect(spokeButtons(root)).toHaveLength(0);
  expect(all(root).some((n) => n.props['aria-label'] === 'Review stages')).toBe(false);
});

it('fetches legacy records only after an empty review, and stops using them when an attempt arrives', async () => {
  const fetch = vi.fn(async (_path: string) => Response.json([]));
  vi.stubGlobal('fetch', fetch);
  const { root, client } = setup();
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(fetch).not.toHaveBeenCalled();
  client.setQueryData([...keys.recordsAll('w', 'c'), 'review'], []);
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(fetch.mock.calls.map((args) => String(args[0])).sort()).toEqual([
    '/api/cards/c/records?kind=outcome',
    '/api/cards/c/records?kind=score',
  ]);
  client.setQueryData([...keys.recordsAll('w', 'c'), 'review'], columns());
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(spokeButtons(root)).toHaveLength(4);
  expect(textOf(root)).not.toContain('Reviewed under the old scoring');
});

it.each(['score', 'outcome'])(
  'waits for legacy %s records and retries errors before displaying evidence',
  async (kind) => {
    const score = {
      kind: 'score',
      board_column: 'grounding',
      round: 1,
      body: { criterion: 'Relevance', level: 4 },
    };
    const outcome = {
      kind: 'outcome',
      board_column: 'grounding',
      round: 1,
      body: { verdict: 'middle' },
    };
    let fail!: () => void;
    const pending = new Promise<Response>((resolve) => {
      fail = () => resolve(Response.json({ error: 'History read failed' }, { status: 503 }));
    });
    let first = true;
    vi.stubGlobal('fetch', (path: string) => {
      if (path.endsWith('kind=' + kind) && first) {
        first = false;
        return pending;
      }
      return Promise.resolve(Response.json([path.endsWith('kind=score') ? score : outcome]));
    });
    const { root } = setup([], 'done');
    await nextTick();
    expect(textOf(root)).toContain('Loading review history');
    expect(textOf(root)).not.toContain('Nothing to review yet');
    expect(textOf(root)).not.toContain('Reviewed under the old scoring');
    fail();
    await vi.waitFor(() => expect(textOf(root)).toContain('History read failed'));
    expect(textOf(root)).not.toContain('Nothing to review yet');
    expect(textOf(root)).not.toContain('Reviewed under the old scoring');
    const retry = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Retry')!;
    await click(retry);
    await vi.waitFor(() => expect(textOf(root)).toContain('Grounding · middle'));
    expect(textOf(root)).toContain('Relevance — Mostly');
  },
);
