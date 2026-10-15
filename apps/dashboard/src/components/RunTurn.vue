<script setup lang="ts">
import { computed, defineAsyncComponent, defineComponent, h, ref } from 'vue';
import RunActivity from './RunActivity.vue';
import { transcriptBlocks } from '../lib/run-transcript';
import { useRunMessages } from '../api/queries';
import type { AgentRun } from '../api/types';
import { roleLabel } from '../lib/studies';

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

const props = defineProps<{ run: AgentRun; machine: string }>();
const messages = useRunMessages(() => props.run, true);
const blocks = computed(() => transcriptBlocks(messages.data.value ?? []));
const harness = computed(
  () => ({ claude: 'Claude Code', codex: 'Codex' })[props.run.harness] ?? props.run.harness,
);
const status = computed(() =>
  props.run.status === 'completed'
    ? 'Completed'
    : props.run.status === 'queued' && props.run.queue_reason
      ? `Queued: ${props.run.queue_reason.replace(/_/g, ' ')}`
      : props.run.status[0]!.toUpperCase() + props.run.status.slice(1),
);
const time = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
</script>
<template>
  <div class="who">
    <span class="avatar agent">{{ roleLabel(run.role)[0] }}</span
    ><span>{{ roleLabel(run.role) }}</span
    ><span class="tm"
      >{{ harness }}<template v-if="machine"> on {{ machine }}</template> ·
      {{ time(run.started_at ?? run.created_at) }}</span
    ><span v-if="run.attempt > 1" class="tm">Attempt {{ run.attempt }}</span
    ><span v-if="status" class="chip">{{ status }}</span>
  </div>
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
    <template v-for="block in blocks" :key="block.key">
      <template v-if="block.kind === 'text'">
        <div v-if="formattingFailed" class="recorded-text-fallback">{{ block.text }}</div>
        <RecordedMarkdown v-else :content="block.text" />
      </template>
      <RunActivity v-else :messages="block.messages" />
    </template>
    <RunActivity v-if="messages.data.value?.length" :messages="messages.data.value" history />
    <p v-if="run.error" class="run-error">{{ run.error }}</p>
  </div>
</template>
<style scoped>
.who {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  font-size: 13.5px;
  margin: 8px 0 6px 14px;
}
.tm {
  color: var(--muted);
  font-size: 12.5px;
}
.avatar.agent {
  background: #fff3d6;
  color: #b5841b;
}
.agent-turn {
  margin: 0 0 14px 24px;
  border-left: 1.5px solid var(--border);
  padding-left: 14px;
  font-size: 13.5px;
  overflow-wrap: anywhere;
  min-width: 0;
  line-height: 1.7;
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
}
</style>
