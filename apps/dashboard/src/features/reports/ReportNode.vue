<script setup lang="ts">
import { computed } from 'vue';
import type { ReportNodeKind } from '../../api/types';
import { kindDot } from './report-layout';
import { kindName } from './report-sections';

const props = defineProps<{
  id: string;
  data: {
    kind: ReportNodeKind;
    label: string;
    status: string;
    selected: boolean;
    hovered?: boolean;
  };
}>();
const emit = defineEmits<{ select: [id: string] }>();
// The node is one line wide; CSS adds the ellipsis, this keeps a huge label
// out of the DOM. The aria label always carries the full text.
const shortLabel = computed(() =>
  props.data.label.length > 80 ? `${props.data.label.slice(0, 80)}…` : props.data.label,
);
function onKey(e: KeyboardEvent) {
  if (e.key !== 'Enter' && e.key !== ' ') return;
  e.preventDefault();
  emit('select', props.id);
}
</script>

<template>
  <div
    class="report-node"
    :class="[`kind-${data.kind}`, { rejected: data.status === 'rejected', hovered: data.hovered }]"
    role="button"
    :tabindex="0"
    :aria-label="`${kindName(data.kind)}: ${data.label}`"
    :aria-pressed="data.selected"
    @click="emit('select', id)"
    @keydown="onKey"
  >
    <span class="report-node-dot" :style="{ background: kindDot[data.kind] }" aria-hidden="true" />
    <span class="report-node-label">{{ shortLabel }}</span>
  </div>
</template>

<style scoped>
.report-node {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 180px;
  height: 40px;
  padding: 0 10px;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--fill-raised);
  color: var(--text);
  cursor: pointer;
  /* Vue Flow turns pointer events off on nodes it can neither select nor drag. */
  pointer-events: all;
}
.report-node:hover,
.report-node.hovered {
  background: var(--hover);
}
.report-node[aria-pressed='true'] {
  border-color: var(--muted);
  background: var(--hover);
}
.report-node:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}
.report-node.kind-concept {
  border-radius: 20px;
}
.report-node.rejected {
  opacity: 0.55;
}
.report-node-dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.report-node-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
