<script setup lang="ts">
import Icon from './Icon.vue';
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
        <Icon name="right" :size="11" class="step-chevron" />
        <span v-if="step.error || (active && step.running)" class="sr-only">{{
          step.error ? 'Error' : 'Running'
        }}</span>
      </summary>
      <div class="step-body">
        <p class="step-provenance">
          {{ step.label }}<span v-if="step.error" class="activity-error"> · Error</span>
        </p>
        <pre v-if="step.detail" class="step-intent" tabindex="0">{{ step.detail }}</pre>
        <details v-if="step.output" class="step-output">
          <summary>
            {{ step.messages[0]!.type === 'thinking' ? 'Recorded reasoning' : 'Show output' }}
          </summary>
          <pre tabindex="0">{{ step.output }}</pre>
        </details>
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
      </div>
    </details>
  </li>
</template>
<style scoped>
.activity-step {
  position: relative;
  min-width: 0;
  padding: 2px 0;
  list-style: none;
}
.activity-step.nested {
  padding-left: 16px;
}
.activity-step.nested::before {
  content: '';
  position: absolute;
  width: 1px;
  left: 7.5px;
  top: 0;
  bottom: 0;
  background: var(--border);
}
.step-marker {
  display: none;
  position: absolute;
  left: 4.5px;
  top: 11px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--stage-validation-dot);
  box-shadow: 0 0 0 3px var(--modal);
}
.nested .step-marker {
  display: block;
}
.recorded .step-marker {
  background: var(--modal);
  border: 1px solid var(--icon-faint);
}
.failed .step-marker {
  border: 0;
  color: var(--error-text);
  background: var(--modal);
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
  align-items: baseline;
  gap: 8px;
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
  background: var(--hover);
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
.step-chevron {
  color: var(--icon-faint);
  flex-shrink: 0;
  align-self: center;
  transition: transform 0.12s;
  opacity: 0;
}
.step-heading:hover .step-chevron,
.step-heading:focus-visible .step-chevron,
.tool-step[open] > .step-heading .step-chevron {
  opacity: 1;
}
.tool-step[open] > .step-heading .step-chevron {
  transform: rotate(90deg);
}
.step-body {
  padding: 5px 0 10px;
  color: var(--muted);
  font-size: 11.5px;
}
.step-provenance {
  margin: 0 0 5px;
}
.activity-error {
  color: var(--error-text);
}
.step-intent {
  margin: 0 0 8px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font:
    11.5px/1.6 ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
.step-output,
.step-payload {
  margin-top: 4px;
}
.step-output summary,
.step-payload summary {
  cursor: pointer;
  overflow-wrap: anywhere;
}
.record-kind {
  color: var(--icon-faint);
}
.step-output pre,
.step-payload pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  overflow: auto;
  padding: 10px;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 5px;
  font:
    11.5px/1.6 ui-monospace,
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
  .step-chevron {
    transition: none;
  }
}
</style>
