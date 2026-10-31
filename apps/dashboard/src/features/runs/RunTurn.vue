<script setup lang="ts">
import { computed, defineAsyncComponent, defineComponent, h, ref } from 'vue';
import RunTimeline from './RunTimeline.vue';
import CopyButton from '../coresearch/CopyButton.vue';
import { liveStatus, transcriptBlocks, visibleTranscriptMessages } from './run-transcript';
import { useRunMessages } from '../../api/queries';
import type { AgentRun } from '../../api/types';
import { readableAssignment } from '../../lib/studies';
import { durationLabel } from './run-timeline';

const formattingFailed = ref(false);
const PlainRecordedText = defineComponent({
  props: { content: { type: String, default: '' } },
  setup: (text) => () => h('div', { class: 'recorded-text-fallback' }, text.content),
});
const RecordedMarkdown = defineAsyncComponent({
  loader: () => import('../../ui/RecordedMarkdown.vue'),
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
const blocks = computed(() =>
  transcriptBlocks(
    visibleTranscriptMessages(messages.data.value ?? []).filter(
      (message) => message.type !== 'thinking',
    ),
    { joinText: false },
  ),
);
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
      <RecordedMarkdown v-else class="lyk-reading" :content="assignment" />
      <div class="assignment-actions"><CopyButton :text="assignment" label="Copy message" /></div>
    </div>
    <RunTimeline
      :blocks="blocks"
      :active="active"
      :attempt="run.attempt"
      :ending="ending"
      :queued="run.status === 'queued'"
    >
      <template #text="{ block }">
        <div v-if="formattingFailed" class="recorded-text-fallback">{{ block.text }}</div>
        <RecordedMarkdown v-else class="lyk-reading" :content="block.text" />
      </template>
    </RunTimeline>
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
  position: relative;
  width: fit-content;
  max-width: min(100%, 52ch);
  margin: 0 0 39px auto;
  padding: 8px 12px;
  border: 1px solid var(--step-line);
  border-radius: 12px;
  background: var(--step-hover);
  min-width: 0;
}
.assignment-actions {
  position: absolute;
  right: 0;
  top: 100%;
  padding-top: 4px;
  opacity: 0;
}
.turn-assignment:hover .assignment-actions,
.turn-assignment:focus-within .assignment-actions {
  opacity: 1;
}
.assignment-actions :deep(.quiet-action) {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 0;
  border-radius: 4px;
  padding: 3px 6px;
  background: transparent;
  color: var(--muted);
  font-size: 12px;
}
.assignment-actions :deep(.quiet-action:hover) {
  color: var(--text);
  background: var(--hover);
}
@media (hover: none) {
  .assignment-actions {
    opacity: 1;
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
