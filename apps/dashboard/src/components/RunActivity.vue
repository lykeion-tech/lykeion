<script setup lang="ts">
import { computed } from 'vue';
import Icon from './Icon.vue';
import RunStep from './RunStep.vue';
import type { RunMessage } from '../api/types';
import { activitySteps } from '../lib/run-transcript';
const props = defineProps<{ messages: RunMessage[]; active?: boolean }>();
const steps = computed(() => activitySteps(props.messages));
const errors = computed(() => steps.value.filter((s) => s.error).length);
const working = computed(() => props.active && steps.value.some((s) => s.running));
</script>
<template>
  <details v-if="steps.length > 1" class="run-activity" data-activity-group>
    <summary>
      <Icon name="right" :size="11" class="activity-chevron" />
      <span class="activity-title">{{ working ? 'Running' : 'Ran' }} {{ steps.length }} steps</span>
      <span class="activity-count">{{ steps.length }} steps</span>
      <span v-if="errors" class="activity-error"
        >{{ errors }} {{ errors === 1 ? 'error' : 'errors' }}</span
      >
    </summary>
    <ol class="activity-steps" aria-label="Recorded activity">
      <RunStep v-for="step in steps" :key="step.key" :step="step" :active="active" nested />
    </ol>
  </details>
  <ol v-else class="run-activity activity-single" aria-label="Recorded activity">
    <RunStep v-for="step in steps" :key="step.key" :step="step" :active="active" />
  </ol>
</template>
<style scoped>
.run-activity {
  margin: 0;
  font-size: 13px;
  line-height: 20px;
  color: var(--muted);
  min-width: 0;
}
.run-activity > summary {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  list-style: none;
  min-height: 24px;
  padding: 1px 4px;
  margin-inline: -4px;
  border-radius: 6px;
}
.run-activity > summary::-webkit-details-marker {
  display: none;
}
.run-activity > summary:hover {
  background: var(--hover);
}
.activity-chevron {
  color: var(--icon-faint);
  transition: transform 0.12s;
}
.run-activity[open] > summary > .activity-chevron {
  transform: rotate(90deg);
}
.activity-title {
  color: var(--text);
  font-weight: 500;
}
.activity-count {
  margin-left: auto;
  font:
    11px ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
  color: var(--icon-faint);
}
.activity-error {
  color: var(--error-text);
  font-size: 11px;
}
.activity-steps {
  list-style: none;
  padding: 0 0 0 6px;
  margin: 0;
}
.activity-single {
  padding: 0;
  list-style: none;
}
summary:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .activity-chevron {
    transition: none;
  }
}
</style>
