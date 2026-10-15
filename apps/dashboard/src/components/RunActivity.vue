<script setup lang="ts">
import { computed, ref } from 'vue';
import type { RunMessage } from '../api/types';
import { activityStep, recordedContent } from '../lib/run-transcript';
const props = defineProps<{ messages: RunMessage[]; history?: boolean }>();
const expanded = ref(false);
function toggle(event: Event) {
  if (event.target === event.currentTarget)
    expanded.value = (event.target as HTMLDetailsElement).open;
}
const steps = computed(() =>
  props.messages.map((message) => ({ message, ...activityStep(message) })),
);
const errors = computed(() => steps.value.filter((step) => step.error).length);
</script>
<template>
  <details
    class="run-activity"
    data-activity-group
    :class="{ 'full-history': history }"
    @toggle="toggle"
  >
    <summary>
      <span>{{ history ? 'Full recorded history' : 'Activity' }}</span>
      <span class="activity-count">{{ messages.length }} {{ history ? 'messages' : 'steps' }}</span>
      <span v-if="errors" class="activity-error"
        >{{ errors }} {{ errors === 1 ? 'error' : 'errors' }}</span
      >
    </summary>
    <ol
      v-if="!history || expanded"
      class="activity-steps"
      :aria-label="history ? 'Recorded run messages' : 'Recorded activity'"
    >
      <li
        v-for="step in steps"
        :key="`${step.message.attempt}:${step.message.seq}`"
        :data-message-type="step.message.type"
      >
        <div class="step-heading">
          <span class="step-label">{{ step.label }}</span
          ><span v-if="step.detail" class="step-intent" :title="step.detail">{{ step.detail }}</span
          ><span class="step-state" :class="{ 'activity-error': step.error }">{{
            step.state
          }}</span>
        </div>
        <details v-if="step.output" class="step-output">
          <summary>
            <span class="when-closed">{{
              step.message.type === 'thinking'
                ? 'Show recorded reasoning'
                : step.message.type === 'text'
                  ? 'Show recorded text'
                  : 'Show output'
            }}</span>
            <span class="when-open">{{
              step.message.type === 'thinking'
                ? 'Hide recorded reasoning'
                : step.message.type === 'text'
                  ? 'Hide recorded text'
                  : 'Hide output'
            }}</span>
          </summary>
          <pre tabindex="0">{{ step.output }}</pre>
        </details>
        <details class="step-payload">
          <summary>
            Full recorded message<span class="step-sequence"
              >Attempt {{ step.message.attempt }} · Sequence {{ step.message.seq }}</span
            >
          </summary>
          <pre tabindex="0">{{ recordedContent(step.message) }}</pre>
        </details>
      </li>
    </ol>
  </details>
</template>
<style scoped>
.run-activity {
  margin: 12px 0 16px;
  font-size: 12px;
  color: var(--muted);
  min-width: 0;
}
.run-activity > summary {
  cursor: pointer;
  width: fit-content;
  max-width: 100%;
  line-height: 1.8;
}
.run-activity > summary > span + span {
  margin-left: 9px;
}
.run-activity > summary > span:first-child {
  color: var(--text);
  font-weight: 500;
}
.activity-count {
  font-size: 11.5px;
}
.activity-error {
  color: var(--error-text);
}
.activity-steps {
  list-style: none;
  padding: 0 0 0 15px;
  margin: 8px 0 0 4px;
  border-left: 1px solid var(--border);
}
.activity-steps > li {
  padding: 7px 0 9px;
  min-width: 0;
}
.activity-steps > li + li {
  border-top: 1px solid var(--border);
}
.step-heading {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}
.step-label {
  font-weight: 500;
  color: var(--text);
  flex: none;
}
.step-intent {
  overflow-wrap: anywhere;
  min-width: 0;
}
.step-state {
  margin-left: auto;
  flex: none;
  font-size: 11px;
  text-transform: capitalize;
}
.step-output,
.step-payload {
  font-size: 11px;
}
.step-output summary,
.step-payload summary {
  cursor: pointer;
  overflow-wrap: anywhere;
}
.step-output .when-open {
  display: none;
}
.step-output[open] > summary .when-closed {
  display: none;
}
.step-output[open] > summary .when-open {
  display: inline;
}
.step-sequence {
  margin-left: 8px;
  font-size: 10.5px;
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
.full-history {
  border-top: 1px solid var(--border);
  padding-top: 8px;
  margin-top: 18px;
}
@media (max-width: 600px) {
  .step-heading {
    flex-wrap: wrap;
    gap: 3px 8px;
  }
  .step-intent {
    flex-basis: 100%;
    order: 3;
  }
  .step-sequence {
    display: block;
    margin-left: 12px;
  }
}
</style>
