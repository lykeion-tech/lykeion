<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { useMe } from '../../api/queries';
import { keys } from '../../api/keys';
import type { DecisionItem } from '../../api/types';
import AristotleWindow from '../aristotle-window/AristotleWindow.vue';
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
  me = useMe();
const chat = ref(''),
  ai = ref(false),
  draft = ref(blankManualDecisionDraft());
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
const showArchived = ref(false);
const archived = computed(() => !!saved.data.value?.archived_at);
const recent = useInfiniteQuery({
  queryKey: computed(() => [
    ...labKey(workspaceId.value),
    owner.value,
    purpose,
    'drafts',
    showArchived.value,
  ]),
  enabled: computed(() => !!workspaceId.value && !!owner.value),
  initialPageParam: '',
  queryFn: ({ queryKey, pageParam, signal }) =>
    api<{ chats: { id: string; title: string }[]; next_cursor: string }>(
      'GET',
      '/api/lab/chats?purpose=' +
        purpose +
        (queryKey[6] ? '&archived=true' : '') +
        (pageParam ? '&cursor=' + encodeURIComponent(pageParam) : ''),
      undefined,
      undefined,
      { workspace: queryKey[1] as string, signal },
    ),
  getNextPageParam: (last) => last.next_cursor || undefined,
  staleTime: 0,
  gcTime: 0,
  refetchInterval: 15000,
});
const recentDrafts = computed(() =>
  recent.isError.value ? [] : (recent.data.value?.pages.flatMap((page) => page.chats) ?? []),
);
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
    loaded.value = false;
    denied.value = false;
    created.value = false;
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
  if (busy.value || !owner.value) return;
  const visit = scope();
  busy.value = true;
  error.value = '';
  try {
    const result = await api<{ chat: { id: string } }>(
      'POST',
      '/api/lab/chats',
      { purpose },
      undefined,
      { workspace: visit.ws },
    );
    if (!visit.current()) return;
    await router.replace({
      path: '/decisions/new',
      query: { draft: result.chat.id, ...(withAI ? { mode: 'ai' } : {}) },
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
    savedRevision = revision;
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
          snapshot,
          undefined,
          { workspace: visit.ws },
        );
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
function proposal(value: { draft: ManualDecisionDraft; input: ManualDecisionDraft }) {
  if (!loaded.value || created.value || busy.value || archived.value) return;
  edit(mergeManualDecisionDraft(draft.value, value.draft, value.input));
}
async function create() {
  if (
    busy.value ||
    created.value ||
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
    const note = await api<DecisionItem>('POST', '/api/decisions/manual', snapshot, undefined, {
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
  <section class="directory-page manual-create">
    <header class="agent-create-header">
      <RouterLink to="/decisions" class="icon-button" aria-label="Back to decisions"
        ><Icon name="back" :size="16"
      /></RouterLink>
      <div>
        <h1>Create a decision</h1>
        <p>{{ chat ? 'Private note · Only you' : 'Choose a starting point' }}</p>
      </div>
    </header>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <div v-if="!chat" class="agent-create-start">
      <small>Decision creation</small>
      <h2>How would you like to start?</h2>
      <div class="agent-create-options">
        <button class="agent-create-option" :disabled="busy || !owner" @click="start(false)">
          <span class="option-icon"><Icon name="file" :size="18" /></span
          ><strong>Start blank</strong
          ><span>Write a private note, with an optional deadline and study.</span
          ><em>Continue <Icon name="right" :size="12" /></em>
        </button>
        <button class="agent-create-option" :disabled="busy || !owner" @click="start(true)">
          <span class="option-icon"><Icon name="chat" :size="18" /></span
          ><strong>Create with AI</strong
          ><span>Draft an editable note through a conversation with Aristotle.</span
          ><em>Continue <Icon name="right" :size="12" /></em>
        </button>
      </div>
      <section class="drafts">
        <div class="draft-list-heading">
          <h3>{{ showArchived ? 'Archived drafts' : 'Unfinished drafts' }}</h3>
          <button class="button" @click="showArchived = !showArchived">
            {{ showArchived ? 'Show unfinished drafts' : 'Show archived drafts' }}
          </button>
        </div>
        <RouterLink
          v-for="item in recentDrafts"
          :key="item.id"
          :to="{ path: '/decisions/new', query: { draft: item.id, mode: 'ai' } }"
          >{{ item.title || 'Untitled draft' }}</RouterLink
        ><button
          v-if="recent.hasNextPage.value"
          class="button"
          :disabled="recent.isFetching.value"
          @click="recent.fetchNextPage()"
        >
          More drafts
        </button>
      </section>
      <p v-if="recent.isError.value" role="alert">
        Drafts could not be loaded. <button class="button" @click="recent.refetch()">Retry</button>
      </p>
    </div>
    <div v-else class="form-wrap">
      <p v-if="denied || (saved.isError.value && !loaded)" role="alert">
        This draft is unavailable. <button class="button" @click="saved.refetch()">Retry</button>
      </p>
      <template v-else-if="loaded">
        <p v-if="saved.isError.value" role="alert">
          Could not refresh this draft. Your current edits are kept.
          <button class="button" @click="saved.refetch()">Retry refresh</button>
        </p>
        <h2>{{ archived ? 'Archived draft' : 'Review your note' }}</h2>
        <p class="save-state" role="status">
          {{ saveState || 'Your draft is private and saved across visits.' }}
          <button v-if="saveState === 'Not saved' && !archived" class="button" @click="retrySave">
            Retry saving draft
          </button>
        </p>
        <ManualDecisionForm
          :model-value="draft"
          :busy="busy || created"
          :readonly="archived"
          @update:model-value="edit"
          @submit="create"
          ><RouterLink to="/decisions" class="button">Cancel</RouterLink></ManualDecisionForm
        >
        <button v-if="!ai && !created && !archived" class="button draft-with-ai" @click="ai = true">
          Draft with Aristotle
        </button>
        <AristotleWindow
          v-if="ai && !created"
          :key="workspaceId + ':' + owner + ':' + chat"
          :user-id="owner"
          :draft-chat="chat"
          :manual-draft="draft"
          :readonly="archived"
          :submit-manual-question="submitManualQuestion"
          @manual-proposal="proposal"
        />
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
.form-wrap {
  max-width: 680px;
  margin: 48px auto;
  padding-bottom: 120px;
}
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
.draft-with-ai {
  margin-top: 20px;
}
.drafts {
  width: 100%;
  max-width: 768px;
  margin-top: 32px;
  display: grid;
  gap: 12px;
  font-size: 13px;
}
.draft-list-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.drafts h3 {
  font-size: 12px;
  color: var(--muted);
  font-weight: 500;
}
.drafts a:hover {
  text-decoration: underline;
}
.agent-create-option:focus-visible,
.drafts a:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
@media (max-width: 700px) {
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
