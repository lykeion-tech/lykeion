<script setup lang="ts">
import { computed } from 'vue';
import { parseAlignment, parseTree, treeViewportWidth, type TreeNode } from './alignment-tree';
const props = defineProps<{ text: string; extension: string; partial: boolean }>();
const alignment = computed(() => parseAlignment(props.text, props.extension, props.partial));
const tree = computed(() => parseTree(props.text, props.partial));
const isTree = computed(() => ['nwk', 'newick', 'tree', 'nhx'].includes(props.extension));
const drawing = computed(() => {
  if (!tree.value) return undefined;
  const nodes: { x: number; y: number; name: string }[] = [];
  const lines: { x1: number; y1: number; x2: number; y2: number }[] = [];
  let leaf = 0;
  const walk = (node: TreeNode, depth: number): { x: number; y: number } => {
    const children = node.children.map((child) => walk(child, depth + 1));
    const x = 12 + depth * 48;
    const y = children.length
      ? (children[0]!.y + children[children.length - 1]!.y) / 2
      : 20 + leaf++ * 24;
    if (children.length) {
      lines.push({ x1: x, y1: children[0]!.y, x2: x, y2: children[children.length - 1]!.y });
      for (const child of children) lines.push({ x1: x, y1: child.y, x2: child.x, y2: child.y });
    }
    nodes.push({ x, y, name: node.name });
    return { x, y };
  };
  walk(tree.value, 0);
  return {
    nodes,
    lines,
    width: treeViewportWidth(nodes),
    height: Math.max(40, leaf * 24 + 16),
  };
});
function residueClass(value: string) {
  const v = value.toUpperCase();
  if ('ACGTU'.includes(v)) return `base-${v === 'U' ? 'T' : v}`;
  return '';
}
</script>
<template>
  <div v-if="isTree && drawing" class="preview-scroll">
    <svg
      :width="drawing.width"
      :height="drawing.height"
      role="img"
      aria-label="Phylogenetic cladogram"
    >
      <line v-for="(line, i) in drawing.lines" :key="i" v-bind="line" stroke="currentColor" />
      <text v-for="(node, i) in drawing.nodes" :key="`n${i}`" :x="node.x + 5" :y="node.y - 4">
        {{ node.name }}
      </text>
    </svg>
    <details>
      <summary>Tree labels</summary>
      <ul>
        <li v-for="(node, i) in drawing.nodes.filter((n) => n.name)" :key="i">{{ node.name }}</li>
      </ul>
    </details>
  </div>
  <div v-else-if="!isTree && alignment" class="preview-scroll">
    <p v-if="alignment.truncated">Showing the first 300 of {{ alignment.width }} columns.</p>
    <table aria-label="Sequence alignment">
      <tbody>
        <tr v-for="(row, i) in alignment.rows" :key="i">
          <th>{{ row.name }}</th>
          <td>
            <span v-for="(base, j) in row.sequence" :key="j" :class="residueClass(base)">{{
              base
            }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
  <p v-else>Preview unavailable for this incomplete or unsupported file.</p>
</template>
<style scoped>
.preview-scroll {
  overflow: auto;
  max-width: 100%;
}
table {
  border-collapse: collapse;
  font:
    12px/1.6 ui-monospace,
    monospace;
}
th {
  position: sticky;
  left: 0;
  text-align: left;
  background: var(--surface, white);
  padding-right: 16px;
}
td {
  white-space: pre;
}
.base-A {
  color: #21805f;
}
.base-C {
  color: #286bb2;
}
.base-G {
  color: #aa6b00;
}
.base-T {
  color: #bc4056;
}
svg {
  font: 12px sans-serif;
}
</style>
