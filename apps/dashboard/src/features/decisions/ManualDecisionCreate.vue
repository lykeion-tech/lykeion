<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { useMe, useRuntimes } from '../../api/queries';
import { keys } from '../../api/keys';
import type { DecisionItem } from '../../api/types';
import DecisionBuilderConversation from './DecisionBuilderConversation.vue';
import { backendLabel } from '../../lib/backends';
import { labKey } from '../aristotle-window/api';
import ManualDecisionForm from './ManualDecisionForm.vue';
import {
  blankManualDecisionDraft,
  manualDecisionDraftError,
  mergeManualDecisionDraft,
  parseManualDecisionDraft,
  type ManualDecisionDraft,
  type ManualDecisionTurn,
} from './manualDecisionDraft';
const purpose = 'manual_decision_draft';
const route = useRoute(),
  router = useRouter(),
  client = useQueryClient(),
  me = useMe(),
  runtimes = useRuntimes();
const chat = ref(''),
  ai = ref(false),
  draft = ref(blankManualDecisionDraft());
const setup = ref(false), selectedRuntimeID = ref(''), selectedModel = ref('');
const builderPending = ref(false);
const availableRuntimes = computed(() => (runtimes.data.value ?? []).filter((runtime) =>
  runtime.owner_id === me.data.value?.id && runtime.status === 'online' && !runtime.unhealthy_reason &&
  (runtime.harness === 'claude' || runtime.harness === 'codex'),
));
watch(availableRuntimes, (list) => {
  if (!selectedRuntimeID.value && list.length) selectedRuntimeID.value = list[0]!.id;
}, { immediate: true });
const setupRuntime = computed(() => availableRuntimes.value.find((runtime) => runtime.id === selectedRuntimeID.value));
function runtimeLabel(runtime: (typeof availableRuntimes.value)[number]) {
  const name = runtime.custom_name || runtime.device_name;
  return `${name} · ${backendLabel(runtime.harness)}`;
}
watch(selectedRuntimeID, () => { selectedModel.value = ''; });
watch([workspaceId, () => me.data.value?.id], () => {
  setup.value = false;
  selectedRuntimeID.value = '';
  selectedModel.value = '';
  if (availableRuntimes.value.length) selectedRuntimeID.value = availableRuntimes.value[0]!.id;
});
const appliedMessageId = ref<string | null>(null);
let pendingAppliedMessageId: string | null = null;
const busy = ref(false),
  error = ref(''),
  saveState = ref(''),
  loaded = ref(false),
  denied = ref(false),
  created = ref(false);
let revision = 0,
  accessEpoch = 0;
let generation = 0,
  writeQueue: Promise<void> = Promise.resolve();
const owner = computed(() => me.data.value?.id ?? '');
const archived = computed(() => !!saved.data.value?.archived_at);
const saved = useQuery({
  queryKey: computed(() => [
    ...labKey(workspaceId.value),
    owner.value,
    purpose,
    'draft',
    chat.value,
  ]),
  enabled: computed(() => !!chat.value && !!owner.value && !!workspaceId.value),
  queryFn: async ({ queryKey, signal }) => {
    const access = accessEpoch;
    const result = await api<{
      chat_id: string;
      purpose: string;
      draft: ManualDecisionDraft;
      archived_at?: string | null;
      applied_message_id?: string | null;
      runtime_id?: string | null;
    }>(
      'GET',
      `/api/lab/chats/${encodeURIComponent(queryKey[6] as string)}/draft`,
      undefined,
      undefined,
      { workspace: queryKey[1] as string, signal },
    );
    if (access !== accessEpoch) throw new Error('Draft access changed. Retry loading the draft.');
    return result;
  },
  staleTime: 0,
  gcTime: 0,
  refetchInterval: 15000,
});
watch(
  [workspaceId, owner, () => route.query.draft],
  () => {
    generation++;
    revision++;
    chat.value = typeof route.query.draft === 'string' ? route.query.draft : '';
    ai.value = route.query.mode === 'ai';
    draft.value = blankManualDecisionDraft();
    appliedMessageId.value = null;
    pendingAppliedMessageId = null;
    loaded.value = false;
    denied.value = false;
    created.value = false;
    builderPending.value = false;
    busy.value = false;
    error.value = '';
    saveState.value = '';
  },
  { immediate: true, flush: 'sync' },
);
watch(
  [() => saved.data.value, () => saved.isSuccess.value, () => saved.dataUpdatedAt.value],
  ([value, success]) => {
    if (
      !success ||
      loaded.value ||
      !value ||
      value.chat_id !== chat.value ||
      value.purpose !== purpose
    )
      return;
    const valid = parseManualDecisionDraft(value.draft, true);
    if (!valid) {
      error.value = 'This draft could not be loaded.';
      return;
    }
    draft.value = valid;
    appliedMessageId.value = value.applied_message_id ?? null;
    pendingAppliedMessageId = null;
    denied.value = false;
    loaded.value = true;
  },
  { immediate: true },
);
function accessFailure(value: unknown) {
  return (
    !!value &&
    typeof value === 'object' &&
    'status' in value &&
    [401, 403, 404].includes(Number(value.status))
  );
}
function clearPrivateDraft() {
  accessEpoch++;
  generation++;
  revision++;
  loaded.value = false;
  denied.value = true;
  busy.value = false;
  draft.value = blankManualDecisionDraft();
  appliedMessageId.value = null;
  pendingAppliedMessageId = null;
  saveState.value = '';
}
watch(
  () => saved.error.value,
  (value) => {
    if (accessFailure(value)) clearPrivateDraft();
  },
);
onUnmounted(() => generation++);
function scope() {
  const visit = generation,
    ws = workspaceId.value,
    user = owner.value,
    id = chat.value;
  return {
    ws,
    id,
    user,
    access: accessEpoch,
    current: () =>
      generation === visit && workspaceId.value === ws && owner.value === user && chat.value === id,
  };
}
async function start(withAI: boolean) {
  if (busy.value || !owner.value || (withAI && !setupRuntime.value)) return;
  const visit = scope();
  busy.value = true;
  error.value = '';
  try {
    const result = await api<{ chat: { id: string } }>(
      'POST',
      '/api/lab/chats',
      withAI ? { purpose, runtime_id: setupRuntime.value!.id } : { purpose },
      undefined,
      { workspace: visit.ws },
    );
    if (!visit.current()) return;
    await router.replace({
      path: '/decisions/new',
      query: { draft: result.chat.id, ...(withAI ? { mode: 'ai', ...(selectedModel.value ? { model: selectedModel.value } : {}) } : {}) },
    });
    await client.invalidateQueries({ queryKey: labKey(visit.ws) });
  } catch (e) {
    if (visit.current()) error.value = (e as Error).message;
  } finally {
    if (visit.current()) busy.value = false;
  }
}
function save(value = draft.value, afterSave?: () => Promise<unknown>): Promise<void> {
  const visit = scope(),
    snapshot = { ...value },
    savedRevision = revision,
    marker = pendingAppliedMessageId;
  if (!visit.id || !loaded.value || archived.value)
    return Promise.reject(new Error('Load a draft before saving.'));
  const invalid = manualDecisionDraftError(snapshot, true);
  if (invalid) {
    saveState.value = 'Not saved';
    return Promise.reject(new Error(invalid));
  }
  saveState.value = 'Saving draft…';
  const next = writeQueue
    .catch(() => {})
    .then(async () => {
      if (owner.value !== visit.user || visit.access !== accessEpoch)
        throw new Error('Draft access changed.');
      try {
        await api(
          'PUT',
          `/api/lab/chats/${encodeURIComponent(visit.id)}/draft`,
          marker ? { ...snapshot, applied_message_id: marker } : snapshot,
          undefined,
          { workspace: visit.ws },
        );
        if (visit.current() && marker && pendingAppliedMessageId === marker)
          pendingAppliedMessageId = null;
        if (visit.current() && revision === savedRevision) {
          saveState.value = 'Draft saved';
          error.value = '';
        }
      } catch (e) {
        if (visit.current()) {
          if (accessFailure(e)) clearPrivateDraft();
          else if (revision === savedRevision) saveState.value = 'Not saved';
          error.value = (e as Error).message;
        }
        throw e;
      }
      // Question submission also saves its frozen input on the server. Keep it
      // inside this queue so later human edits are persisted after that write.
      if (afterSave) {
        if (!visit.current()) throw new Error('Draft visit changed.');
        try {
          await afterSave();
        } catch (e) {
          if (visit.current() && accessFailure(e)) clearPrivateDraft();
          throw e;
        }
      }
    });
  writeQueue = next;
  return next;
}
function submitManualQuestion(turn: ManualDecisionTurn): Promise<void> {
  const visit = scope();
  return save(turn.current_draft, () =>
    api('POST', `/api/lab/chats/${encodeURIComponent(visit.id)}/questions`, turn, undefined, {
      workspace: visit.ws,
    }),
  );
}
function edit(value: ManualDecisionDraft) {
  revision++;
  draft.value = value;
  void save(value).catch(() => {});
}
function retrySave() {
  void save().catch(() => {});
}
function proposal(value: {
  messageId: string;
  draft: ManualDecisionDraft;
  input: ManualDecisionDraft;
}) {
  if (!loaded.value || created.value || busy.value || archived.value) return;
  if (!value.messageId || value.messageId === appliedMessageId.value) return;
  appliedMessageId.value = value.messageId;
  pendingAppliedMessageId = value.messageId;
  edit(mergeManualDecisionDraft(draft.value, value.draft, value.input));
}
async function create() {
  if (
    busy.value ||
    created.value ||
    (ai.value && builderPending.value) ||
    archived.value ||
    !loaded.value ||
    manualDecisionDraftError(draft.value)
  )
    return;
  const visit = scope(),
    snapshot = { ...draft.value };
  busy.value = true;
  error.value = '';
  try {
    await save(snapshot);
    if (!visit.current()) return;
    const note = await api<DecisionItem>('POST', '/api/decisions/manual', { ...snapshot, draft_chat_id: visit.id }, undefined, {
      workspace: visit.ws,
    });
    if (!visit.current()) return;
    created.value = true;
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.decisionsAll(visit.ws) }),
      client.invalidateQueries({ queryKey: keys.decisionCount(visit.ws) }),
    ]);
    // Retain the conversation. Failure to archive never undoes note creation.
    try {
      await api(
        'POST',
        `/api/lab/chats/${encodeURIComponent(visit.id)}/archive`,
        undefined,
        undefined,
        { workspace: visit.ws },
      );
      void client.invalidateQueries({ queryKey: labKey(visit.ws) });
    } catch {
      /* An unfinished draft may remain listed after a network failure. */
    }
    if (visit.current()) await router.push('/decisions/' + note.id);
  } catch (e) {
    if (visit.current())
      error.value = created.value
        ? 'Your note was created. Open Decisions to view it.'
        : (e as Error).message;
  } finally {
    if (visit.current()) busy.value = false;
  }
}
</script>
<template>
  <section class="directory-page manual-create" :class="{ 'manual-create-studio': chat && ai }">
    <header class="agent-create-header">
      <button v-if="setup && !chat" class="icon-button" aria-label="Back to starting options" @click="setup = false"><Icon name="back" :size="16" /></button>
      <RouterLink
        v-else
        :to="chat ? '/decisions/new' : '/decisions'"
        class="icon-button"
        :aria-label="chat ? 'Back to starting options' : 'Back to decisions'"
        ><Icon name="back" :size="16"
      /></RouterLink>
      <div>
        <h1>Create a decision</h1>
        <p>{{ chat ? 'Private note · Only you' : 'Choose a starting point' }}</p>
      </div>
    </header>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <div v-if="!chat && !setup" class="agent-create-start">
      <small>Decision creation</small>
      <h2>How would you like to start?</h2>
      <div class="agent-create-options">
        <button class="agent-create-option" :disabled="busy || !owner" @click="start(false)">
          <span class="option-icon"><Icon name="file" :size="18" /></span
          ><strong>Start blank</strong
          ><span>Write a private note, with an optional deadline and study.</span
          ><em>Continue <Icon name="right" :size="12" /></em>
        </button>
        <button class="agent-create-option" :disabled="busy || !owner" @click="setup = true">
          <span class="option-icon"><Icon name="chat" :size="18" /></span
          ><strong>Create with AI</strong
          ><span>Draft an editable note through a conversation.</span
          ><em>Continue <Icon name="right" :size="12" /></em>
        </button>
      </div>
    </div>
    <div v-if="!chat && setup" class="builder-setup" role="dialog" aria-label="Choose a machine">
      <h2>Choose a machine</h2>
      <p>Choose the machine and CLI for this conversation. The Decision is not tied to a machine.</p>
      <label>Machine and CLI
        <select v-model="selectedRuntimeID" aria-label="Machine and CLI" :title="setupRuntime ? runtimeLabel(setupRuntime) : undefined">
          <option v-if="!availableRuntimes.length" value="">No available machines</option>
          <option v-for="runtime in availableRuntimes" :key="runtime.id" :value="runtime.id" :aria-label="runtimeLabel(runtime)" :title="runtimeLabel(runtime)">{{ runtimeLabel(runtime) }}</option>
        </select>
      </label>
      <label>Model
        <select v-model="selectedModel" aria-label="Builder model">
          <option value="">Harness default</option>
          <option v-for="model in setupRuntime?.models ?? []" :key="model.value" :value="model.value">{{ model.label }}</option>
        </select>
      </label>
      <div class="setup-actions"><button class="button" @click="setup = false">Cancel</button><button class="button primary" :disabled="busy || !setupRuntime" @click="start(true)">Start conversation</button></div>
    </div>
    <div v-if="chat" :class="ai ? 'studio-wrap' : 'form-wrap'">
      <p v-if="denied || (saved.isError.value && !loaded)" role="alert">
        This draft is unavailable. <button class="button" @click="saved.refetch()">Retry</button>
      </p>
      <template v-else-if="loaded">
        <p v-if="saved.isError.value" role="alert">
          Could not refresh this draft. Your current edits are kept.
          <button class="button" @click="saved.refetch()">Retry refresh</button>
        </p>
        <DecisionBuilderConversation
          v-if="ai && !created"
          :key="workspaceId + ':' + owner + ':' + chat"
          :user-id="owner"
          :draft-chat="chat"
          :runtime="runtimes.data.value?.find((runtime) => runtime.id === saved.data.value?.runtime_id)"
          :model="typeof route.query.model === 'string' ? route.query.model : ''"
          :manual-draft="draft"
          :applied-message-id="appliedMessageId"
          :manual-draft-busy="busy"
          :readonly="archived"
          :submit-manual-question="submitManualQuestion"
          @pending-change="builderPending = $event"
          @manual-proposal="proposal"
        />
        <div class="decision-editor">
        <h2>{{ archived ? 'Archived draft' : 'Save a decision' }}</h2>
        <p class="save-state" role="status">
          {{ saveState || 'Your draft is private and saved across visits.' }}
          <button v-if="saveState === 'Not saved' && !archived" class="button" @click="retrySave">
            Retry saving draft
          </button>
        </p>
        <ManualDecisionForm
          :model-value="draft"
          :busy="busy || created"
          :submit-disabled="ai && builderPending"
          :readonly="archived"
          @update:model-value="edit"
          @submit="create"
          ><RouterLink to="/decisions" class="button">Cancel</RouterLink></ManualDecisionForm
        >
        </div>
      </template>
      <p v-else role="status">Loading draft…</p>
    </div>
  </section>
</template>
<style scoped src="../../ui/directory.css"></style>
<style scoped>
.manual-create {
  color: var(--text);
}
.manual-create-studio { display:flex; flex-direction:column; padding-bottom:0; }
.form-wrap {
  max-width: 680px;
  margin: 48px auto;
  padding-bottom: 120px;
}
.studio-wrap { display:grid; grid-template-columns:minmax(0,1fr) minmax(0,1fr); gap:16px; padding:24px; flex:1; box-sizing:border-box; align-items:stretch; }
.studio-wrap .decision-editor { display:flex; flex-direction:column; border:1px solid var(--border); border-radius:8px; padding:20px; min-width:0; }
.studio-wrap .decision-editor :deep(.manual-form) { flex:1; max-width:none; }
.studio-wrap .decision-editor :deep(.manual-form > label:nth-child(2)) { flex:1; }
.studio-wrap .decision-editor :deep(.manual-form > label:nth-child(2) textarea) { flex:1; min-height:125px; }
.builder-setup { max-width:480px; margin:28px auto; padding:24px; border:1px solid var(--border); border-radius:8px; background:var(--surface); }
.builder-setup h2 { margin:0 0 8px; font-size:20px; }
.builder-setup p { color:var(--muted); font-size:13px; line-height:1.5; }
.builder-setup label { display:flex; flex-direction:column; gap:6px; margin-top:16px; font-size:13px; }
.builder-setup select { width:min(145px, 100%); padding:5px 8px; border:1px solid var(--border); border-radius:6px; color:var(--text); background:var(--surface); }
.setup-actions { display:flex; justify-content:flex-end; gap:8px; margin-top:20px; }
.form-wrap h2 {
  font-size: 24px;
  font-weight: 500;
  margin-bottom: 10px;
}
.save-state {
  color: var(--muted);
  font-size: 12px;
  margin-bottom: 28px;
}
.agent-create-option:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
@media (max-width: 700px) {
  .studio-wrap { grid-template-columns:1fr; padding:16px; }
  .agent-create-start {
    padding-top: 8vh;
  }
  .agent-create-start h2 {
    font-size: 26px;
  }
  .form-wrap {
    margin-top: 28px;
  }
}
</style>
