<script setup lang="ts">
import { computed } from 'vue';
import { Handle, Position, VueFlow, type Edge, type Node } from '@vue-flow/core';
import '@vue-flow/core/dist/style.css';
import type { StudyReport } from '../../api/types';
import { edgeStyle, kindDot, layoutReport, legend } from './report-layout';
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
    return {
      id: `${e.from}>${e.to}:${e.kind}`,
      source: e.from,
      target: e.to,
      style: edgeStyle(e.kind, touching),
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
  <ul class="report-legend type-caption" aria-label="Legend">
    <li v-for="item in legend" :key="item.kind">
      <i :style="{ background: kindDot[item.kind] }" aria-hidden="true" />{{ item.label }}
    </li>
  </ul>
</template>

<style scoped>
.report-legend {
  position: absolute;
  left: 8px;
  bottom: 8px;
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  max-width: calc(100% - 16px);
  margin: 0;
  padding: 6px 10px;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--fill-raised);
  color: var(--muted);
  pointer-events: none;
}
.report-legend li {
  display: flex;
  align-items: center;
  gap: 5px;
}
.report-legend i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
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
