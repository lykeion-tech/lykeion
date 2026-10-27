<script setup lang="ts">
import { computed, ref } from 'vue';
import type { ActivityStep } from '../lib/run-transcript';
import RailRow from './RailRow.vue';
import TurnRail from './TurnRail.vue';
import ToolStep from './ToolStep.vue';
const props = defineProps<{ steps: ActivityStep[]; active: boolean }>();
const open = ref(false);
const marker = computed(() =>
  props.steps.some((s) => s.error)
    ? 'error'
    : props.active && props.steps.some((s) => s.running)
      ? 'running'
      : 'ok',
);
const title = computed(() => {
  const tools = new Set(props.steps.map((s) => s.messages[0]?.tool));
  const ran = props.steps.every((s) => !s.running);
  if (!ran) return 'Tool steps';
  if (tools.size === 1) {
    const tool = props.steps[0]?.messages[0]?.tool ?? '';
    const verbs: Record<string, string> = {
      Bash: 'Ran',
      Write: 'Wrote',
      Edit: 'Edited',
      Read: 'Read',
    };
    if (verbs[tool])
      return `${verbs[tool]} ${props.steps.length} ${tool === 'Bash' ? 'commands' : 'files'}`;
  }
  return `Ran ${props.steps.length} steps`;
});
</script>
<template>
  <ToolStep v-if="steps.length === 1" :step="steps[0]!" :active="active" />
  <div v-else-if="steps.length" class="tool-step-group" data-testid="tool-step-group">
    <RailRow :marker="marker" class="tool-step-group-row"
      ><button
        type="button"
        class="tool-step-group-head"
        :aria-expanded="open"
        @click="open = !open"
      >
        <svg
          class="tool-step-chevron"
          :class="{ 'tool-step-chevron--open': open }"
          width="13"
          height="13"
          viewBox="0 0 16 16"
          fill="none"
        >
          <path d="m6 4 4 4-4 4" stroke="currentColor" stroke-width="1.2" /></svg
        ><span class="tool-step-group-title">{{ title }}</span
        ><span class="tool-step-group-summary">{{ steps.length }} steps</span>
      </button></RailRow
    >
    <div v-if="open" class="tool-step-group-body">
      <TurnRail nested
        ><ToolStep v-for="step in steps" :key="step.key" :step="step" :active="active"
      /></TurnRail>
    </div>
  </div>
</template>
