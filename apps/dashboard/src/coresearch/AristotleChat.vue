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
import { chatPath, isActiveTurn, ownOnlineClis, useCoresearchMessages } from './api';
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
    : state.conversations.data.value?.find(
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
      const id = restoreConversation(list, readText(lastKey(state.scope.value)) ?? null);
      if (id) void router.replace(`/coresearch/conversations/${id}`);
    } else if (route.params.conversationId) {
      const selected = list.find((c) => c.id === route.params.conversationId);
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
            Together, we can turn questions into clear hypotheses, design careful experiments, and
            make sense of the evidence.
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
    /></template>
  </section>
</template>
<style scoped src="./aristotle.css" />
