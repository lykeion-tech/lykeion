<script setup lang="ts">
import { computed } from 'vue';
import { Handle, Position, VueFlow, type Edge, type Node } from '@vue-flow/core';
import '@vue-flow/core/dist/style.css';
import type { StudyReport } from '../../api/types';
import { layoutReport } from './report-layout';
import ReportNode from './ReportNode.vue';

const props = defineProps<{ report: StudyReport; selected: string | null }>();
const emit = defineEmits<{ select: [id: string] }>();

const ids = computed(() => new Set(props.report.nodes.map((n) => n.id)));
const edgesShown = computed(() =>
  props.report.edges.filter((e) => ids.value.has(e.from) && ids.value.has(e.to)),
);
const positions = computed(() => layoutReport(props.report.nodes, edgesShown.value));
const nodes = computed<Node[]>(() =>
  props.report.nodes.map((n) => ({
    id: n.id,
    type: 'report',
    position: positions.value.get(n.id)!,
    data: { kind: n.kind, label: n.label, status: n.status, selected: n.id === props.selected },
  })),
);
const edges = computed<Edge[]>(() =>
  edgesShown.value.map((e) => {
    const touching =
      props.selected !== null && (e.from === props.selected || e.to === props.selected);
    const contradicts = e.kind === 'contradicts';
    return {
      id: `${e.from}>${e.to}:${e.kind}`,
      source: e.from,
      target: e.to,
      style: {
        stroke: contradicts ? 'var(--danger-text)' : touching ? 'var(--text)' : 'var(--chart-rule)',
        strokeWidth: touching ? 2 : 1,
        strokeDasharray: contradicts ? '5 4' : undefined,
      },
    };
  }),
);
</script>

<template>
  <!-- Each node is its own button, so Vue Flow's wrapper is kept out of the tab order. -->
  <VueFlow
    class="report-graph type-small"
    :nodes="nodes"
    :edges="edges"
    :nodes-draggable="false"
    :nodes-connectable="false"
    :elements-selectable="false"
    :nodes-focusable="false"
    :edges-focusable="false"
    :min-zoom="0.2"
    fit-view-on-init
  >
    <template #node-report="node">
      <Handle type="target" :position="Position.Left" :connectable="false" />
      <ReportNode :id="node.id" :data="node.data" @select="emit('select', $event)" />
      <Handle type="source" :position="Position.Right" :connectable="false" />
    </template>
  </VueFlow>
</template>

<style scoped>
.report-graph {
  width: 100%;
  height: 100%;
  background: var(--canvas);
}
.report-graph :deep(.vue-flow__handle) {
  opacity: 0;
  pointer-events: none;
}
</style>
