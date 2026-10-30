<script setup lang="ts">
import { computed, defineAsyncComponent, defineComponent, h, ref } from 'vue';
import RunActivity from './RunActivity.vue';
import {
  activitySteps,
  isActivityGroup,
  liveStatus,
  transcriptBlocks,
} from '../lib/run-transcript';
import { useRunMessages } from '../api/queries';
import type { AgentRun } from '../api/types';
import { readableAssignment } from '../lib/studies';
import { durationLabel } from '../lib/run-timeline';

const formattingFailed = ref(false);
const PlainRecordedText = defineComponent({
  props: { content: { type: String, default: '' } },
  setup: (text) => () => h('div', { class: 'recorded-text-fallback' }, text.content),
});
const RecordedMarkdown = defineAsyncComponent({
  loader: () => import('./RecordedMarkdown.vue'),
  loadingComponent: PlainRecordedText,
  errorComponent: PlainRecordedText,
  delay: 0,
  onError: (_error, _retry, fail) => {
    formattingFailed.value = true;
    fail();
  },
});

const props = defineProps<{ run: AgentRun }>();
const assignment = computed(() => readableAssignment(props.run));
const messages = useRunMessages(() => props.run, true);
const active = computed(() => ['dispatched', 'running'].includes(props.run.status));
// Each block's mark on the timeline: messages are rings, finished activity a
// dot, activity with a recorded error a cross, the activity still going a spinner.
const blocks = computed(() => {
  const list = transcriptBlocks(
    (messages.data.value ?? []).filter(
      (message) => message.type !== 'log' && message.type !== 'thinking',
    ),
    {
      joinText: false,
    },
  );
  return list.map((block, i) => {
    const steps = block.kind === 'activity' ? activitySteps(block.messages) : [];
    const markedSteps = isActivityGroup(steps) ? steps : steps.slice(0, 1);
    const mark =
      block.kind === 'text'
        ? 'mark-message'
        : markedSteps.some((step) => step.error)
          ? 'mark-fail'
          : !markedSteps.some((step) => step.tool)
            ? 'mark-message'
            : active.value &&
                block.messages[0]?.attempt === props.run.attempt &&
                i === list.length - 1 &&
                markedSteps.some((step) => step.running)
              ? 'mark-run'
              : 'mark-ok';
    return { ...block, mark };
  });
});
const ending = computed(() => {
  const r = props.run;
  const took =
    r.started_at && r.completed_at
      ? ` · ${durationLabel(Date.parse(r.completed_at) - Date.parse(r.started_at))}`
      : '';
  if (active.value)
    return { mark: 'mark-live', text: liveStatus(messages.data.value ?? []) || 'Working…' };
  if (r.status === 'completed') return { mark: 'mark-ok', text: `Run finished${took}` };
  if (r.status === 'failed')
    return {
      mark: 'mark-fail',
      text: `Run failed${took}${r.failure_reason ? ` · ${r.failure_reason.replace(/_/g, ' ')}` : ''}`,
    };
  if (r.status === 'queued')
    return {
      mark: 'mark-queued',
      text: r.queue_reason ? `Queued: ${r.queue_reason.replace(/_/g, ' ')}` : 'Queued',
    };
  return { mark: 'mark-queued', text: 'Cancelled' };
});
</script>
<template>
  <div class="agent-turn">
    <p v-if="messages.isError.value" class="run-error run-read-error" role="alert">
      Run messages: {{ messages.error.value?.message }}
      <button class="text-button" :disabled="messages.isFetching.value" @click="messages.refetch()">
        Retry run messages
      </button>
    </p>
    <p v-if="messages.isPending.value && run.attempt > 0" role="status">Loading run messages…</p>
    <p v-if="formattingFailed" class="formatting-notice" role="status">
      Markdown formatting is unavailable; showing recorded text.
    </p>
    <div v-if="assignment" class="turn-assignment" aria-label="Assignment">
      <div v-if="formattingFailed" class="recorded-text-fallback">{{ assignment }}</div>
      <RecordedMarkdown v-else :content="assignment" />
    </div>
    <ol class="turn-timeline" aria-label="Transcript">
      <li v-for="block in blocks" :key="block.key" class="turn-item" :class="block.mark">
        <template v-if="block.kind === 'text'">
          <div v-if="formattingFailed" class="recorded-text-fallback">{{ block.text }}</div>
          <RecordedMarkdown v-else :content="block.text" />
        </template>
        <RunActivity
          v-else
          :messages="block.messages"
          :active="active && block.messages[0]?.attempt === run.attempt"
        />
      </li>
      <li class="turn-item turn-ending" :class="ending.mark">
        <span :role="active || run.status === 'queued' ? 'status' : undefined">{{
          ending.text
        }}</span>
      </li>
    </ol>
    <p v-if="run.error" class="run-error">{{ run.error }}</p>
  </div>
</template>
<style scoped>
.agent-turn {
  --step-canvas: var(--surface);
  --step-border: color-mix(in srgb, var(--text) 12%, var(--surface));
  --step-line: color-mix(in srgb, var(--text) 20%, var(--surface));
  --step-quiet: color-mix(in srgb, var(--text) 40%, var(--surface));
  --step-muted: color-mix(in srgb, var(--text) 55%, var(--surface));
  --step-surface: #edeef3;
  --step-hover: color-mix(in srgb, var(--step-surface) 92%, var(--text));
  --step-done: #27a644;
  width: 100%;
  max-width: 100ch;
  margin-inline: auto;
  font-size: 14px;
  line-height: 21px;
  overflow-wrap: anywhere;
  min-width: 0;
}
/* The run's assignment, on the right like the researcher's own comments. */
.turn-assignment {
  width: fit-content;
  max-width: min(100%, 52ch);
  margin: 0 0 39px auto;
  padding: 8px 12px;
  border: 1px solid var(--step-line);
  border-radius: 12px;
  background: var(--step-hover);
  min-width: 0;
}
.turn-assignment :deep(.markdown-body) {
  font-size: 14px;
  line-height: 21px;
}
.turn-timeline {
  position: relative;
  list-style: none;
  margin: 0;
  padding: 0 0 0 28px;
  display: flex;
  flex-direction: column;
  gap: 0;
}
/* The rail the marks sit on. */
.turn-timeline::before {
  content: '';
  position: absolute;
  left: 10px;
  top: 0;
  bottom: 0;
  width: 1px;
  background: var(--step-line);
}
.turn-item {
  position: relative;
  min-width: 0;
  padding-block: 5px;
}
.turn-item:has(> .run-activity) {
  padding-block: 0;
}
.turn-item:has(> .run-activity)::before {
  top: 9px;
}
.turn-item::before {
  content: '';
  position: absolute;
  left: -21px;
  top: 12px;
  width: 7px;
  height: 7px;
  box-sizing: border-box;
  border-radius: 50%;
  box-shadow: 0 0 0 4px var(--step-canvas);
}
.mark-message::before {
  border: 1px solid var(--step-muted);
  background: var(--step-canvas);
}
.mark-ok::before {
  background: var(--step-done);
}
.mark-queued::before {
  border: 1px dashed var(--step-muted);
  background: var(--step-canvas);
}
.mark-live::before {
  background: var(--stage-experimentation-dot);
  animation: turn-pulse 1.6s ease-in-out infinite;
}
.mark-run::before {
  border: 1.5px solid var(--primary);
  border-right-color: transparent;
  animation: turn-spin 1s linear infinite;
}
.mark-fail::before {
  left: -22px;
  top: 11px;
  width: 9px;
  height: 9px;
  border-radius: 0;
  background:
    linear-gradient(45deg, transparent 43%, var(--error-text) 43% 57%, transparent 57%),
    linear-gradient(-45deg, transparent 43%, var(--error-text) 43% 57%, transparent 57%),
    var(--step-canvas);
}
.turn-ending {
  font-size: 12.5px;
  color: var(--muted);
}
.turn-item :deep(.markdown-body) {
  font-size: 14px;
  line-height: 21px;
}
@keyframes turn-pulse {
  50% {
    opacity: 0.35;
  }
}
@keyframes turn-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .turn-item::before {
    animation: none;
  }
}
.recorded-text-fallback {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.formatting-notice {
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 8px;
}
.run-error {
  color: var(--error-text);
  margin-top: 10px;
}
</style>

<style>
:root[data-theme='dark'] .agent-turn {
  --step-surface: color-mix(in srgb, var(--surface) 92%, var(--text));
}
</style>
