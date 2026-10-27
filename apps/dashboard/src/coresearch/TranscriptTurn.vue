<script setup lang="ts">
import { computed } from 'vue';
import { activitySteps, transcriptBlocks } from '../lib/run-transcript';
import { isActiveTurn, useCoresearchSteps } from './api';
import type { MessageRow } from './types';
import AssistantMessage from './AssistantMessage.vue';
import CopyButton from './CopyButton.vue';
import TurnRail from './TurnRail.vue';
import RailRow from './RailRow.vue';
import ToolStepGroup from './ToolStepGroup.vue';
const props = defineProps<{
  projectId: string;
  chatId: string;
  turn: MessageRow;
  replies: MessageRow[];
  settledReplies?: string[];
}>();
const active = computed(() => isActiveTurn(props.turn.status));
const steps = useCoresearchSteps(
  () => props.projectId,
  () => props.chatId,
  () => props.turn.agent_run_id ?? '',
  () => active.value,
);
const replies = computed(() => props.replies.filter((m) => m.kind === 'reply'));
const blocks = computed(() =>
  transcriptBlocks(
    (steps.data.value ?? []).map((m) =>
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
    <TurnRail
      ><template v-for="block in blocks" :key="block.key"
        ><RailRow v-if="block.kind === 'text'" marker="prose"
          ><div
            :class="
              thinkingKeys.has(block.key)
                ? 'turn-block turn-block--thinking'
                : 'turn-block turn-block--interim'
            "
          >
            <AssistantMessage :text="block.text" :live="active" /></div></RailRow
        ><ToolStepGroup v-else :steps="activitySteps(block.messages)" :active="active" /></template
      ><RailRow v-for="reply in replies" :key="reply.id" marker="prose"
        ><AssistantMessage :text="reply.body" /></RailRow
    ></TurnRail>
    <div v-if="copy" class="msg-actions"><CopyButton :text="copy" /></div>
  </div>
  <div v-if="active" class="run-line" role="status">
    {{
      turn.status === 'queued' ? 'Queued' : turn.status === 'dispatched' ? 'Starting…' : 'Working…'
    }}
  </div>
  <div
    v-for="failure in props.replies.filter((m) => m.kind === 'failure')"
    :key="failure.id"
    class="run-line run-line--failed"
  >
    {{
      `Run failed${failure.failure_reason || turn.failure_reason ? ` · ${failure.failure_reason || turn.failure_reason}` : ''}${failure.body ? ` — ${failure.body}` : ''}`
    }}
  </div>
</template>
