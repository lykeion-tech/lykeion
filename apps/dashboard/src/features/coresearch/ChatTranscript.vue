<script setup lang="ts">
import { computed } from 'vue';
import type { MessageRow } from './types';
import TranscriptTurn from './TranscriptTurn.vue';
import AssistantMessage from './AssistantMessage.vue';
import CopyButton from './CopyButton.vue';
import TurnRail from './TurnRail.vue';
import RailRow from './RailRow.vue';
const props = defineProps<{ projectId: string; chatId: string; messages: MessageRow[] }>();
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
      :turn="entry.message"
      :replies="entry.replies"
      :settled-replies="
        messages
          .filter((m) => m.kind === 'reply' && m.agent_run_id === entry.message.agent_run_id)
          .map((m) => m.body)
      "
    />
    <div v-else-if="entry.message.kind === 'comment'" class="msg msg--user">
      <span class="type-small text-fg-subtle">Comment</span
      ><AssistantMessage :text="entry.message.body" prose />
      <div class="msg-actions"><CopyButton :text="entry.message.body" /></div>
    </div>
    <div v-else-if="entry.message.kind === 'failure'" class="run-line run-line--failed">
      {{
        `Run failed${entry.message.failure_reason ? ` · ${entry.message.failure_reason}` : ''}${entry.message.body ? ` — ${entry.message.body}` : ''}`
      }}
    </div>
    <div v-else class="turn-reply">
      <TurnRail
        ><RailRow marker="prose"
          ><AssistantMessage class="lyk-reading" :text="entry.message.body" /></RailRow
      ></TurnRail>
      <div class="msg-actions"><CopyButton :text="entry.message.body" /></div>
    </div>
  </template>
</template>
