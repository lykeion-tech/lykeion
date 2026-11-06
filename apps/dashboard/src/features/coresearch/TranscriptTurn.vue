<script setup lang="ts">
import { computed } from 'vue';
import { transcriptBlocks, visibleTranscriptMessages } from '../runs/run-transcript';
import RunTimeline from '../runs/RunTimeline.vue';
import RunStatus from '../runs/RunStatus.vue';
import { isActiveTurn, useCoresearchSteps } from './api';
import type { MessageRow } from './types';
import AssistantMessage from './AssistantMessage.vue';
import CopyButton from './CopyButton.vue';
import { cleanAssistantText } from './assistant-paths';
import ExperimentFileCard from './ExperimentFileCard.vue';
import type { TranscriptFile } from './transcript-files';
const props = defineProps<{
  projectId: string;
  chatId: string;
  turn: MessageRow;
  replies: MessageRow[];
  settledReplies?: string[];
  files?: TranscriptFile[];
}>();
const emit = defineEmits<{ 'open-file': [TranscriptFile] }>();
const active = computed(() => isActiveTurn(props.turn.status));
const steps = useCoresearchSteps(
  () => props.projectId,
  () => props.chatId,
  () => props.turn.agent_run_id ?? '',
  () => active.value,
);
const replies = computed(() => props.replies.filter((m) => m.kind === 'reply' && m.body.trim()));
const blocks = computed(() =>
  transcriptBlocks(
    visibleTranscriptMessages(steps.data.value ?? []).map((m) =>
      m.type === 'thinking' && typeof m.content === 'string' ? { ...m, type: 'text' as const } : m,
    ),
  ).filter(
    (b) =>
      !(
        b.kind === 'text' &&
        (props.settledReplies ?? replies.value.map((m) => m.body)).includes(b.text)
      ),
  ),
);
const timelineBlocks = computed(() => [
  ...blocks.value,
  ...replies.value.map((reply) => ({
    key: `reply:${reply.id}`,
    kind: 'text' as const,
    text: reply.body,
    messages: [],
  })),
]);
const thinkingKeys = computed(
  () =>
    new Set(
      (steps.data.value ?? [])
        .filter((m) => m.type === 'thinking')
        .map((m) => `${m.attempt}:${m.seq}`),
    ),
);
const copy = computed(() =>
  [
    ...blocks.value
      .filter((b) => b.kind === 'text' && !thinkingKeys.value.has(b.key))
      .map((b) => b.text),
    ...replies.value.map((m) => m.body),
  ]
    .filter(Boolean)
    .join('\n\n'),
);
</script>
<template>
  <div class="msg msg--user">
    <AssistantMessage :text="turn.body" prose />
    <div class="msg-actions"><CopyButton :text="turn.body" /></div>
  </div>
  <p v-if="steps.error.value" role="alert" class="run-line run-line--failed">
    Could not read tool steps: {{ steps.error.value.message }}
  </p>
  <div v-if="blocks.length || replies.length" class="turn-reply">
    <RunTimeline class="coresearch-run-timeline" :blocks="timelineBlocks" :active="active">
      <template #text="{ block }">
        <div
          :class="
            thinkingKeys.has(block.key)
              ? 'turn-block turn-block--thinking'
              : 'turn-block turn-block--interim'
          "
        >
          <AssistantMessage
            class="lyk-reading"
            :text="block.text"
            :live="active"
            redact-local-paths
          />
        </div>
      </template>
    </RunTimeline>
    <div v-if="copy" class="msg-actions"><CopyButton :text="cleanAssistantText(copy)" /></div>
  </div>
  <section v-if="files?.length" class="generated-files" aria-label="Generated files">
    <hr class="generated-files-divider" />
    <h3 class="generated-files-heading">FILES GENERATED</h3>
    <div class="transcript-files">
      <ExperimentFileCard
        v-for="item in files"
        :key="`${item.file.id}:${item.version.n}`"
        :project-id="projectId"
        :file="{ ...item.file, head: item.version }"
        :list="false"
        @open="emit('open-file', item)"
      />
    </div>
  </section>
  <div v-if="active" class="run-line" role="status">
    <RunStatus
      :working="turn.status !== 'queued'"
      :text="
        turn.status === 'queued'
          ? 'Queued'
          : turn.status === 'dispatched'
            ? 'Starting…'
            : 'Working…'
      "
    />
  </div>
  <div
    v-for="failure in props.replies.filter((m) => m.kind === 'failure')"
    :key="failure.id"
    class="run-line run-line--failed"
  >
    {{
      `Run failed${failure.failure_reason || turn.failure_reason ? ` · ${cleanAssistantText(failure.failure_reason || turn.failure_reason || '')}` : ''}${failure.body ? ` — ${cleanAssistantText(failure.body)}` : ''}`
    }}
  </div>
</template>

<style scoped>
.generated-files {
  margin: 16px 0;
}
.generated-files-divider {
  border: 0;
  border-top: 1px solid var(--border);
  margin: 0 0 12px;
}
.generated-files-heading {
  font-size: 11px;
  font-weight: 600;
  color: var(--muted);
  letter-spacing: 0.04em;
  margin: 0 0 8px;
}
.transcript-files {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
}
.transcript-files :deep(.experiment-file-preview) {
  background: var(--fill-preview);
}
.coresearch-run-timeline {
  --step-canvas: var(--surface);
  --step-border: color-mix(in srgb, var(--text) 12%, var(--surface));
  --step-line: color-mix(in srgb, var(--text) 20%, var(--surface));
  --step-quiet: color-mix(in srgb, var(--text) 40%, var(--surface));
  --step-muted: color-mix(in srgb, var(--text) 55%, var(--surface));
  --step-surface: #edeef3;
  --step-hover: color-mix(in srgb, var(--step-surface) 92%, var(--text));
  --step-done: #27a644;
}
:global(:root[data-theme='dark']) .coresearch-run-timeline {
  --step-surface: color-mix(in srgb, var(--surface) 92%, var(--text));
}
</style>
