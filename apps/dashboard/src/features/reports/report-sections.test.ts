import { expect, it } from 'vitest';
import type { ReportNode, StudyReport } from '../../api/types';
import { overviewOf, sectionFor } from './report-sections';

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
