<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import {
  useAristotle,
  acceptanceRequest,
  conversationKey,
  defaultStarters,
  restoreConversation,
  type AcceptanceRequest,
  type Conversation,
} from './aristotle';
import { readText, writeText } from '../storage';
import { workspaceId } from '../api/session';
import { useMe, useRuntimes } from '../api/queries';
import { api } from '../api/client';
import { guardResourceDrafts } from '../state/resource-draft';
import { PANE_TABS } from './pane-tabs';
import type { FileRow } from './types';
import RightPaneTabs from './RightPaneTabs.vue';
import NotebookPane from './NotebookPane.vue';
import FilesPane from './FilesPane.vue';
import ComputePane from './ComputePane.vue';
import SidebarFiles from './SidebarFiles.vue';
import {
  chatPath,
  isActiveTurn,
  ownOnlineClis,
  useCoresearchProject,
  useCoresearchChats,
  useCoresearchFiles,
  useCoresearchMessages,
} from './api';
import Aristotle from '../components/Aristotle.vue';
import Icon from '../components/Icon.vue';
import ChatTranscript from './ChatTranscript.vue';
import AristotleComposer from './AristotleComposer.vue';
import './l2.css';
const route = useRoute(),
  router = useRouter(),
  client = useQueryClient();
const state = useAristotle(),
  me = useMe(),
  runtimes = useRuntimes();
const isDraft = computed(() => route.path === '/coresearch/new');
const conversation = computed(() =>
  isDraft.value
    ? undefined
    : state.conversations.data.value
        ?.filter((c): c is Conversation => !c.shared)
        .find(
          (c) =>
            c.id === (route.params.conversationId || state.bootstrap.data.value?.conversation.id),
        ),
);
const agent = computed(() => state.agent.data.value);
const starters = computed(() =>
  agent.value?.conversation_starters?.length ? agent.value.conversation_starters : defaultStarters,
);
const messages = useCoresearchMessages(
  () => conversation.value?.project_id ?? '',
  () => conversation.value?.chat_id ?? '',
);
const experiment = computed(
  () =>
    !!conversation.value &&
    !isDraft.value &&
    !conversation.value.is_welcome &&
    conversation.value.mode !== 'cowrite',
);
const activePane = ref('notebook'),
  paneMode = ref<'closed' | 'split' | 'focus'>('closed'),
  activeFile = ref<FileRow>();
const inspectorProject = computed(() =>
  experiment.value && paneMode.value !== 'closed' ? (conversation.value?.project_id ?? '') : '',
);
const project = useCoresearchProject(() => inspectorProject.value),
  chats = useCoresearchChats(() => inspectorProject.value),
  files = useCoresearchFiles(() => inspectorProject.value);
const chat = computed(() => chats.data.value?.find((c) => c.id === conversation.value?.chat_id));
watch(
  () => [workspaceId.value, conversation.value?.id],
  () => {
    paneMode.value = 'closed';
    activePane.value = 'notebook';
    activeFile.value = undefined;
  },
);
async function selectPane(id: string) {
  if (activePane.value === 'files' && !(await guardResourceDrafts())) return;
  if (id === 'files') activeFile.value = undefined;
  activePane.value = id;
}
async function closePane() {
  if (activePane.value === 'files' && !(await guardResourceDrafts())) return;
  paneMode.value = 'closed';
}
async function openPane() {
  await selectPane('notebook');
  if (activePane.value === 'notebook') paneMode.value = 'split';
}
async function openFile(file: FileRow) {
  if (!(await guardResourceDrafts())) return;
  activeFile.value = file;
}
const historyUnavailable = computed(
  () =>
    !isDraft.value && !!conversation.value && (messages.isPending.value || !!messages.error.value),
);
const activeRun = computed(
  () =>
    messages.data.value?.find((m) => m.kind === 'turn' && isActiveTurn(m.status))?.agent_run_id ??
    undefined,
);
const harness = computed(
  () => conversation.value?.snapshot_harness ?? agent.value?.harness ?? 'claude',
);
const blocker = computed(() =>
  ownOnlineClis(runtimes.data.value ?? [], me.data.value?.id).includes(harness.value)
    ? undefined
    : `No available own machine with ${harness.value}; connect a machine in Settings.`,
);
const draft = ref(''),
  busy = ref(false),
  error = ref('');
let pending: AcceptanceRequest | null = null;
let viewGeneration = 0;
onUnmounted(() => {
  viewGeneration++;
});
watch(
  draft,
  (value) => {
    if (pending && value.trim() !== pending.body) pending = null;
  },
  { flush: 'sync' },
);
const lastKey = (scope: string) => `lykeion.aristotle.last:${scope}`;
watch(
  [state.scope, () => route.path],
  () => {
    viewGeneration++;
    draft.value = '';
    error.value = '';
    busy.value = false;
    pending = null;
  },
  { flush: 'sync' },
);
watch(
  [() => state.conversations.data.value, () => route.path],
  ([list]) => {
    if (!list?.length) return;
    if (route.path === '/coresearch') {
      const owned = list.filter((c): c is Conversation => !c.shared);
      const id = restoreConversation(owned, readText(lastKey(state.scope.value)) ?? null);
      if (id) void router.replace(`/coresearch/conversations/${id}`);
    } else if (route.params.conversationId) {
      const selected = list
        .filter((c): c is Conversation => !c.shared)
        .find((c) => c.id === route.params.conversationId);
      if (selected) writeText(lastKey(state.scope.value), selected.id);
      else {
        const welcome = list.find((c) => c.is_welcome);
        if (welcome) void router.replace(`/coresearch/conversations/${welcome.id}`);
      }
    }
  },
  { immediate: true },
);
async function send(prompt?: string) {
  if (prompt) draft.value = prompt;
  if (
    !draft.value.trim() ||
    busy.value ||
    historyUnavailable.value ||
    blocker.value ||
    activeRun.value ||
    !agent.value
  )
    return;
  const ws = workspaceId.value,
    member = state.member.value,
    scope = state.scope.value,
    target = conversation.value?.id ?? '',
    path = route.path;
  const generation = ++viewGeneration;
  const request = acceptanceRequest(pending, draft.value.trim(), target);
  pending = request;
  busy.value = true;
  error.value = '';
  const current = () =>
    generation === viewGeneration && state.scope.value === scope && route.path === path;
  try {
    const accepted = await api<{ conversation: Conversation; run: { id: string } }>(
      'POST',
      target ? `/api/coresearch/conversations/${target}/turns` : '/api/coresearch/conversations',
      { body: request.body, request_id: request.request_id },
      undefined,
      { workspace: ws },
    );
    writeText(
      `lykeion.aristotle.accepted:${scope}`,
      JSON.stringify({
        conversation: accepted.conversation.id,
        project: accepted.conversation.project_id,
        chat: accepted.conversation.chat_id,
        run: accepted.run.id,
      }),
    );
    writeText(lastKey(scope), accepted.conversation.id);
    await client.invalidateQueries({ queryKey: conversationKey(ws, member) });
    await client.invalidateQueries({
      queryKey: ['ws', ws, 'coresearch', accepted.conversation.project_id],
    });
    if (current()) {
      draft.value = '';
      pending = null;
      await router.push(`/coresearch/conversations/${accepted.conversation.id}`);
    }
  } catch (e) {
    if (current()) error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
async function stop() {
  const c = conversation.value,
    run = activeRun.value;
  if (!c || !run) return;
  const ws = workspaceId.value,
    scope = state.scope.value;
  try {
    await api(
      'POST',
      `${chatPath(c.project_id, c.chat_id)}/turns/${run}/cancel`,
      undefined,
      undefined,
      { workspace: ws },
    );
    await client.invalidateQueries({ queryKey: ['ws', ws, 'coresearch', c.project_id] });
  } catch (e) {
    if (state.scope.value === scope) error.value = (e as Error).message;
  }
}
</script>
<template>
  <section class="coresearch aristotle-chat" data-testid="aristotle-chat">
    <div class="task-columns" :data-pane-mode="paneMode" style="--task-utility-width: 360px">
      <div
        class="task-main aristotle-conversation"
        data-column="conversation"
        :hidden="paneMode === 'focus'"
      >
        <header v-if="!isDraft" class="aristotle-header">
          <Aristotle still />
          <div>
            <b>{{
              conversation?.is_welcome
                ? 'Getting started with Aristotle'
                : conversation?.title || 'Chat with ' + (agent?.name || 'Aristotle')
            }}</b
            ><small>Your research companion</small>
          </div>
        </header>
        <div v-if="state.bootstrap.error.value" class="center-empty" role="alert">
          {{ state.bootstrap.error.value.message
          }}<button class="button" @click="state.bootstrap.refetch()">Try again</button>
        </div>
        <div v-else-if="state.conversations.error.value" class="center-empty" role="alert">
          {{ state.conversations.error.value.message
          }}<button class="button" @click="state.conversations.refetch()">Try again</button>
        </div>
        <div v-else-if="state.agent.error.value" class="center-empty" role="alert">
          {{ state.agent.error.value.message
          }}<button class="button" @click="state.agent.refetch()">Try again</button>
        </div>
        <div v-else-if="!agent || (!isDraft && !conversation)" class="center-empty" role="status">
          Loading Aristotle…
        </div>
        <template v-else
          ><div class="aristotle-scroll">
            <div v-if="isDraft" class="aristotle-start">
              <Aristotle still />
              <h1>Chat with {{ agent.name }}</h1>
              <p>
                {{
                  agent.description ||
                  'Your personal companion for thoughtful, evidence-grounded research.'
                }}
              </p>
              <div class="starter-stack">
                <button
                  v-for="starter in starters"
                  :key="starter.label"
                  :disabled="busy"
                  @click="send(starter.prompt)"
                >
                  {{ starter.label }}<Icon name="right" :size="14" />
                </button>
              </div>
              <RouterLink :to="`/agents/${agent.id}?tab=settings`" class="customize-starters"
                ><Icon name="settings" :size="14" />Customize starters</RouterLink
              >
            </div>
            <div v-else-if="messages.error.value" class="center-empty" role="alert">
              {{ messages.error.value.message }}<button @click="messages.refetch()">Retry</button>
            </div>
            <div v-else-if="messages.isPending.value" class="center-empty" role="status">
              Loading conversation…
            </div>
            <div
              v-else-if="conversation?.is_welcome && !messages.data.value?.length"
              class="aristotle-welcome"
            >
              <p>Welcome to Coresearch. I'm {{ agent.name }}, your research companion.</p>
              <p>
                Together, we can turn questions into clear hypotheses, design careful experiments,
                and make sense of the evidence.
              </p>
              <p>Start with an idea, a dataset, or a finding you want to explore.</p>
              <p>You stay in control of what happens next.</p>
              <div class="welcome-actions">
                <button
                  v-for="(starter, index) in defaultStarters"
                  :key="starter.label"
                  @click="send(starter.prompt)"
                >
                  <div class="research-motif" :class="'motif-' + index">
                    <span /><span /><span /><Icon
                      :name="['search', 'flask', 'check'][index]!"
                      :size="22"
                    />
                  </div>
                  <b>{{ starter.label }}</b
                  ><small>{{
                    [
                      'Find a focused question worth pursuing.',
                      'Plan the data, methods, and next steps.',
                      'Understand the strength of your results.',
                    ][index]
                  }}</small
                  ><span class="start-link">Start <Icon name="right" :size="12" /></span>
                </button>
              </div>
            </div>
            <div v-else-if="conversation" class="aristotle-transcript">
              <ChatTranscript
                :project-id="conversation.project_id"
                :chat-id="conversation.chat_id"
                :messages="messages.data.value ?? []"
              />
            </div>
          </div>
          <AristotleComposer
            v-model="draft"
            :busy="busy"
            :blocker="historyUnavailable ? 'Load conversation history before sending.' : blocker"
            :error="error"
            :active-run="activeRun ?? undefined"
            @send="send()"
            @stop="stop"
            ><template v-if="experiment" #top>
              <button
                type="button"
                class="composer-notebook-chip"
                :class="{ 'is-on': paneMode !== 'closed' && activePane === 'notebook' }"
                :aria-pressed="paneMode !== 'closed' && activePane === 'notebook'"
                @click="openPane"
              >
                <svg
                  width="13"
                  height="13"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.75"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <rect x="5" y="3" width="14" height="18" rx="2" />
                  <path d="M9 3v18M9 8h6M9 12h6" /></svg
                >Notebook
              </button></template
            ></AristotleComposer
          ></template
        >
      </div>
      <button
        v-if="paneMode !== 'closed'"
        type="button"
        class="task-pane-scrim"
        aria-label="Close panel"
        @click="closePane"
      />
      <div v-if="paneMode !== 'closed' && experiment" class="task-rightpane">
        <RightPaneTabs
          :tabs="PANE_TABS"
          :active-id="activePane"
          :pane-mode="paneMode"
          @select="selectPane"
          @toggle-focus="paneMode = paneMode === 'focus' ? 'split' : 'focus'"
          @close-pane="closePane"
        />
        <div class="task-rightpane-card">
          <p
            v-if="project.error.value || chats.error.value"
            class="p-5 type-body text-fg-subtle"
            role="alert"
          >
            {{ project.error.value?.message || chats.error.value?.message }}
          </p>
          <p
            v-else-if="!project.data.value || !chat"
            class="p-5 type-body text-fg-subtle"
            role="status"
          >
            Loading project…
          </p>
          <NotebookPane
            v-else-if="activePane === 'notebook'"
            :key="`${workspaceId}:${project.data.value.id}:${chat.id}:notebook`"
            :project="project.data.value"
            :chat="chat"
            :owner="chat.owner_id === me.data.value?.id"
          />
          <ComputePane
            v-else-if="activePane === 'compute'"
            :key="`${workspaceId}:${project.data.value.id}:${chat.id}:compute`"
            :project="project.data.value"
            :chat="chat"
            :owner="chat.owner_id === me.data.value?.id"
          />
          <FilesPane
            v-else-if="activePane === 'files' && activeFile"
            :key="`${workspaceId}:${project.data.value.id}:${activeFile.id}`"
            :project="project.data.value"
            :file="activeFile"
          />
          <SidebarFiles
            v-else-if="activePane === 'files'"
            :files="files.data.value ?? []"
            :loading="files.isPending.value"
            :error="files.error.value?.message"
            @open="openFile"
          />
        </div>
      </div>
    </div>
  </section>
</template>
<style scoped src="./aristotle.css" />

<style scoped>
.aristotle-chat {
  min-width: 0;
  overflow: hidden;
}
.aristotle-conversation {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.aristotle-conversation[hidden] {
  display: none;
}
</style>
