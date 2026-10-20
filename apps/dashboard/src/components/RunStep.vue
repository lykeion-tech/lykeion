<script setup lang="ts">
import RunStepCode from './RunStepCode.vue';
import { recordedContent, type ActivityStep } from '../lib/run-transcript';
defineProps<{ step: ActivityStep; active?: boolean; nested?: boolean }>();
</script>
<template>
  <li
    class="activity-step"
    :class="{ nested, recorded: !step.tool, failed: step.error, running: active && step.running }"
    :data-message-type="step.messages[0]!.type"
  >
    <span class="step-marker" aria-hidden="true">{{ step.error ? '×' : '' }}</span>
    <details class="tool-step" open>
      <summary class="step-heading">
        <span class="step-label">{{ step.title }}</span>
        <span v-if="step.error || (active && step.running)" class="sr-only">{{
          step.error ? 'Error' : 'Running'
        }}</span>
      </summary>
      <div class="step-body">
        <RunStepCode
          v-if="step.notebook"
          :code="step.notebook.code"
          :language="step.notebook.language"
          :execution-count="step.notebook.executionCount"
          notebook
        />
        <RunStepCode v-else-if="step.command" :code="step.command" language="bash" />
        <p v-else-if="step.detail" class="step-intent">
          <span class="step-detail-label">{{ step.label }}</span
          >{{ step.detail }}
        </p>
        <details class="step-output">
          <summary>
            {{
              step.messages[0]!.type === 'thinking'
                ? 'Recorded reasoning'
                : step.output
                  ? 'Show output'
                  : 'Show recorded details'
            }}
          </summary>
          <pre v-if="step.output" tabindex="0">{{ step.output }}</pre>
          <details
            v-for="message in step.messages"
            :key="`${message.attempt}:${message.seq}`"
            class="step-payload"
            :data-message-type="message.type"
          >
            <summary>
              Full recorded message<span v-if="step.messages.length > 1" class="record-kind">
                · {{ message.type === 'tool_result' ? 'result' : 'call' }}</span
              >
            </summary>
            <pre tabindex="0">{{ recordedContent(message) }}</pre>
          </details>
        </details>
      </div>
    </details>
  </li>
</template>
<style scoped>
.activity-step {
  position: relative;
  min-width: 0;
  padding: 1px 0;
  min-height: 26px;
  list-style: none;
}
.activity-step.nested {
  padding-left: 24px;
}
.activity-step.nested::before {
  content: '';
  position: absolute;
  width: 1px;
  left: 7.5px;
  top: 0;
  bottom: 0;
  background: var(--step-border, var(--border));
}
.step-marker {
  display: none;
  position: absolute;
  left: 4.5px;
  top: 9px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--step-done, var(--stage-validation-dot));
  box-shadow: 0 0 0 4px var(--step-canvas, var(--modal));
}
.nested .step-marker {
  display: block;
}
.recorded .step-marker {
  background: var(--step-canvas, var(--modal));
  border: 1px solid var(--step-quiet, var(--icon-faint));
}
.failed .step-marker {
  border: 0;
  color: var(--error-text);
  background: var(--step-canvas, var(--modal));
  border-radius: 0;
  font-size: 13px;
  line-height: 5px;
}
.running .step-marker {
  background: var(--stage-experimentation-dot);
  animation: pulse 1.6s ease-in-out infinite;
}
.step-heading {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  cursor: pointer;
  list-style: none;
  padding: 1px 4px;
  margin: 0 -4px;
  border-radius: 6px;
  min-height: 22px;
}
.step-heading::-webkit-details-marker {
  display: none;
}
.step-heading:hover {
  background: var(--step-hover, var(--hover));
}
.step-label {
  flex: 1;
  min-width: 0;
  color: var(--text);
  font-size: 14px;
  line-height: 20.3px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.step-body {
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  color: var(--step-quiet, var(--muted));
  font-size: 12px;
}
.step-body:has(> .notebook-cell) {
  gap: 2px;
}
.step-intent {
  margin: 0;
  display: flex;
  gap: 10px;
  align-items: baseline;
  font:
    13px/1.45 'Inter Variable',
    sans-serif;
  overflow-wrap: anywhere;
}
.step-detail-label {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--step-quiet, var(--icon-faint));
}
.step-output,
.step-payload {
  margin: 0;
}
.step-output > summary,
.step-payload > summary {
  padding: 4px 2px;
  line-height: 16.8px;
  list-style: none;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.record-kind {
  color: var(--step-quiet, var(--icon-faint));
}
.step-output pre,
.step-payload pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  overflow: auto;
  padding: 10px;
  background: var(--input);
  border: 1px solid var(--step-border, var(--border));
  border-radius: 5px;
  font:
    12px/1.5 'JetBrains Mono Variable',
    ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
summary:focus-visible,
pre:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
@keyframes pulse {
  50% {
    opacity: 0.35;
  }
}
@media (prefers-reduced-motion: reduce) {
  .step-marker {
    animation: none;
  }
}

.step-output > summary::-webkit-details-marker,
.step-payload > summary::-webkit-details-marker {
  display: none;
}
.step-output > summary::before,
.step-payload > summary::before {
  content: '▸';
  display: inline-block;
  width: 1em;
}
.step-output[open] > summary::before,
.step-payload[open] > summary::before {
  content: '▾';
}
</style>
