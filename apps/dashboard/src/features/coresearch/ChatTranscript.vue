<script setup lang="ts">
import { computed, watch } from 'vue';
import { useQueries } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { filePath, isActiveTurn, keys, useCoresearchFiles } from './api';
import { queuePreview } from './preview-queue';
import { filesForRun, type TranscriptFile } from './transcript-files';
import type { FileVersion } from './types';
import type { MessageRow } from './types';
import TranscriptTurn from './TranscriptTurn.vue';
import AssistantMessage from './AssistantMessage.vue';
import CopyButton from './CopyButton.vue';
import { cleanAssistantText } from './assistant-paths';
import TurnRail from './TurnRail.vue';
import RailRow from './RailRow.vue';
const props = defineProps<{ projectId: string; chatId: string; messages: MessageRow[]; researchId?: string | null }>();
const emit = defineEmits<{ 'open-file': [TranscriptFile] }>();
const files = useCoresearchFiles(() => props.projectId);
// Discover newly saved files while a run is active and once when it settles.
watch(
  () => props.messages.some((m) => m.kind === 'turn' && isActiveTurn(m.status)),
  (active, _wasActive, cleanup) => {
    if (!workspaceId.value || !props.projectId) return;
    if (active || _wasActive) void files.refetch();
    if (active) {
      const timer = setInterval(() => {
        void files.refetch();
      }, 3000);
      cleanup(() => clearInterval(timer));
    }
  },
  { immediate: true },
);
const versions = useQueries({
  queries: computed(() =>
    (files.data.value ?? []).map((file) => {
      const ws = workspaceId.value,
        project = props.projectId;
      return {
        queryKey: [...keys.coresearchVersions(ws, project, file.id), 'transcript', file.head.n],
        enabled:
          !!ws && !!project && props.messages.some((m) => m.kind === 'turn' && !!m.agent_run_id),
        queryFn: ({ signal }: { signal: AbortSignal }) =>
          queuePreview(
            signal,
            async () =>
              (await api<FileVersion[] | null>(
                'GET',
                `${filePath(project, file.id)}/versions`,
                undefined,
                undefined,
                { signal, workspace: ws },
              )) ?? [],
          ),
      };
    }),
  ),
});
const entries = computed(() => {
  const result: { message: MessageRow; replies: MessageRow[] }[] = [];
  for (const message of props.messages) {
    const last = result.at(-1);
    if (
      last?.message.kind === 'turn' &&
      message.agent_run_id === last.message.agent_run_id &&
      ['reply', 'failure'].includes(message.kind)
    )
      last.replies.push(message);
    else result.push({ message, replies: [] });
  }
  return result;
});
</script>
<template>
  <template v-for="entry in entries" :key="entry.message.id">
    <TranscriptTurn
      v-if="entry.message.kind === 'turn'"
      :project-id="projectId"
      :chat-id="chatId"
      :research-id="researchId"
      :turn="entry.message"
      :replies="entry.replies"
      :files="
        filesForRun(
          entry.message.agent_run_id ?? '',
          files.data.value ?? [],
          versions.map((v) => v.data),
        )
      "
      :settled-replies="
        messages
          .filter((m) => m.kind === 'reply' && m.agent_run_id === entry.message.agent_run_id)
          .map((m) => m.body)
      "
      @open-file="emit('open-file', $event)"
    />
    <div v-else-if="entry.message.kind === 'comment'" class="msg msg--user">
      <span class="type-small text-fg-subtle">Comment</span
      ><AssistantMessage :text="entry.message.body" prose />
      <div class="msg-actions"><CopyButton :text="entry.message.body" /></div>
    </div>
    <div v-else-if="entry.message.kind === 'failure'" class="run-line run-line--failed">
      {{
        `Run failed${entry.message.failure_reason ? ` · ${cleanAssistantText(entry.message.failure_reason)}` : ''}${entry.message.body ? ` — ${cleanAssistantText(entry.message.body)}` : ''}`
      }}
    </div>
    <div v-else class="turn-reply">
      <TurnRail
        ><RailRow marker="prose"
          ><AssistantMessage
            class="lyk-reading"
            :text="entry.message.body"
            colleague-links
            redact-local-paths /></RailRow
      ></TurnRail>
      <div class="msg-actions"><CopyButton :text="cleanAssistantText(entry.message.body)" /></div>
    </div>
  </template>
</template>
