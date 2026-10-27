<script setup lang="ts">
import { computed, ref } from 'vue';
import type { ActivityStep } from '../lib/run-transcript';
import RailRow from './RailRow.vue';
import ToolStepDetail from './ToolStepDetail.vue';
const props = defineProps<{ step: ActivityStep; active: boolean }>();
const open = ref(false);
const marker = computed(() =>
  props.step.error ? 'error' : props.step.running && props.active ? 'running' : 'ok',
);
</script>
<template>
  <RailRow
    :marker="marker"
    class="tool-step"
    :class="[`tool-step--${marker}`, { 'tool-step--open': open }]"
    data-testid="tool-step"
    ><button type="button" class="tool-step-head" :aria-expanded="open" @click="open = !open">
      <span class="tool-step-copy"
        ><span class="tool-step-title">{{ step.title }}</span></span
      >
    </button>
    <div v-if="open" class="tool-step-detail" data-testid="tool-step-detail">
      <ToolStepDetail :step="step" :active="active" /></div
  ></RailRow>
</template>
