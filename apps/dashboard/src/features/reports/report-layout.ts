import { Graph, layout } from '@dagrejs/dagre';
import type { ReportEdge, ReportNode, ReportNodeKind } from '../../api/types';

export const NODE_WIDTH = 180;
export const NODE_HEIGHT = 40;

// Lays the argument out left to right: the question, then what grounds and
// tests it, then the findings. Positions are top-left corners.
export function layoutReport(
  nodes: ReportNode[],
  edges: ReportEdge[],
): Map<string, { x: number; y: number }> {
  const g = new Graph();
  g.setGraph({ rankdir: 'LR', nodesep: 24, ranksep: 64 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  for (const e of edges) if (g.hasNode(e.from) && g.hasNode(e.to)) g.setEdge(e.from, e.to);
  layout(g);
  return new Map(
    nodes.map((n) => {
      const { x, y } = g.node(n.id);
      return [n.id, { x: x - NODE_WIDTH / 2, y: y - NODE_HEIGHT / 2 }];
    }),
  );
}

// Each node kind's dot colour, shared by the nodes and the legend.
export const kindDot: Record<ReportNodeKind, string> = {
  question: 'var(--text)',
  paper: 'var(--stage-dot)',
  dataset: 'var(--stage-dot)',
  hypothesis: 'var(--stage-hypotheses-dot)',
  experiment: 'var(--stage-experimentation-dot)',
  output: 'var(--stage-experimentation-dot)',
  finding: 'var(--stage-validation-dot)',
  objection: 'var(--danger-text)',
  doubt: 'var(--danger-text)',
  concept: 'var(--stage-reporting-dot)',
};
export const legend: { label: string; kind: ReportNodeKind }[] = [
  { label: 'Question', kind: 'question' },
  { label: 'Source', kind: 'paper' },
  { label: 'Hypothesis', kind: 'hypothesis' },
  { label: 'Experiment or output', kind: 'experiment' },
  { label: 'Finding', kind: 'finding' },
  { label: 'Objection or doubt', kind: 'objection' },
  { label: 'Concept', kind: 'concept' },
];

const factKinds = new Set<ReportEdge['kind']>([
  'is_a',
  'part_of',
  'associated_with',
  'increases',
  'decreases',
  'measured_by',
  'mentioned_in',
]);

// A contradicting paper is a dashed red edge; a fact is a thin grey one; any
// other edge darkens beside the selected node.
export function edgeStyle(kind: ReportEdge['kind'], touching: boolean) {
  if (kind === 'contradicts')
    return { stroke: 'var(--danger-text)', strokeWidth: 1, strokeDasharray: '5 4' };
  if (factKinds.has(kind) || !touching) return { stroke: 'var(--chart-rule)', strokeWidth: 1 };
  return { stroke: 'var(--text)', strokeWidth: 2 };
}
