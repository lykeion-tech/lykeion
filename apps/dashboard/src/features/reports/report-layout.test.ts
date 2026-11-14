import { expect, it } from 'vitest';
import type { ReportEdge, ReportNode } from '../../api/types';
import { edgeStyle, layoutReport } from './report-layout';

const node = (id: string, kind: ReportNode['kind']): ReportNode => ({
  id,
  kind,
  label: id,
  status: '',
  reasons: [],
  quote: '',
  checks: [],
  run_id: null,
  agent: '',
  column: '',
});

it('layoutReport puts the question left of hypotheses and hypotheses left of findings', () => {
  const nodes = [
    node('rec:f1', 'finding'),
    node('rec:h1', 'hypothesis'),
    node('question', 'question'),
    node('rec:h2', 'hypothesis'),
  ];
  const edges: ReportEdge[] = [
    { from: 'question', to: 'rec:h1', kind: 'tests' },
    { from: 'question', to: 'rec:h2', kind: 'tests' },
    { from: 'rec:h1', to: 'rec:f1', kind: 'tests' },
  ];
  const at = layoutReport(nodes, edges);
  expect(at.size).toBe(4);
  for (const e of edges) expect(at.get(e.from)!.x).toBeLessThan(at.get(e.to)!.x);
});

it('contradicts edge is dashed and red', () => {
  expect(edgeStyle('contradicts', false)).toEqual({
    stroke: 'var(--danger-text)',
    strokeWidth: 1,
    strokeDasharray: '5 4',
  });
  expect(edgeStyle('tests', false).strokeDasharray).toBeUndefined();
  // Fact edges stay thin and grey even beside the selected node.
  expect(edgeStyle('is_a', true)).toEqual({ stroke: 'var(--chart-rule)', strokeWidth: 1 });
  expect(edgeStyle('tests', true)).toEqual({ stroke: 'var(--text)', strokeWidth: 2 });
});
