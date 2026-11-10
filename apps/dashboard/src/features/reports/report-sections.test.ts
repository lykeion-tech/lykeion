import { expect, it } from 'vitest';
import type { ReportNode, StudyReport } from '../../api/types';
import { handInSegments, noticesOf, overviewOf, sectionFor } from './report-sections';

const node = (n: Partial<ReportNode> & Pick<ReportNode, 'id' | 'kind'>): ReportNode => ({
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

function report(): StudyReport {
  return {
    card_id: 'c',
    number: 7,
    title: 'Sleep and memory',
    question: 'Does sleep help memory?',
    stages: ['grounding', 'hypotheses', 'experimentation', 'validation'],
    finished_at: '2026-11-10T10:00:00Z',
    hand_in: null,
    nodes: [
      node({ id: 'question', kind: 'question', label: 'Does sleep help memory?' }),
      node({
        id: 'rec:r1',
        kind: 'paper',
        label: 'doi:10.1/a',
        status: 'pass',
        column: 'grounding',
      }),
      node({ id: 'rec:r2', kind: 'hypothesis', label: 'Sleep helps', status: 'kept' }),
      node({
        id: 'rec:r3',
        kind: 'hypothesis',
        label: 'Naps hurt',
        status: 'rejected',
        checks: [
          { id: 'falsifiable', by: 'jev', status: 'fail', p: 0.1, question: 'Is it falsifiable?' },
        ],
      }),
      node({ id: 'run:x', kind: 'experiment', label: 'Runner', run_id: 'x', agent: 'Runner' }),
      node({ id: 'out:plot.png', kind: 'output', label: 'plot.png', run_id: 'x' }),
      node({
        id: 'rec:r5',
        kind: 'finding',
        label: 'Sleep helps',
        status: 'caveated',
        column: 'validation',
        checks: [
          { id: 'supported', by: 'jev', status: 'pass', p: 0.8, question: 'Is it supported?' },
        ],
      }),
      node({
        id: 'rec:r6',
        kind: 'finding',
        label: 'Sleep helps more in the old',
        status: 'inconclusive',
        reasons: ['Only 12 participants were over 60.', 'The rerun printed no effect size.'],
        column: 'validation',
      }),
      node({ id: 'rec:r7', kind: 'finding', label: 'f3', status: 'supported' }),
      node({ id: 'rec:r8', kind: 'finding', label: 'f4', status: 'refuted' }),
      node({
        id: 'rec:r9',
        kind: 'finding',
        label: 'f5',
        status: 'invalid',
        reasons: ['No control.'],
      }),
      node({ id: 'obj:o1', kind: 'objection', label: 'overreach', status: 'upheld' }),
      node({ id: 'doubt:grounding:r1', kind: 'doubt', label: 'doi:10.1/a', status: 'unclear' }),
    ],
    edges: [
      { from: 'question', to: 'rec:r1', kind: 'uses' },
      { from: 'question', to: 'rec:r2', kind: 'tests' },
      { from: 'question', to: 'rec:r3', kind: 'tests' },
      { from: 'rec:r1', to: 'rec:r2', kind: 'grounds' },
      { from: 'rec:r2', to: 'run:x', kind: 'tested_by' },
      { from: 'run:x', to: 'out:plot.png', kind: 'produced' },
      { from: 'rec:r2', to: 'rec:r5', kind: 'tests' },
      { from: 'out:plot.png', to: 'rec:r5', kind: 'supports' },
      { from: 'obj:o1', to: 'rec:r5', kind: 'challenges' },
      { from: 'doubt:grounding:r1', to: 'rec:r5', kind: 'challenges' },
      // An edge to a node the report does not hold is never linked.
      { from: 'rec:gone', to: 'rec:r5', kind: 'supports' },
      { from: 'rec:gone', to: 'rec:r2', kind: 'grounds' },
      { from: 'obj:gone', to: 'rec:r5', kind: 'challenges' },
    ],
    steering: [
      { kind: 'comment', author: 'u', text: 'Look at the old.', at: '2026-11-09T10:00:00Z' },
    ],
    links: [],
    extras: 'none',
    checks: 'none',
    machine: '',
  };
}

it('a caveated finding reads "◐ Supported with caveats" with screen-reader text "Supported with caveats"', () => {
  const s = sectionFor(report(), 'rec:r5');
  expect(s.status).toEqual({
    icon: '◐',
    word: 'Supported with caveats',
    srText: 'Supported with caveats',
  });
});

it(`an inconclusive finding's first fold is "Why inconclusive" and lists its recorded reasons verbatim`, () => {
  const s = sectionFor(report(), 'rec:r6');
  expect(s.folds[0]!.title).toBe('Why inconclusive');
  expect(s.folds[0]!.rows.map((r) => r.label)).toEqual([
    'Only 12 participants were over 60.',
    'The rerun printed no effect size.',
  ]);
  expect(sectionFor(report(), 'rec:r9').folds[0]!.title).toBe('Why invalid');
  expect(sectionFor(report(), 'rec:r5').folds.map((f) => f.title)).toEqual([
    'Checks · evidence',
    'Rerun · objections',
    'Sources',
    'Who decided',
  ]);
});

it('a rejected hypothesis has a "Why rejected" fold', () => {
  expect(sectionFor(report(), 'rec:r3').folds.map((f) => f.title)).toContain('Why rejected');
  expect(sectionFor(report(), 'rec:r2').folds.map((f) => f.title)).not.toContain('Why rejected');
});

it('link rows point at existing node ids only', () => {
  const r = report();
  const ids = new Set(r.nodes.map((n) => n.id));
  const links = r.nodes.flatMap((n) =>
    sectionFor(r, n.id).folds.flatMap((f) =>
      f.rows.flatMap((row) => (row.kind === 'link' ? [row] : [])),
    ),
  );
  expect(links.length).toBeGreaterThan(5);
  for (const link of links) expect(ids.has(link.nodeId)).toBe(true);
  const sources = sectionFor(r, 'rec:r5').folds.find((f) => f.title === 'Sources')!;
  expect(sources.rows.map((row) => row.kind === 'link' && row.nodeId)).toEqual([
    'rec:r1',
    'out:plot.png',
  ]);
});

it('overviewOf counts each finding status separately', () => {
  expect(overviewOf(report())).toEqual({
    supported: 1,
    caveated: 1,
    refuted: 1,
    inconclusive: 1,
    invalid: 1,
    rejected: 1,
    objections: 1,
    doubts: 1,
  });
});

it("study steering is labelled as the study's, apart from the node's own run and judge", () => {
  const who = sectionFor(report(), 'rec:r5').folds.find((f) => f.title === 'Who decided')!;
  expect(who.rows.map((r) => r.label)).toEqual([
    'Study steering · Look at the old.',
    'Judged in Validation',
  ]);
  const steering = who.rows[0]!;
  expect(steering.kind === 'detail' && steering.detail[0]!.label).toBe(
    "A researcher's comment on the study, not linked to this node",
  );
});

it('the checks summary has words for screen readers', () => {
  const checks = sectionFor(report(), 'rec:r5').folds.find((f) => f.title === 'Checks · evidence')!;
  expect(checks.summary).toBe('✓ 1 · ◐ 0 · ○ 0');
  expect(checks.srSummary).toBe('1 supported, 0 unclear, 0 unsupported');
});

it('objections read Upheld or Not upheld, never a finding status', () => {
  const r = report();
  expect(sectionFor(r, 'obj:o1').status).toEqual({ icon: '○', word: 'Upheld', srText: 'Upheld' });
  r.nodes = r.nodes.map((n) => (n.id === 'obj:o1' ? { ...n, status: 'not_upheld' } : n));
  expect(sectionFor(r, 'obj:o1').status).toEqual({
    icon: '✓',
    word: 'Not upheld',
    srText: 'Not upheld',
  });
});

it('a doubt whose item passed reads "Passed", not "Supported"', () => {
  const r = report();
  r.nodes = r.nodes.map((n) => (n.kind === 'doubt' ? { ...n, status: 'pass' } : n));
  expect(sectionFor(r, 'doubt:grounding:r1').status.word).toBe('Passed');
  r.nodes = r.nodes.map((n) => (n.kind === 'doubt' ? { ...n, status: 'fail' } : n));
  expect(sectionFor(r, 'doubt:grounding:r1').status.word).toBe('Failed');
});

// A report with concepts, Jev's figure, table and novelty checks, and links.
function judged(): StudyReport {
  const r = report();
  r.extras = 'ready';
  r.checks = 'ready';
  r.nodes.push(
    node({ id: 'out:table.csv', kind: 'output', label: 'table.csv', content_type: 'text/csv' }),
    node({
      id: 'concept:insulin',
      kind: 'concept',
      label: 'Insulin',
      status: 'protein',
      facts: [
        {
          subject: 'insulin',
          relation: 'measured_by',
          object: 'elisa',
          quote: 'Insulin was measured by ELISA.',
          source_node: 'rec:r5',
          p: 0.91,
        },
        {
          subject: 'insulin',
          relation: 'mentioned_in',
          object: 'rec:r2',
          quote: 'Sleep helps insulin.',
          source_node: 'rec:r2',
          p: 0.88,
        },
      ],
    }),
    node({ id: 'concept:elisa', kind: 'concept', label: 'ELISA', status: 'method' }),
  );
  r.edges.push({ from: 'out:table.csv', to: 'rec:r5', kind: 'supports' });
  const f = r.nodes.find((n) => n.id === 'rec:r5')!;
  f.novelty = 'contradicts';
  f.checks.push(
    {
      id: 'figure:out:plot.png',
      by: 'jev',
      status: 'pass',
      p: 0.9,
      question: 'Does this figure, as its recorded text describes it, show what the claim says?',
      detail: 'Recall by group.',
      basis: 'text',
    },
    {
      id: 'novelty:rec:r1',
      by: 'jev',
      status: 'pass',
      p: 0.85,
      question: 'Reading only the abstract…',
      detail: 'choice: contradicts\nSleep does not help memory.',
      basis: 'text',
    },
  );
  return r;
}

it('concept section lists judged facts with quotes', () => {
  const s = sectionFor(judged(), 'concept:insulin');
  expect(s.status).toEqual({ icon: '', word: 'Protein', srText: 'Protein' });
  expect(s.folds.map((f) => f.title)).toEqual(['Facts about it']);
  const rows = s.folds[0]!.rows;
  expect(rows.map((r) => r.label)).toEqual([
    'Insulin is measured by ELISA',
    'Insulin is mentioned in Sleep helps',
  ]);
  const first = rows[0]!;
  expect(first.kind === 'detail' && first.pct).toBe(91);
  expect(first.kind === 'detail' && first.detail).toEqual([
    { label: 'Quote', text: 'Insulin was measured by ELISA.' },
    { label: 'From', text: 'Sleep helps', nodeId: 'rec:r5' },
  ]);
});

it('finding novelty fold summarises contradicts', () => {
  const r = judged();
  const folds = sectionFor(r, 'rec:r5').folds;
  expect(folds.map((f) => f.title)).toEqual([
    'Checks · evidence',
    'Novelty',
    'Rerun · objections',
    'Sources',
    'Who decided',
  ]);
  const novelty = folds[1]!;
  expect(novelty.summary).toBe('Contradicts');
  expect(novelty.rows).toHaveLength(1);
  const row = novelty.rows[0]!;
  expect(row.label).toBe('doi:10.1/a · Contradicts');
  expect(row.kind === 'detail' && row.pct).toBe(85);
  expect(row.kind === 'detail' && row.detail[0]).toEqual({
    label: 'Paper',
    text: 'doi:10.1/a',
    nodeId: 'rec:r1',
  });
  // Novelty checks are not counted among the finding's evidence checks.
  expect(folds[0]!.summary).toBe('✓ 2 · ◐ 0 · ○ 0');
  r.nodes.find((n) => n.id === 'rec:r5')!.novelty = undefined;
  expect(sectionFor(r, 'rec:r5').folds[1]!.summary).toBe('Not judged');
});

it('figure check row says it is judged from text', () => {
  const r = judged();
  const checks = sectionFor(r, 'rec:r5').folds[0]!.rows;
  const figure = checks.find((c) => c.label === 'Figure · plot.png')!;
  expect(figure.kind === 'detail' && figure.detail[0]!.text).toBe(
    "Judged from the figure's recorded text, not the image.",
  );
  const image = r.nodes.find((n) => n.id === 'rec:r5')!.checks[1]!;
  image.basis = 'image';
  const shown = sectionFor(r, 'rec:r5').folds[0]!.rows.find(
    (c) => c.label === 'Figure · plot.png',
  )!;
  expect(shown.kind === 'detail' && shown.detail[0]!.text).toBe('Judged from the image.');
  // The table has no check, and says so.
  const table = checks.find((c) => c.label.startsWith('table.csv'))!;
  expect(table.kind === 'detail' && table.detail[0]!.text).toBe(
    'Not checked: no recorded text describes this output.',
  );
  // The output's own section shows Jev's check for the finding it supports.
  const supports = sectionFor(r, 'out:plot.png').folds.find((f) => f.title === 'Supports')!;
  expect(supports.rows.map((x) => x.label)).toEqual(['Sleep helps', 'Figure · plot.png']);
});

it('hand-in links never change the text', () => {
  const text = '\uFEFFRésumé: β-cells 🧪 secrete insulin; sleep helps.';
  const bytes = new TextEncoder().encode(text);
  const at = (s: string) => new TextEncoder().encode(text.slice(0, text.indexOf(s))).length;
  const len = (s: string) => new TextEncoder().encode(s).length;
  const links = [
    { start: at('insulin'), end: at('insulin') + len('insulin'), node: 'concept:insulin', p: 0.9 },
    { start: at('β-cells'), end: at('β-cells') + len('β-cells'), node: 'rec:r5', p: 0.8 },
    // Overlapping, or cutting a character in half, is never linked.
    { start: at('β-cells') + 1, end: at('🧪') + 2, node: 'rec:r2', p: 0.8 },
    { start: at('sleep'), end: bytes.length + 5, node: 'rec:r2', p: 0.8 },
  ];
  const segments = handInSegments(text, links);
  expect(segments.map((s) => s.text).join('')).toBe(text);
  expect(segments.filter((s) => s.node).map((s) => [s.text, s.node])).toEqual([
    ['β-cells', 'rec:r5'],
    ['insulin', 'concept:insulin'],
  ]);
});

it('pending extras notice names the machine', () => {
  const r = judged();
  r.extras = 'pending';
  r.machine = 'lab-mac';
  expect(noticesOf(r)).toEqual(['Concepts and links are waiting for lab-mac.']);
  r.machine = '';
  r.checks = 'unavailable';
  expect(noticesOf(r)).toEqual([
    'Concepts and links are being judged.',
    "Jev could not judge this report's figure, table and novelty checks.",
  ]);
  r.extras = 'ready';
  r.checks = 'ready';
  expect(noticesOf(r)).toEqual([]);
});
