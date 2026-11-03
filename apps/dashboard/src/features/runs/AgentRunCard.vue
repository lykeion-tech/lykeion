<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import type { AgentRun, Comment, Runtime } from '../../api/types';
import { useCard, useMembers } from '../../api/queries';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { deliveryLine } from '../studies/comment-delivery';
import { registerDraftVisit } from '../../state/resource-draft';
import { backendLabel } from '../../lib/backends';
import RunTurn from './RunTurn.vue';
import { columnName, roleLabel, systemRun, timeAgo } from '../../lib/studies';
import { agentDisplayName, agentNameWithoutCli } from '../../lib/agent-name';

const props = defineProps<{
  run: AgentRun;
  study: string;
  position: number;
  runtime?: Runtime | null;
  studyId: string;
  replies?: Comment[];
  authorInitial?: string;
}>();
const emit = defineEmits<{ notebook: [run: string] }>();
const active = computed(() => ['dispatched', 'running'].includes(props.run.status));
const open = ref(active.value);
const members = useMembers();
const studyCard = useCard(() => props.studyId);
const client = useQueryClient();
const reply = ref('');
const replyBusy = ref(false);
const replyError = ref('');
registerDraftVisit({
  dirty: () => !!reply.value.trim(),
  discard: () => {
    reply.value = '';
  },
  identity: () => `${workspaceId.value}:${props.studyId}:${props.run.id}`,
});
watch(
  () => [workspaceId.value, props.studyId, props.run.id],
  () => {
    reply.value = '';
    replyError.value = '';
  },
);
async function sendReply() {
  const text = reply.value.trim();
  if (!text || replyBusy.value) return;
  const ws = workspaceId.value,
    study = props.studyId,
    run = props.run.id;
  replyBusy.value = true;
  replyError.value = '';
  try {
    await api(
      'POST',
      `/api/cards/${study}/comments`,
      { body: text, target_run_id: run },
      undefined,
      { workspace: ws },
    );
    if (
      workspaceId.value === ws &&
      props.studyId === study &&
      props.run.id === run &&
      reply.value.trim() === text
    )
      reply.value = '';
    await client.invalidateQueries({ queryKey: keys.comments(ws, study) });
  } catch (error) {
    if (workspaceId.value === ws && props.studyId === study && props.run.id === run)
      replyError.value = (error as Error).message;
  } finally {
    replyBusy.value = false;
  }
}
// Only what the run and its machine's daemon recorded; nothing is filled in.
const environment = computed(() => {
  const r = props.run;
  const machine = props.runtime;
  const ownerId = r.machine_owner_id || machine?.owner_id;
  const owner = members.data.value?.find((m) => m.user_id === ownerId);
  const ownerName = owner?.name || owner?.email || (ownerId ? 'Former member' : '');
  const machineName = machine ? machine.custom_name || machine.device_name : r.machine_name;
  const machineLabel = machineName ? `${machineName}${ownerName ? ` (${ownerName})` : ''}` : '';
  return [
    { key: 'Machine', value: machineLabel },
    { key: 'Runtime', value: backendLabel(r.harness) },
    { key: 'Model', value: r.model },
    { key: 'Stage', value: columnName(r.board_column) },
  ].filter((e) => e.value);
});
const name = computed(() => agentDisplayName(props.run.agent_name || roleLabel(props.run.role)));
function replyStatus(item: Comment) {
  if (item.delivery) return deliveryLine(item.delivery, { working: active.value, who: name.value });
  if (!item.steer_attempt || !active.value || item.steer_attempt !== props.run.attempt)
    return 'Saved as a study comment';
  return props.run.harness === 'codex'
    ? 'Saved as a study comment; live steering is unavailable for Codex'
    : `Waiting to reach ${name.value}`;
}
const state = computed(
  () =>
    ({
      queued: 'Queued',
      dispatched: 'Working',
      running: 'Working',
      completed: 'Done',
      failed: 'Failed',
      cancelled: 'Cancelled',
    })[props.run.status],
);
// The run's hand-in answer, else the latest thing the agent wrote, as plain
// text for a three-line preview. The runs listing carries both, so a card
// never reads the run's messages.
const preview = computed(() => {
  const text = props.run.answer || props.run.latest_text;
  if (text)
    return text
      .replace(/```[\s\S]*?(```|$)/g, ' ')
      .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/[#>*_`|]+/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
  if (props.run.status === 'queued')
    return props.run.queue_reason ? `Queued: ${props.run.queue_reason.replace(/_/g, ' ')}` : '';
  // The objector and the rerun are Lykeion's own runs: their failure is
  // told plainly, never as the harness's raw error.
  if (systemRun(props.run.role))
    return props.run.status === 'failed'
      ? `Did not run${props.run.failure_reason ? ` (${props.run.failure_reason.replace(/_/g, ' ')})` : ''}`
      : '';
  return props.run.error;
});
const lastActive = computed(
  () => props.run.last_activity_at ?? props.run.started_at ?? props.run.created_at,
);
</script>
<template>
  <article class="agent-card" :data-status="run.status">
    <button
      class="run-header"
      :aria-label="`${name} (${state}), Run ${position}`"
      :aria-expanded="open"
      :aria-controls="`run-transcript-${run.id}`"
      @click="open = !open"
    >
      <span class="fold-caret" aria-hidden="true"
        ><Icon :name="open ? 'down' : 'right'" :size="14"
      /></span>
      <span class="avatar agent">
        {{ name[0] }}
        <span v-if="active" class="live-dot" :title="state" />
      </span>
      <span class="run-identity">
        <span class="agent-name">{{
          agentNameWithoutCli(run.agent_name || roleLabel(run.role), run.harness)
        }}</span>
        <span class="run-meta"
          ><template v-if="run.model">{{ run.model }} · </template
          ><time :datetime="lastActive">{{ timeAgo(lastActive) }}</time></span
        >
      </span>
    </button>
    <template v-if="!open">
      <p class="preview">{{ preview }}</p>
      <button class="show-transcript" @click="open = true">▸ show transcript</button>
    </template>
    <div v-else :id="`run-transcript-${run.id}`" class="open-transcript">
      <RunTurn :run="run" />
      <p class="run-environment" aria-label="Run environment">
        <span v-for="e in environment" :key="e.key"
          ><span class="environment-label">{{ e.key }}:</span> {{ e.value }}</span
        >
      </p>
      <p v-if="studyCard.data.value?.machine_notice" class="machine-notice" role="status">
        {{ studyCard.data.value.machine_notice }}
      </p>
      <button class="text-button notebook-link" @click="emit('notebook', run.id)">
        Open in Notebook
      </button>
    </div>
    <div v-if="replies?.length" class="run-replies">
      <div v-for="item in replies" :key="item.id" class="run-reply">
        <span class="avatar reply-avatar">{{
          (members.data.value?.find((m) => m.user_id === item.author_id)?.name ||
            members.data.value?.find((m) => m.user_id === item.author_id)?.email ||
            '?')[0]
        }}</span>
        <div>
          <p>{{ item.body }}</p>
          <small>{{ replyStatus(item) }}</small>
        </div>
      </div>
    </div>
    <form class="reply-composer" :aria-label="`Reply to ${name}`" @submit.prevent="sendReply">
      <span class="avatar reply-avatar" aria-hidden="true">{{ authorInitial || '?' }}</span>
      <textarea
        :value="reply"
        :aria-label="`Reply to ${name}`"
        placeholder="Leave a reply…"
        rows="1"
        @input="reply = ($event.target as HTMLTextAreaElement).value"
        @keydown.meta.enter.prevent="sendReply"
        @keydown.ctrl.enter.prevent="sendReply"
      />
      <button
        type="submit"
        :disabled="replyBusy || !reply.trim()"
        :aria-label="`Send reply to ${name}`"
      >
        <Icon name="up" :size="16" />
      </button>
    </form>
    <p v-if="replyError" class="reply-error" role="alert">{{ replyError }}</p>
    <p v-if="reply.trim() && active && run.harness === 'codex'" class="reply-note">
      Codex cannot receive replies during this run; the reply is saved as a study comment.
    </p>
    <p v-else-if="reply.trim() && run.status !== 'running'" class="reply-note">
      This reply will be saved as a study comment.
    </p>
  </article>
</template>
<style scoped>
.agent-card {
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid var(--card-border);
  border-radius: 8px;
  box-shadow: 0 1px 2px #00000014;
  background: #ffffff16;
  font-size: 14px;
  line-height: 19px;
}
.run-header {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-width: 0;
  text-align: left;
  border-radius: 4px;
}
.run-header:focus-visible,
.show-transcript:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 4px;
}
.fold-caret {
  display: grid;
  place-items: center;
  flex: none;
  width: 14px;
  color: var(--muted);
}
.avatar.agent {
  position: relative;
  flex: none;
  background: #fff3d6;
  color: #b5841b;
}
.live-dot {
  position: absolute;
  bottom: -1px;
  right: -1px;
  width: 8px;
  height: 8px;
  border: 2px solid var(--surface);
  border-radius: 50%;
  background: var(--primary);
}
.run-identity {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.agent-name {
  font-weight: 500;
}
.run-meta {
  color: var(--muted);
  font-size: 12px;
}
.preview {
  margin-top: 12px;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.show-transcript {
  margin-top: 10px;
  color: var(--primary-text);
  font-size: 12px;
}
.show-transcript:hover {
  color: var(--text);
}
.open-transcript {
  margin-top: 20px;
  min-width: 0;
}
.run-environment {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin-top: 18px;
  padding: 12px 0 0 6px;
  border-top: 1px solid var(--border);
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.environment-label {
  color: var(--text);
}
.machine-notice {
  margin-top: 6px;
  color: var(--muted);
  font-size: 12px;
}
.notebook-link {
  margin-top: 12px;
  font-size: 12px;
}
.notebook-link:hover {
  text-decoration: underline;
}
.notebook-link:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 4px;
}
.run-replies {
  border-top: 1px solid var(--border);
  margin: 14px -16px 0;
  padding: 12px 16px 0;
}
.run-reply {
  display: flex;
  gap: 10px;
  margin-bottom: 10px;
  overflow-wrap: anywhere;
}
.run-reply p {
  margin: 0;
  white-space: pre-wrap;
}
.run-reply small,
.reply-note {
  color: var(--muted);
  font-size: 12px;
}
.reply-composer {
  display: flex;
  align-items: center;
  gap: 10px;
  border-top: 1px solid var(--border);
  margin: 14px -16px -14px;
  padding: 10px 16px;
}
.reply-avatar {
  background: var(--input);
  color: var(--text);
}
.reply-composer textarea {
  flex: 1;
  min-width: 0;
  resize: vertical;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text);
  font: inherit;
}
.reply-composer button {
  color: var(--primary-text);
}
.reply-composer button:disabled {
  opacity: 0.45;
}
.reply-error {
  color: var(--error-text);
  font-size: 12px;
  margin-top: 16px;
}
.reply-note {
  margin-top: 18px;
}
</style>
