<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { lazyRecordedMarkdown } from '../../ui/lazy-recorded-markdown';
import { FileText, Square } from '@lucide/vue';
import Modal from '../../ui/Modal.vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import AvatarDisplay from '../../ui/AvatarDisplay.vue';
import type { AgentDefinition, AgentRun, Comment, Runtime } from '../../api/types';
import { useCard, useMembers, useRunMessages, uploadAttachment } from '../../api/queries';
import { useAttachments } from '../../lib/attachments';
import CommentFiles from '../studies/CommentFiles.vue';
import CommentFilePicker from '../studies/CommentFilePicker.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { deliveryLine } from '../studies/comment-delivery';
import { registerDraftVisit } from '../../state/resource-draft';
import RunTurn from './RunTurn.vue';
import RunStatus from './RunStatus.vue';
import RunReactions from './RunReactions.vue';
const RecordedMarkdown = lazyRecordedMarkdown();
import { runLabel, timeAgo } from '../../lib/studies';
import { agentDisplayName, agentNameWithoutCli } from '../../lib/agent-name';

const props = defineProps<{
  run: AgentRun;
  agent?: AgentDefinition | null;
  study: string;
  position: number;
  runtime?: Runtime | null;
  studyId: string;
  replies?: Comment[];
  authorInitial?: string;
  authorId?: string;
  cancellingComment?: string;
}>();
const emit = defineEmits<{ cancelComment: [id: string]; openOutput: [key: string] }>();
const active = computed(() => ['dispatched', 'running'].includes(props.run.status));
const stoppable = computed(() => ['queued', 'dispatched', 'running'].includes(props.run.status));
const logOpen = ref(false);
const stopping = ref(false);
const stopError = ref('');
const finalMessages = useRunMessages(
  () => props.run,
  () => props.run.status === 'completed' && !props.run.answer,
);
// A report run hands in the study's report as concepts, facts and links, not
// as prose, so its card says what it wrote and where to read it.
const reportSummary = computed(() => {
  const hand = props.run.hand_in;
  if (props.run.kind !== 'report' || props.run.status !== 'completed' || !hand) return null;
  if (hand.status !== 'done') {
    const reason = typeof hand.reason === 'string' ? hand.reason.trim() : '';
    return {
      done: false,
      text: `Couldn't write the study report: ${reason || 'no reason was given.'}`,
    };
  }
  const count = (key: string, one: string, many: string) => {
    const n = Array.isArray(hand[key]) ? hand[key].length : 0;
    return `${n} ${n === 1 ? one : many}`;
  };
  return {
    done: true,
    text: `Wrote the study report: ${count('concepts', 'concept', 'concepts')}, ${count('facts', 'fact', 'facts')} and ${count('links', 'link', 'links')}.`,
  };
});
const finalAnswer = computed(() => {
  if (props.run.status !== 'completed') return '';
  if (props.run.answer) return props.run.answer;
  const messages = (finalMessages.data.value ?? []).filter((m) => m.attempt === props.run.attempt);
  const marked = [...messages]
    .reverse()
    .find((m) => m.type === 'status' && m.tool === 'lykeion.final_output');
  if (
    marked &&
    marked.content &&
    typeof marked.content === 'object' &&
    !Array.isArray(marked.content)
  ) {
    const text = (marked.content as Record<string, unknown>).text;
    return typeof text === 'string' ? text : '';
  }
  // Codex records every agent_message as text, and its Output is the last one.
  // A completed attempt was settled only after its messages were acknowledged.
  if (props.run.harness === 'codex') {
    const text = [...messages]
      .reverse()
      .find((m) => m.type === 'text' && typeof m.content === 'string');
    return text ? String(text.content) : '';
  }
  return '';
});
async function stopRun() {
  if (!stoppable.value || stopping.value) return;
  const ws = workspaceId.value,
    study = props.studyId,
    run = props.run.id;
  stopping.value = true;
  stopError.value = '';
  try {
    await api('POST', `/api/agent-runs/${run}/cancel`, undefined, undefined, { workspace: ws });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.runs(ws, study) }),
      client.invalidateQueries({ queryKey: keys.card(ws, study) }),
      client.invalidateQueries({ queryKey: keys.cards(ws) }),
    ]);
  } catch (e) {
    if (ws === workspaceId.value && study === props.studyId && run === props.run.id)
      stopError.value = (e as Error).message;
  } finally {
    stopping.value = false;
  }
}
const members = useMembers();
const studyCard = useCard(() => props.studyId);
const client = useQueryClient();
const reply = ref('');
const replyFiles = useAttachments(uploadAttachment);
const replyBusy = ref(false);
const replyError = ref('');
let replyVisit = 0;
registerDraftVisit({
  dirty: () => !!reply.value.trim() || replyFiles.items.value.length > 0,
  discard: () => {
    reply.value = '';
    replyFiles.clear();
  },
  identity: () => `${workspaceId.value}:${props.studyId}:${props.run.id}`,
});
watch(
  () => [workspaceId.value, props.studyId, props.run.id],
  () => {
    replyVisit++;
    reply.value = '';
    replyFiles.clear();
    replyBusy.value = false;
    replyError.value = '';
  },
  { flush: 'sync' },
);
async function sendReply() {
  const text = reply.value.trim();
  if (
    (!text && !replyFiles.ids.value.length) ||
    replyBusy.value ||
    replyFiles.uploading.value ||
    replyFiles.failed.value
  )
    return;
  const attachmentIds = [...replyFiles.ids.value];
  const visit = replyVisit;
  const ws = workspaceId.value,
    study = props.studyId,
    run = props.run.id;
  replyBusy.value = true;
  replyError.value = '';
  try {
    await api(
      'POST',
      `/api/cards/${study}/comments`,
      {
        body: text,
        target_run_id: run,
        ...(attachmentIds.length ? { attachment_ids: attachmentIds } : {}),
      },
      undefined,
      { workspace: ws },
    );
    if (
      replyVisit === visit &&
      workspaceId.value === ws &&
      props.studyId === study &&
      props.run.id === run &&
      reply.value.trim() === text
    )
      reply.value = '';
    if (
      replyVisit === visit &&
      workspaceId.value === ws &&
      props.studyId === study &&
      props.run.id === run
    )
      replyFiles.clear();
    await client.invalidateQueries({ queryKey: keys.comments(ws, study) });
  } catch (error) {
    if (
      replyVisit === visit &&
      workspaceId.value === ws &&
      props.studyId === study &&
      props.run.id === run
    )
      replyError.value = (error as Error).message;
  } finally {
    if (replyVisit === visit) replyBusy.value = false;
  }
}
const name = computed(() => agentDisplayName(runLabel(props.run)));
function replyAuthor(item: Comment) {
  const member = members.data.value?.find((m) => m.user_id === item.author_id);
  return member?.name || member?.email || 'Unknown colleague';
}
function replyStatus(item: Comment) {
  if (item.attachments?.length && !item.delivery)
    return deliveryLine(null, {
      working: active.value,
      reply: true,
      files: true,
      who: name.value,
      cancelled: item.cancelled,
    });
  if (item.delivery) return deliveryLine(item.delivery, { working: active.value, who: name.value });
  if (!item.steer_attempt || !active.value || item.steer_attempt !== props.run.attempt) return '';
  return props.run.harness === 'codex'
    ? 'Live steering is unavailable for Codex'
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
const lastActive = computed(
  () => props.run.last_activity_at ?? props.run.started_at ?? props.run.created_at,
);
</script>
<template>
  <section class="run-card-group">
    <article class="agent-card" :data-status="run.status">
      <div class="run-header-row">
        <div class="run-header" :aria-label="`${name} (${state}), Run ${position}`">
          <RouterLink
            v-if="run.agent_id && agent?.id === run.agent_id"
            class="avatar agent agent-profile-link"
            :to="`/agents/${encodeURIComponent(run.agent_id)}`"
            :aria-label="`Open ${name}’s agent details`"
          >
            <AvatarDisplay v-if="agent.avatar_url" :value="agent.avatar_url.slice(6)" :size="16" />
            <template v-else>{{ name[0] }}</template>
            <span v-if="active" class="live-dot" :title="state" />
          </RouterLink>
          <span v-else class="avatar agent">
            {{ name[0] }}
            <span v-if="active" class="live-dot" :title="state" />
          </span>
          <span class="run-identity">
            <span class="agent-name">{{ agentNameWithoutCli(runLabel(run), run.harness) }}</span>
            <span class="run-meta"
              ><time :datetime="lastActive">{{ timeAgo(lastActive) }}</time></span
            >
          </span>
        </div>
        <button
          type="button"
          class="header-action full-log"
          aria-label="Open full log"
          title="Open full log"
          @click="logOpen = true"
        >
          <FileText :size="15" /></button
        ><button
          v-if="stoppable"
          type="button"
          class="header-action stop-run"
          :aria-label="`Stop ${name} run`"
          title="Stop run"
          :disabled="stopping"
          @click="stopRun"
        >
          <Square :size="14" /></button
        ><RunReactions :study-id="studyId" :run-id="run.id" />
      </div>
      <div :id="`run-transcript-${run.id}`" class="run-result">
        <RecordedMarkdown
          v-if="finalAnswer"
          class="lyk-reading"
          local-files
          :content="finalAnswer"
        />
        <p v-else-if="reportSummary">
          {{ reportSummary.text }}
          <RouterLink
            v-if="reportSummary.done"
            :to="`/decisions/report/${encodeURIComponent(studyId)}`"
            >Open the report</RouterLink
          >
        </p>
        <p
          v-else-if="run.status === 'completed' && finalMessages.isPending.value && run.attempt > 0"
        >
          Loading final message…
        </p>
        <p v-else-if="run.status === 'completed' && finalMessages.isError.value" role="alert">
          Could not load the final message.
          <button type="button" @click="finalMessages.refetch()">Retry</button>
        </p>
        <p v-else-if="run.status === 'completed'">
          Final message unavailable. Open the full log to inspect the run.
        </p>
        <RunStatus v-else-if="active" text="Working…" working />
        <p v-else-if="run.status === 'failed'">
          {{
            run.failure_reason === 'runtime_offline'
              ? 'Did not run (runtime offline)'
              : 'Run failed. Open the full log for details.'
          }}
        </p>
        <p v-if="studyCard.data.value?.machine_notice" class="machine-notice" role="status">
          {{ studyCard.data.value.machine_notice }}
        </p>
      </div>
      <p v-if="stopError" class="reply-error" role="alert">{{ stopError }}</p>
      <Modal v-if="logOpen" title="Full log" wide @close="logOpen = false">
        <div class="log-dialog">
          <header>
            <h2>Full log</h2>
            <button type="button" aria-label="Close full log" @click="logOpen = false">
              Close
            </button>
          </header>
          <RunTurn
            :run="run"
            :study="studyCard.data.value"
            @open-output="
              logOpen = false;
              emit('openOutput', $event);
            "
          />
        </div>
      </Modal>
      <div v-if="replies?.length" class="run-replies">
        <div v-for="item in replies" :key="item.id" class="run-reply">
          <span class="avatar reply-avatar">{{ replyAuthor(item)[0] }}</span>
          <div class="reply-content">
            <div class="reply-heading">
              <b>{{ replyAuthor(item) }}</b>
              <time
                :datetime="item.created_at"
                :title="new Date(item.created_at).toLocaleString()"
                >{{ timeAgo(item.created_at) }}</time
              >
            </div>
            <p v-if="item.body">{{ item.body }}</p>
            <CommentFiles :comment="item" :study-id="studyId" />
            <small v-if="replyStatus(item)">{{ replyStatus(item) }}</small>
          </div>
          <button
            v-if="item.author_id === authorId"
            type="button"
            class="reply-remove"
            aria-label="Remove comment"
            :title="item.delivery ? 'Remove comment (already delivered)' : 'Remove comment'"
            :disabled="!!cancellingComment"
            @click="emit('cancelComment', item.id)"
          >
            <Icon name="trash" :size="15" />
          </button>
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
        <CommentFilePicker
          :items="replyFiles.items.value"
          :error="replyFiles.error.value"
          :disabled="replyBusy"
          @pick="replyFiles.add"
          @remove="replyFiles.remove"
        />
        <button
          type="submit"
          class="round-send reply-send"
          :disabled="
            replyBusy ||
            replyFiles.uploading.value ||
            replyFiles.failed.value ||
            (!reply.trim() && !replyFiles.ids.value.length)
          "
          :aria-label="`Send reply to ${name}`"
        >
          <Icon name="up" :size="18" />
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
  </section>
</template>
<style scoped>
:deep(.step-heading .step-chevron) {
  display: none;
}
:deep(.step-heading) {
  padding-inline: 0;
}
:deep(.step-body) {
  margin-left: 22px;
}
:deep(.activity-steps) {
  padding-left: 0;
  margin-left: 0;
}
.agent-card {
  margin: 0;
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
  flex: 1;
  min-width: 0;
  text-align: left;
}
.run-header-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.full-log,
.stop-run,
:deep(.reaction-action) {
  opacity: 0;
}
.agent-card:hover .full-log,
.agent-card:focus-within .full-log,
.agent-card:hover .stop-run,
.agent-card:focus-within .stop-run,
.agent-card:hover :deep(.reaction-action),
.agent-card:focus-within :deep(.reaction-action) {
  opacity: 1;
}
@media (hover: none) {
  .full-log,
  .stop-run,
  :deep(.reaction-action) {
    opacity: 1;
  }
}
.avatar.agent {
  position: relative;
  flex: none;
}
.agent-profile-link {
  color: inherit;
  text-decoration: none;
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
  font-size: 13px;
}
.run-result {
  margin: 12px 0 0 34px;
  min-width: 0;
  font-size: 14px;
  line-height: 1.5;
}
.header-action {
  display: inline-grid;
  place-items: center;
  flex: none;
  width: 26px;
  height: 26px;
  color: var(--muted);
  border-radius: 4px;
}
.header-action:hover {
  color: var(--text);
  background: var(--hover);
}

.log-dialog {
  padding: 20px;
  max-height: 85vh;
  overflow: auto;
}
.log-dialog > header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.machine-notice {
  margin-top: 6px;
  color: var(--muted);
  font-size: 13px;
}
.run-replies {
  margin: 14px -16px 0;
}
.run-reply {
  display: flex;
  gap: 10px;
  padding: 14px 16px;
  border-top: 1px solid var(--border);
  overflow-wrap: anywhere;
}
.run-replies + .reply-composer {
  margin-top: 0;
}
.reply-content {
  min-width: 0;
}
.reply-heading {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 3px;
  font-size: 13px;
}
.reply-heading time {
  color: var(--muted);
}
.run-reply p {
  margin: 0;
  white-space: pre-wrap;
}
.run-reply small,
.reply-note {
  color: var(--muted);
  font-size: 13px;
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
  resize: none;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text);
  font: inherit;
}
.reply-composer .reply-send {
  width: 26px;
  height: 26px;
  padding: 0;
  color: var(--on-primary);
}
.reply-remove {
  display: inline-grid;
  place-items: center;
  flex: none;
  margin-left: auto;
  width: 24px;
  height: 24px;
  padding: 0;
  border-radius: 50%;
  color: var(--muted);
}
.reply-remove:hover,
.reply-remove:focus-visible {
  background: var(--hover);
  color: var(--text);
}
.reply-composer button:disabled {
  opacity: 0.45;
}
.reply-error {
  color: var(--error-text);
  font-size: 13px;
  margin-top: 16px;
}
.reply-note {
  margin-top: 18px;
}
</style>
