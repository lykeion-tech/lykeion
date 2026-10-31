<script setup lang="ts">
import { computed } from 'vue';
import { CheckCircle2, Circle, CircleX, LoaderCircle } from '@lucide/vue';
import Icon from '../../ui/Icon.vue';
import RunStepCode from './RunStepCode.vue';
import { recordedContent, type ActivityStep } from './run-transcript';
const props = defineProps<{ step: ActivityStep; active?: boolean; nested?: boolean }>();
const fallback = computed(() => {
  const step = props.step;
  if (step.notebook || step.command || step.detail || (!step.tool && step.output)) return '';
  const message = step.messages[0];
  if (!message || message.content == null) return '';
  const content = message.content;
  if (typeof content === 'object' && !Array.isArray(content)) {
    const input = (content as Record<string, unknown>).input;
    if (input != null) return recordedContent(input);
  }
  return recordedContent(content);
});
</script>
<template>
  <li
    class="activity-step"
    :class="{ nested, failed: step.error, running: active && step.running }"
    :data-message-type="step.messages[0]!.type"
  >
    <details class="tool-step" :open="step.error">
      <summary class="step-heading">
        <Icon name="right" :size="13" class="step-chevron" />
        <CircleX v-if="step.error" :size="14" class="step-status" aria-label="Error" />
        <LoaderCircle
          v-else-if="active && step.running"
          :size="14"
          class="step-status step-spinner"
          aria-label="Running"
        />
        <CheckCircle2
          v-else-if="step.tool && !step.running"
          :size="14"
          class="step-status step-success"
          aria-label="Completed"
        />
        <Circle v-else :size="14" class="step-status" aria-label="Recorded" />
        <span class="step-label">{{ step.title }}</span>
      </summary>
      <div class="step-body">
        <template v-if="step.notebook || step.command">
          <span class="step-detail-label">{{ step.notebook ? 'Code' : 'Command' }}</span>
          <RunStepCode
            v-if="step.notebook"
            :code="step.notebook.code"
            :language="step.notebook.language"
            :execution-count="step.notebook.executionCount"
            notebook
          />
          <RunStepCode v-else-if="step.command" :code="step.command" language="bash" />
        </template>
        <p v-else-if="step.detail" class="step-intent">{{ step.detail }}</p>
        <template v-else-if="fallback">
          <span class="step-detail-label">{{ step.tool ? 'Input' : 'Content' }}</span>
          <pre class="step-output" tabindex="0">{{ fallback }}</pre>
        </template>
        <template v-if="step.output">
          <span class="step-detail-label">Output</span>
          <pre class="step-output" tabindex="0">{{ step.output }}</pre>
        </template>
      </div>
    </details>
  </li>
</template>
<style scoped>
.activity-step {
  min-width: 0;
  list-style: none;
}
.step-heading {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  cursor: pointer;
  list-style: none;
  padding: 7px 8px;
  border-radius: 6px;
  min-width: 0;
}
.step-heading::-webkit-details-marker {
  display: none;
}
.step-heading:hover {
  background: var(--step-hover, var(--hover));
}
.step-chevron,
.step-status {
  flex: none;
  margin-top: 3px;
  color: var(--step-quiet, var(--muted));
}
.step-chevron {
  transition: transform 0.12s;
}
.tool-step[open] > .step-heading > .step-chevron {
  transform: rotate(90deg);
}
.failed .step-status {
  color: var(--error-text);
}
.step-success {
  color: var(--step-done, var(--stage-validation-dot));
}
.running .step-status {
  color: var(--stage-experimentation-dot);
}
.step-spinner {
  animation: spin 1.5s linear infinite;
}
.step-label {
  flex: 1;
  min-width: 0;
  color: var(--text);
  font-size: 13px;
  line-height: 20px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.step-body {
  margin: 0 8px 12px 38px;
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}
.step-detail-label {
  margin-top: 6px;
  font-size: 12px;
  color: var(--muted);
}
.step-intent {
  margin: 6px 0;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.step-output {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  overflow: auto;
  padding: 10px;
  background: var(--input);
  border-radius: 6px;
  color: var(--text);
  font:
    12px/1.5 'JetBrains Mono Variable',
    ui-monospace,
    monospace;
}
.failed .step-output {
  color: var(--error-text);
}
summary:focus-visible,
pre:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 3px;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .step-spinner {
    animation: none;
  }
  .step-chevron {
    transition: none;
  }
}
</style>
