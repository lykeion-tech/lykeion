<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import {
  useAristotle,
  acceptanceRequest,
  wireCapabilities,
  conversationKey,
  defaultStarters,
  restoreConversation,
  type AcceptanceRequest,
  type Conversation,
  type ComposerAttachment,
  composerGeneration,
  holdComposerContinuation,
  takeComposerContinuation,
  clearComposerContinuation,
} from './aristotle';
import { readText, writeText } from '../../storage';
import { workspaceId } from '../../api/session';
import { useAgents, useMe, useRuntimes } from '../../api/queries';
import { api } from '../../api/client';
import { guardResourceDrafts } from '../../state/resource-draft';
import type { FileRow, SessionMode } from './types';
import type { AgentDefinition } from '../../api/types';
import SurfacePane from '../../ui/SurfacePane.vue';
import { useElementWidth } from './element-width.ts';
import { agentNameWithoutCli } from '../../lib/agent-name';
import NotebookPane from './NotebookPane.vue';
import FilesPane from './FilesPane.vue';
import ComputePane from './ComputePane.vue';
import ExperimentFilesGallery from './ExperimentFilesGallery.vue';
import {
  chatPath,
  isActiveTurn,
  ownOnlineClis,
  useCoresearchProject,
  useCoresearchChats,
  useCoresearchFiles,
  useCoresearchMessages,
} from './api';
import ChatAgentAvatar from './ChatAgentAvatar.vue';
import Icon from '../../ui/Icon.vue';
import ChatTranscript from './ChatTranscript.vue';
import type { TranscriptFile } from './transcript-files';
import AristotleComposer from './AristotleComposer.vue';
import './l2.css';
const route = useRoute(),
  router = useRouter(),
  client = useQueryClient();
const state = useAristotle(),
  me = useMe(),
  agents = useAgents(),
  runtimes = useRuntimes();
const selectedAgentId = ref('');
const agentPickerOpen = ref(false);
const agentPicker = ref<HTMLElement>();
const agentPickerTrigger = ref<HTMLButtonElement>();
const availableAgents = computed(() =>
  (agents.data.value ?? []).filter(
    (item) =>
      !item.archived_at && (item.owner_id === me.data.value?.id || item.visibility === 'workspace'),
  ),
);
async function toggleAgentPicker() {
  agentPickerOpen.value = !agentPickerOpen.value;
  if (agentPickerOpen.value) {
    await nextTick();
    agentPicker.value
      ?.querySelector<HTMLButtonElement>('[role="menuitemradio"][aria-checked="true"]')
      ?.focus();
  }
}
function agentPickerKey(event: KeyboardEvent) {
  if (!agentPickerOpen.value) return;
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    agentPickerOpen.value = false;
    agentPickerTrigger.value?.focus();
    return;
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  const options = Array.from(
    agentPicker.value?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? [],
  );
  if (!options.length) return;
  event.preventDefault();
  event.stopPropagation();
  const index = options.indexOf(document.activeElement as HTMLButtonElement);
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? options.length - 1
        : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
  options[next]?.focus();
}
function closeAgentPicker(event: PointerEvent) {
  if (agentPicker.value && !agentPicker.value.contains(event.target as Node))
    agentPickerOpen.value = false;
}
onMounted(() => document.addEventListener('pointerdown', closeAgentPicker));
onUnmounted(() => document.removeEventListener('pointerdown', closeAgentPicker));
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
const startsSession = computed(() => isDraft.value || !!conversation.value?.is_welcome);
const agent = computed(() => {
  const id = isDraft.value ? selectedAgentId.value : conversation.value?.agent_id;
  if (!id || id === state.agent.data.value?.id) return state.agent.data.value;
  const found = (agents.data.value ?? []).find((item) => item.id === id);
  if (found || isDraft.value || !agents.isSuccess.value) return found;
  // A conversation keeps its frozen configuration even if its Agent is later deleted or no longer shared.
  return {
    id,
    name: 'Unavailable agent',
    description: '',
    avatar_url: '',
    built_in_key: '',
    conversation_starters: [],
    harness: conversation.value?.snapshot_harness ?? 'claude',
    model: conversation.value?.snapshot_model ?? '',
    visibility: 'private',
    owner_id: '',
    workspace_id: workspaceId.value,
    instructions: '',
    archived_at: null,
    created_at: '',
    updated_at: '',
  } satisfies AgentDefinition;
});
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
const experimentLayout = ref<HTMLElement>();
const experimentWidth = useElementWidth(experimentLayout);
const activePane = ref<string | null>(null),
  paneMode = ref<'closed' | 'split' | 'focus'>('closed'),
  activeFile = ref<FileRow>(),
  activeFileVersion = ref<number>();
let paneKey = '';
let restoringPane = false;
const inspectorProject = computed(() =>
  experiment.value && paneMode.value !== 'closed' ? (conversation.value?.project_id ?? '') : '',
);
const project = useCoresearchProject(() => inspectorProject.value),
  chats = useCoresearchChats(() => inspectorProject.value),
  files = useCoresearchFiles(() => inspectorProject.value);
const chat = computed(() => chats.data.value?.find((c) => c.id === conversation.value?.chat_id));
watch(
  [() => workspaceId.value, () => me.data.value?.id, () => conversation.value?.id],
  ([workspace, user, conversationId]) => {
    restoringPane = true;
    paneKey =
      workspace && user && conversationId
        ? `lykeion.experiment-pane-open:${workspace}:${user}:${conversationId}`
        : '';
    paneMode.value = paneKey && readText(paneKey, 'session') === 'true' ? 'split' : 'closed';
    activePane.value = null;
    activeFile.value = undefined;
    restoringPane = false;
  },
  { immediate: true, flush: 'sync' },
);
watch(
  paneMode,
  (mode) => {
    if (paneKey && !restoringPane) writeText(paneKey, String(mode !== 'closed'), 'session');
  },
  { flush: 'sync' },
);
async function selectPane(id: string | null) {
  if (activePane.value === 'files' && !(await guardResourceDrafts())) return;
  if (id === 'files') activeFile.value = undefined;
  activePane.value = id;
}
const experimentSurfaces = [
  { id: 'notebook', label: 'Notebook', icon: 'code' },
  { id: 'files', label: 'Files', icon: 'file' },
  { id: 'compute', label: 'Compute', icon: 'monitor' },
];
async function toggleSurfaces() {
  if (paneMode.value === 'closed') paneMode.value = 'split';
  else await closePane();
}
async function closePane() {
  if (activePane.value === 'files' && !(await guardResourceDrafts())) return;
  paneMode.value = 'closed';
}
async function openFile(file: FileRow) {
  if (!(await guardResourceDrafts())) return;
  activeFile.value = file;
  activeFileVersion.value = undefined;
}
async function openTranscriptFile(item: TranscriptFile) {
  if (!(await guardResourceDrafts())) return;
  activeFile.value = item.file;
  activeFileVersion.value = item.version.n;
  activePane.value = 'files';
  if (paneMode.value === 'closed') paneMode.value = 'split';
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
const availableClis = computed(() => ownOnlineClis(runtimes.data.value ?? [], me.data.value?.id));
const selectedCli = ref<string>();
const harness = computed(
  () =>
    (!startsSession.value && conversation.value?.snapshot_harness) ||
    (selectedCli.value &&
      availableClis.value.includes(selectedCli.value as 'claude' | 'codex') &&
      selectedCli.value) ||
    (agent.value?.harness &&
      availableClis.value.includes(agent.value.harness as 'claude' | 'codex') &&
      agent.value.harness) ||
    availableClis.value[0] ||
    agent.value?.harness ||
    'claude',
);
function chooseCli(value: string) {
  if (!availableClis.value.includes(value as 'claude' | 'codex') || value === harness.value) return;
  selectedCli.value = value;
  selectedRuntime.value = undefined;
  selectedModel.value = null;
  pending = null;
}
const selectedRuntime = ref<string>();
const runtime = computed(() => {
  const own =
    runtimes.data.value?.filter(
      (r) =>
        r.owner_id === me.data.value?.id &&
        r.status === 'online' &&
        !r.unhealthy_reason &&
        r.harness === harness.value,
    ) ?? [];
  return own.find((r) => r.id === selectedRuntime.value) ?? own[0];
});
const models = computed(() => runtime.value?.models ?? []);
const mode = ref<SessionMode>('experiment'),
  template = ref('blank'),
  selectedModel = ref<string | null>(null);
const effectiveModel = computed(() =>
  startsSession.value
    ? (models.value.find((m) => m.value === selectedModel.value)?.value ??
      (harness.value === agent.value?.harness ? agent.value?.model : undefined) ??
      models.value.find((m) => m.value === agent.value?.model)?.value ??
      models.value[0]?.value ??
      '')
    : (conversation.value?.snapshot_model ?? ''),
);
const attachments = ref<ComposerAttachment[]>([]),
  uploading = ref(false);
const blocker = computed(() =>
  availableClis.value.some((cli) => cli === harness.value)
    ? undefined
    : `No available own machine with ${harness.value}; connect a machine in Settings.`,
);
const composerCapabilities = ref({ skills: [] as string[], connectors: [] as string[] });
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
  [state.scope, () => route.path, composerGeneration],
  () => {
    viewGeneration++;
    draft.value = '';
    composerCapabilities.value = { skills: [], connectors: [] };
    error.value = '';
    busy.value = false;
    pending = null;
    mode.value = 'experiment';
    template.value = 'blank';
    selectedModel.value = null;
    selectedCli.value = undefined;
    selectedRuntime.value = undefined;
    selectedAgentId.value = '';
    agentPickerOpen.value = false;
    attachments.value = [];
    uploading.value = false;
    if (route.path.startsWith('/coresearch') && state.member.value && workspaceId.value) {
      const held = takeComposerContinuation(state.scope.value, route.fullPath, historyPosition());
      if (held) {
        selectedAgentId.value = held.agentId ?? '';
        draft.value = held.body;
        mode.value = held.mode;
        template.value = held.template;
        if (held.harness === 'claude' || held.harness === 'codex') selectedCli.value = held.harness;
        if (held.harness === harness.value) {
          selectedRuntime.value = held.runtime;
          selectedModel.value = held.model;
        }
        attachments.value = held.attachments;
        composerCapabilities.value = held.capabilities ?? { skills: [], connectors: [] };
        pending = held.pending;
      }
    }
  },
  { flush: 'sync', immediate: true },
);
function historyPosition() {
  const value = router.options.history.state.position;
  return typeof value === 'number' ? value : undefined;
}
function rememberComposer() {
  if (busy.value || uploading.value) return;
  holdComposerContinuation({
    scope: state.scope.value,
    path: route.fullPath,
    position: historyPosition(),
    body: draft.value,
    agentId: selectedAgentId.value,
    mode: mode.value,
    template: template.value,
    runtime: runtime.value?.id,
    harness: harness.value,
    model: selectedAgentId.value ? selectedModel.value : effectiveModel.value,
    attachments: attachments.value,
    capabilities: composerCapabilities.value,
    pending,
  });
}
async function manageComposer(page: string) {
  if (busy.value || uploading.value) return;
  rememberComposer();
  await router.push(`/settings?tab=${page}`);
}
watch(
  [() => state.conversations.data.value, () => route.path],
  ([list]) => {
    if (!list?.length) return;
    const owned = list.filter((c): c is Conversation => !c.shared);
    const openConversation = (id: string) => {
      const target = owned.find((c) => c.id === id);
      if (target)
        void router.replace(
          target.mode === 'cowrite'
            ? `/coresearch/${target.project_id}/${target.chat_id}`
            : `/coresearch/conversations/${target.id}`,
        );
    };
    if (route.path === '/coresearch') {
      const id = restoreConversation(owned, readText(lastKey(state.scope.value)) ?? null);
      if (id) openConversation(id);
      else void router.replace('/coresearch/new');
    } else if (route.params.conversationId) {
      const selected = owned.find((c) => c.id === route.params.conversationId);
      if (selected) writeText(lastKey(state.scope.value), selected.id);
      else {
        const id = restoreConversation(owned, readText(lastKey(state.scope.value)) ?? null);
        if (id) openConversation(id);
      }
    }
  },
  { immediate: true },
);
async function send(prompt?: string, capabilities?: { skills: string[]; connectors: string[] }) {
  if (prompt) draft.value = prompt;
  if (
    !draft.value.trim() ||
    busy.value ||
    uploading.value ||
    historyUnavailable.value ||
    blocker.value ||
    activeRun.value ||
    !agent.value
  )
    return;
  const ws = workspaceId.value,
    member = state.member.value,
    scope = state.scope.value,
    target = isDraft.value ? '' : (conversation.value?.id ?? ''),
    path = route.path;
  const generation = ++viewGeneration;
  const options = {
    ...(capabilities && (capabilities.skills.length || capabilities.connectors.length)
      ? { capabilities: wireCapabilities(capabilities) }
      : {}),
    mode: startsSession.value ? mode.value : (conversation.value?.mode ?? 'experiment'),
    ...(startsSession.value && mode.value === 'cowrite' ? { template: template.value } : {}),
    ...(startsSession.value
      ? {
          ...(selectedModel.value !== null || (!selectedAgentId.value && models.value.length)
            ? { model: effectiveModel.value }
            : {}),
          harness: harness.value,
        }
      : {}),
    ...(isDraft.value && selectedAgentId.value ? { agent_id: selectedAgentId.value } : {}),
    ...(attachments.value.length
      ? { attachments: attachments.value.map((file) => ({ ...file })) }
      : {}),
  };
  const request = acceptanceRequest(pending, draft.value.trim(), target, JSON.stringify(options));
  pending = request;
  busy.value = true;
  error.value = '';
  const current = () =>
    generation === viewGeneration && state.scope.value === scope && route.path === path;
  try {
    const accepted = await api<{ conversation: Conversation; run: { id: string } }>(
      'POST',
      target ? `/api/coresearch/conversations/${target}/turns` : '/api/coresearch/conversations',
      { body: request.body, request_id: request.request_id, ...options },
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
      clearComposerContinuation();
      draft.value = '';
      pending = null;
      attachments.value = [];
      await router.push(
        options.mode === 'cowrite'
          ? `/coresearch/${accepted.conversation.project_id}/${accepted.conversation.chat_id}`
          : `/coresearch/conversations/${accepted.conversation.id}`,
      );
    }
  } catch (e) {
    if (current()) error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
async function uploadFiles(chosen: File[]) {
  if (busy.value || uploading.value) return;
  const ws = workspaceId.value,
    scope = state.scope.value,
    path = route.path,
    generation = viewGeneration;
  const current = () =>
    ws === workspaceId.value &&
    scope === state.scope.value &&
    path === route.path &&
    generation === viewGeneration;
  uploading.value = true;
  error.value = '';
  const refused: string[] = [];
  try {
    for (const file of chosen) {
      if (!current()) return;
      try {
        const form = new FormData();
        form.append('file', file, file.name);
        const result = await api<{ upload_id: string }>(
          'POST',
          '/api/coresearch/uploads',
          form,
          undefined,
          { workspace: ws },
        );
        if (current()) attachments.value.push({ upload_id: result.upload_id, name: file.name });
      } catch (e) {
        refused.push(`${file.name} — ${(e as Error).message}`);
      }
    }
    if (current()) error.value = refused.join('; ');
  } finally {
    if (current()) uploading.value = false;
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
    <div
      ref="experimentLayout"
      class="task-columns experiment-surface-layout"
      :data-pane-mode="paneMode"
    >
      <div
        class="task-main aristotle-conversation"
        data-column="conversation"
        :hidden="paneMode === 'focus'"
      >
        <header v-if="!isDraft" class="aristotle-header">
          <ChatAgentAvatar
            :agent="agent"
            :size="34"
            :fallback-aristotle="conversation?.is_welcome"
          />
          <div>
            <b>{{
              conversation?.is_welcome
                ? 'Getting started with Aristotle'
                : conversation?.title || 'Chat with ' + (agent?.name || 'Aristotle')
            }}</b
            ><small>Your research companion</small>
          </div>
          <button
            v-if="experiment && paneMode === 'closed'"
            type="button"
            class="experiment-toggle"
            aria-label="Open experiment surfaces"
            @click="toggleSurfaces"
          >
            <Icon name="panel" :size="16" aria-hidden="true" />
          </button>
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
          Loading agent…
        </div>
        <template v-else
          ><div class="aristotle-scroll">
            <div v-if="isDraft" class="aristotle-start">
              <ChatAgentAvatar :agent="agent" />
              <h1>Chat with {{ agentNameWithoutCli(agent.name, agent.harness) }}</h1>
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
              <div class="starter-actions">
                <RouterLink
                  :to="`/agents/${agent.id}?tab=settings`"
                  class="customize-starters"
                  @click="rememberComposer"
                  ><Icon name="settings" :size="14" />Customize starters</RouterLink
                >
                <div ref="agentPicker" class="chat-agent-picker" @keydown="agentPickerKey">
                  <button
                    ref="agentPickerTrigger"
                    type="button"
                    class="customize-starters"
                    aria-label="Choose agent"
                    aria-haspopup="menu"
                    :aria-expanded="agentPickerOpen"
                    :disabled="busy"
                    @click="toggleAgentPicker"
                  >
                    <Icon name="bot" :size="14" />Choose agent
                  </button>
                  <div
                    v-if="agentPickerOpen"
                    class="chat-agent-menu"
                    role="menu"
                    aria-label="Agents"
                  >
                    <button
                      v-for="item in availableAgents"
                      :key="item.id"
                      type="button"
                      role="menuitemradio"
                      :aria-checked="item.id === agent.id"
                      @click="
                        selectedAgentId = item.id;
                        selectedCli = undefined;
                        selectedRuntime = undefined;
                        selectedModel = null;
                        pending = null;
                        agentPickerOpen = false;
                      "
                    >
                      <ChatAgentAvatar :agent="item" :size="24" /><span>{{
                        agentNameWithoutCli(item.name, item.harness)
                      }}</span
                      ><Icon v-if="item.id === agent.id" name="check" :size="14" />
                    </button>
                    <p v-if="agents.isError.value" role="alert">
                      Could not load agents.
                      <button type="button" @click="agents.refetch()">Retry</button>
                    </p>
                    <p v-else-if="!availableAgents.length">No agents available.</p>
                  </div>
                </div>
              </div>
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
                make sense of the evidence, and write your research together in Cowrite.
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
                <button
                  :disabled="busy"
                  @click="
                    mode = 'cowrite';
                    send(
                      'Help me write a research paper. Ask about my topic, intended audience, and any evidence or drafts I have first.',
                    );
                  "
                >
                  <div class="research-motif motif-3">
                    <span /><span /><span /><Icon name="edit" :size="22" />
                  </div>
                  <b>Write a research{{ ' ' }}<br />paper</b>
                  <small>Develop and refine your manuscript together.</small>
                  <span class="start-link">Start <Icon name="right" :size="12" /></span>
                </button>
              </div>
            </div>
            <div v-else-if="conversation" class="aristotle-transcript">
              <ChatTranscript
                :project-id="conversation.project_id"
                :chat-id="conversation.chat_id"
                :messages="messages.data.value ?? []"
                @open-file="openTranscriptFile"
              />
            </div>
            <AristotleComposer
              v-if="isDraft"
              v-model="draft"
              v-model:mode="mode"
              v-model:template="template"
              :cli="harness"
              :clis="availableClis"
              :selected-model="effectiveModel"
              :model-chosen="selectedModel !== null"
              :models="models"
              :draft="isDraft"
              :attachments="attachments"
              :initial-capabilities="composerCapabilities"
              :busy="busy || uploading"
              :blocker="historyUnavailable ? 'Load conversation history before sending.' : blocker"
              :error="error"
              :active-run="activeRun ?? undefined"
              @capabilities="composerCapabilities = $event"
              @send="send(undefined, $event)"
              @stop="stop"
              @update:cli="chooseCli"
              @update:selected-model="selectedModel = $event"
              @files="uploadFiles"
              @manage="manageComposer"
              @remove="attachments = attachments.filter((file) => file.upload_id !== $event)"
            />
          </div>
          <AristotleComposer
            v-if="!isDraft"
            v-model="draft"
            v-model:mode="mode"
            v-model:template="template"
            :cli="harness"
            :clis="availableClis"
            :selected-model="effectiveModel"
            :model-chosen="selectedModel !== null"
            :models="models"
            :draft="startsSession"
            :attachments="attachments"
            :initial-capabilities="composerCapabilities"
            :busy="busy || uploading"
            :blocker="historyUnavailable ? 'Load conversation history before sending.' : blocker"
            :error="error"
            :active-run="activeRun ?? undefined"
            @capabilities="composerCapabilities = $event"
            @send="send(undefined, $event)"
            @stop="stop"
            @update:cli="chooseCli"
            @update:selected-model="selectedModel = $event"
            @files="uploadFiles"
            @manage="manageComposer"
            @remove="attachments = attachments.filter((file) => file.upload_id !== $event)"
          ></AristotleComposer
        ></template>
      </div>
      <SurfacePane
        v-if="experiment"
        hide-title
        :open="paneMode !== 'closed'"
        :active="activePane"
        :focused="paneMode === 'focus'"
        :surfaces="experimentSurfaces"
        :default-width="240"
        :restore-width="false"
        :container-width="experimentWidth || undefined"
        :focus-key="activeFile?.id"
        width-key="lykeion.experiment-pane-width"
        @update:open="closePane"
        @update:active="selectPane"
        @update:focused="paneMode = $event ? 'focus' : 'split'"
      >
        <template #default>
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
              :selected-n="activeFileVersion"
            />
            <section v-else-if="activePane === 'files'" class="experiment-files" aria-label="Files">
              <header class="art-header experiment-files-header">
                <span class="art-title"
                  ><Icon name="file" :size="16" aria-hidden="true" />Files</span
                >
              </header>
              <ExperimentFilesGallery
                :project-id="project.data.value.id"
                :files="files.data.value ?? []"
                :loading="files.isPending.value"
                :error="files.error.value?.message"
                @open="openFile"
              />
            </section>
          </div>
        </template>
      </SurfacePane>
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

<style scoped>
.experiment-surface-layout {
  display: flex !important;
  position: relative;
  min-width: 0;
}
.experiment-surface-layout > .task-main {
  min-width: 0;
  flex: 1;
}
.experiment-surface-layout > .task-main[hidden] {
  display: none !important;
}
.experiment-surface-layout :deep(.surface-pane) {
  min-height: 0;
}
.experiment-surface-layout :deep(.surface-chooser h2) {
  font-weight: 700;
}
.experiment-surface-layout :deep(.task-rightpane-card) {
  border: 0;
  border-radius: 0;
  height: 100%;
}
.experiment-files {
  min-height: 100%;
  display: flex;
  flex-direction: column;
}
.experiment-surface-layout :deep(.task-rightpane-card),
.experiment-surface-layout :deep(.notebook-panel),
.experiment-surface-layout :deep(.compute-panel),
.experiment-surface-layout :deep(.artifact-pane) {
  background: var(--surface);
}
.experiment-toggle {
  margin-left: auto;
}
.experiment-toggle:hover {
  background: color-mix(in srgb, var(--text) 12%, transparent);
}
.experiment-surface-layout :deep(.surface-header) {
  padding-right: 12px;
}
.experiment-surface-layout :deep(.surface-header .surface-icon[aria-label='Close pane']) {
  transform: translateY(-0.5px);
}
.experiment-surface-layout :deep(.surface-choice) {
  font-weight: 500;
}
</style>
