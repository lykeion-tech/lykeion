import { Graph, layout } from '@dagrejs/dagre';
import type { ReportEdge, ReportNode } from '../../api/types';

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
