<script setup lang="ts">
import { computed, defineAsyncComponent, defineComponent, h, ref } from 'vue';
import RunActivity from './RunActivity.vue';
import { activityStep, liveStatus, transcriptBlocks } from '../lib/run-transcript';
import { useRunMessages } from '../api/queries';
import type { AgentRun } from '../api/types';
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
const messages = useRunMessages(() => props.run, true);
const active = computed(() => ['dispatched', 'running'].includes(props.run.status));
// Each block's mark on the timeline: messages are rings, finished activity a
// dot, activity with a recorded error a cross, the activity still going a spinner.
const blocks = computed(() => {
  const list = transcriptBlocks(messages.data.value ?? [], {
    joinText: props.run.harness === 'antigravity',
  });
  return list.map((block, i) => ({
    ...block,
    mark:
      block.kind === 'text'
        ? 'mark-message'
        : block.messages.some((m) => activityStep(m).error)
          ? 'mark-fail'
          : active.value && i === list.length - 1
            ? 'mark-run'
            : 'mark-ok',
  }));
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
    <div v-if="run.assignment" class="turn-assignment" aria-label="Assignment">
      <div v-if="formattingFailed" class="recorded-text-fallback">{{ run.assignment }}</div>
      <RecordedMarkdown v-else :content="run.assignment" />
    </div>
    <ol class="turn-timeline" aria-label="Transcript">
      <li v-for="block in blocks" :key="block.key" class="turn-item" :class="block.mark">
        <template v-if="block.kind === 'text'">
          <div v-if="formattingFailed" class="recorded-text-fallback">{{ block.text }}</div>
          <RecordedMarkdown v-else :content="block.text" />
        </template>
        <RunActivity v-else :messages="block.messages" />
      </li>
      <li class="turn-item turn-ending" :class="ending.mark">
        <span :role="active || run.status === 'queued' ? 'status' : undefined">{{
          ending.text
        }}</span>
      </li>
    </ol>
    <p v-if="run.error" class="run-error">{{ run.error }}</p>
    <RunActivity v-if="messages.data.value?.length" :messages="messages.data.value" history />
  </div>
</template>
<style scoped>
.agent-turn {
  font-size: 13px;
  line-height: 20px;
  overflow-wrap: anywhere;
  min-width: 0;
}
/* The run's assignment, on the right like the researcher's own comments. */
.turn-assignment {
  width: fit-content;
  max-width: min(80%, 560px);
  margin: 0 0 16px auto;
  padding: 10px 13px;
  border-radius: 12px;
  background: var(--fill-active);
  min-width: 0;
}
.turn-assignment :deep(.markdown-body) {
  font-size: 13px;
  line-height: 20px;
}
.turn-timeline {
  position: relative;
  list-style: none;
  margin: 0;
  padding: 0 0 0 22px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
/* The rail the marks sit on. */
.turn-timeline::before {
  content: '';
  position: absolute;
  left: 4px;
  top: 10px;
  bottom: 10px;
  width: 1px;
  background: var(--chart-rule);
}
.turn-item {
  position: relative;
  min-width: 0;
}
.turn-item::before {
  content: '';
  position: absolute;
  left: -21px;
  top: 6px;
  width: 7px;
  height: 7px;
  box-sizing: border-box;
  border-radius: 50%;
  box-shadow: 0 0 0 3px var(--modal);
}
.mark-message::before {
  border: 1px solid var(--icon-faint);
  background: var(--modal);
}
.mark-ok::before {
  background: var(--stage-validation-dot);
}
.mark-queued::before {
  border: 1px dashed var(--icon-faint);
  background: var(--modal);
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
  top: 5px;
  width: 9px;
  height: 9px;
  border-radius: 0;
  background:
    linear-gradient(45deg, transparent 43%, var(--error-text) 43% 57%, transparent 57%),
    linear-gradient(-45deg, transparent 43%, var(--error-text) 43% 57%, transparent 57%),
    var(--modal);
}
.turn-ending {
  font-size: 12.5px;
  color: var(--muted);
}
.turn-item :deep(.markdown-body) {
  font-size: 13px;
  line-height: 20px;
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
.agent-turn > :deep(.full-history) {
  margin-top: 20px;
}
</style>
