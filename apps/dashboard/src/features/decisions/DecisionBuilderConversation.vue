<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../../api/client';
import { workspaceId } from '../../api/session';
import type { Runtime } from '../../api/types';
import { labKey, useMessages } from '../aristotle-window/api';
import LabAnswer from '../aristotle-window/LabAnswer.vue';
import RunStatus from '../runs/RunStatus.vue';
import { parseManualDecisionProposal, type ManualDecisionDraft, type ManualDecisionTurn } from './manualDecisionDraft';

const props = defineProps<{
  userId: string;
  draftChat: string;
  manualDraft: ManualDecisionDraft;
  appliedMessageId: string | null;
  manualDraftBusy: boolean;
  readonly: boolean;
  runtime?: Runtime;
  model: string;
  submitManualQuestion: (turn: ManualDecisionTurn) => Promise<void>;
}>();
const emit = defineEmits<{ manualProposal: [value: { messageId: string; draft: ManualDecisionDraft; input: ManualDecisionDraft }] }>();
const client = useQueryClient();
const open = ref(true), chat = computed(() => props.draftChat);
const messages = useMessages(() => props.userId, open, chat, () => 'manual_decision_draft');
const turns = computed(() => messages.isError.value ? [] : (messages.data.value?.pages.slice().reverse().flatMap((page) => page.messages) ?? []));
const pending = computed(() => !!messages.data.value?.pages[0]?.pending);
const online = computed(() => !!props.runtime && props.runtime.owner_id === props.userId && props.runtime.status === 'online' && !props.runtime.unhealthy_reason);
const input = ref(''), sending = ref(false), cancelling = ref(false), error = ref('');
const handled = new Set<string>();
let retry: { key: string; id: string } | undefined;
watch([turns, () => messages.isSuccess.value, () => messages.isFetching.value, () => props.appliedMessageId, () => props.manualDraftBusy], ([list, success]) => {
  if (props.readonly || props.manualDraftBusy || !success) return;
  for (let i = list.length - 1; i >= 0; i--) {
    const message = list[i]!;
    if (message.id === props.appliedMessageId) return;
    if (message.kind !== 'answer') continue;
    const proposal = parseManualDecisionProposal(message.proposal);
    if (!proposal) continue;
    if (handled.has(message.id)) return;
    const question = message.agent_run_id && list.find((item) => item.kind === 'question' && item.agent_run_id === message.agent_run_id);
    if (question && question.current_draft) {
      handled.add(message.id);
      emit('manualProposal', { messageId: message.id, draft: proposal.draft, input: question.current_draft });
    } else if (messages.hasNextPage.value && !messages.isFetching.value) void messages.fetchNextPage();
    return;
  }
  if (messages.hasNextPage.value && !messages.isFetching.value) void messages.fetchNextPage();
}, { immediate: true });
async function send() {
  const text = input.value.trim();
  if (!text || sending.value || pending.value || props.readonly || !props.runtime || !online.value) return;
  if ([...text].length > 8000) { error.value = 'Questions can contain up to 8,000 characters.'; return; }
  const ws = workspaceId.value;
  const turn = { user_request: text, current_draft: { ...props.manualDraft }, harness: props.runtime.harness as 'claude' | 'codex', model: props.model };
  const key = JSON.stringify(turn);
  if (retry?.key !== key) retry = { key, id: crypto.randomUUID() };
  sending.value = true; error.value = '';
  try {
    await props.submitManualQuestion({ ...turn, request_id: retry.id });
    retry = undefined;
    if (input.value.trim() === text) input.value = '';
    await client.invalidateQueries({ queryKey: labKey(ws) });
  } catch (e) {
    error.value = e instanceof ApiError && e.status === 409 && e.body.code === 'no_machine'
      ? 'Your selected machine is offline. Your draft is saved.' : (e as Error).message;
  } finally { sending.value = false; }
}
async function stop() {
  const question = [...turns.value].reverse().find((item) => item.kind === 'question');
  if (!question || cancelling.value || !pending.value) return;
  cancelling.value = true; error.value = '';
  const ws = workspaceId.value;
  try {
    await api('POST', `/api/lab/chats/${encodeURIComponent(props.draftChat)}/questions/${encodeURIComponent(question.id)}/cancel`, undefined, undefined, { workspace: ws });
    await client.invalidateQueries({ queryKey: labKey(ws) });
  } catch (e) { error.value = (e as Error).message; }
  finally { cancelling.value = false; }
}
</script>
<template>
  <section class="builder-conversation" aria-label="Decision builder conversation">
    <header><h2>Decision builder</h2><p>Describe what you need to decide. Review the note before creating it.</p></header>
    <div class="conversation-log" aria-live="polite">
      <button v-if="messages.hasNextPage.value" class="button" @click="messages.fetchNextPage()">Earlier messages</button>
      <p v-if="messages.isError.value" role="alert">Could not load this conversation. <button class="button" @click="messages.refetch()">Retry</button></p>
      <p v-else-if="!turns.length" class="empty">What would you like to decide?</p>
      <template v-for="message in turns" :key="message.id">
        <div v-if="message.kind === 'question'" class="question">{{ message.body }}</div>
        <template v-else-if="message.kind === 'answer'">
          <p v-if="parseManualDecisionProposal(message.proposal)" class="proposal">{{ parseManualDecisionProposal(message.proposal)?.message }}</p>
          <LabAnswer :message="message" :evidence="true" />
        </template>
        <p v-else class="failure">{{ message.body }}</p>
      </template>
      <div v-if="pending" role="status"><RunStatus text="Working…" working /></div>
    </div>
    <p v-if="!online && !readonly" class="notice" role="status">Your selected machine is offline. Your draft is saved.</p>
    <p v-if="error" class="notice" role="alert">{{ error }}</p>
    <form v-if="!readonly" class="composer" @submit.prevent="send">
      <textarea :value="input" aria-label="Describe your decision" placeholder="Describe the decision you need to make…" rows="2" :disabled="sending" @input="input = ($event.target as HTMLTextAreaElement).value" />
      <button v-if="pending" type="button" class="button" :disabled="cancelling" @click="stop">Stop</button>
      <button v-else type="submit" class="button primary" :disabled="sending || !input.trim() || !online">Send</button>
    </form>
  </section>
</template>
<style scoped>
.builder-conversation { display:flex; flex-direction:column; min-height:520px; border:1px solid var(--border); border-radius:8px; background:var(--surface); overflow:hidden; }
header { padding:20px; border-bottom:1px solid var(--border); }
h2 { font-size:16px; margin:0 0 5px; }
header p,.notice,.empty { color:var(--muted); font-size:12px; margin:0; line-height:1.5; }
.conversation-log { flex:1; min-height:250px; max-height:55vh; overflow:auto; display:flex; flex-direction:column; gap:18px; padding:20px; font-size:13px; }
.conversation-log > * { flex:none; }
.question { align-self:flex-end; max-width:85%; padding:8px 12px; border-radius:12px; background:var(--input); white-space:pre-wrap; overflow-wrap:anywhere; }
.proposal,.failure { margin:0; white-space:pre-wrap; overflow-wrap:anywhere; }
.notice { padding:8px 20px; }
.composer { display:flex; gap:8px; align-items:flex-end; padding:14px; border-top:1px solid var(--border); }
textarea { flex:1; min-width:0; resize:vertical; padding:9px; color:var(--text); background:var(--surface); border:1px solid var(--border); border-radius:6px; font:inherit; }
</style>
