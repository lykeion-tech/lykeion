<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { ArrowUp, Brain, ChevronDown, CircleDot, GripVertical, Minus, Plus, Trash2, X } from '@lucide/vue';
import Aristotle from '../coresearch/Aristotle.vue';
import { ownOnlineClis } from '../coresearch/api';
import { useRuntimes } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { api, ApiError } from '../../api/client';
import { askLab, createChat, deleteChat, labKey, useChats, useMessages } from './api';
import { useWindowPlacement } from './useWindowPlacement';
import { readJSON, writeJSON } from '../../storage';
import LabWelcome from './LabWelcome.vue';
import LabAnswer from './LabAnswer.vue';
import LabContextPicker from './LabContextPicker.vue';
import RunStatus from '../runs/RunStatus.vue';
import { parseManualDecisionProposal, type ManualDecisionDraft, type ManualDecisionTurn } from '../decisions/manualDecisionDraft';

const props = defineProps<{ userId: string; studyId?: string; studyTitle?: string; draftChat?: string; appliedMessageId?: string | null; manualDraftBusy?: boolean; readonly?: boolean; manualDraft?: ManualDecisionDraft; submitManualQuestion?: (turn: ManualDecisionTurn) => Promise<void> }>();
const emit = defineEmits<{ manualProposal: [value: { messageId: string; draft: ManualDecisionDraft; input: ManualDecisionDraft }] }>();
const drafting = computed(() => !!props.draftChat);
const open = ref(!!props.draftChat),
  focused = ref(false),
  evidence = ref(true);
type Menu = '' | 'chats' | 'options' | 'context' | 'model';
const menu = ref<Menu>(''),
  panel = ref<HTMLElement>(),
  fab = ref<HTMLButtonElement>(),
  minimize = ref<HTMLButtonElement>();
const log = ref<HTMLElement>(),
  composer = ref<HTMLInputElement>(),
  chat = ref(props.draftChat ?? ''),
  selectedTitle = ref('New chat');
const placement = useWindowPlacement(
  'lykeion.aristotle-window:' + workspaceId.value + ':' + props.userId,
  panel,
);
const { style, logHeight } = placement;
const fabKey = 'lykeion.aristotle-button:' + workspaceId.value + ':' + props.userId;
const savedFab = readJSON<{ right: number; bottom: number }>(fabKey, { right: 20, bottom: 20 });
const fabPosition = ref({
  right: Number.isFinite(savedFab?.right) ? savedFab.right : 20,
  bottom: Number.isFinite(savedFab?.bottom) ? savedFab.bottom : 20,
});
const fabViewport = ref({ width: window.innerWidth, height: window.innerHeight });
function clampFab() {
  fabPosition.value = {
    right: Math.max(0, Math.min(fabPosition.value.right, Math.max(0, fabViewport.value.width - 48))),
    bottom: Math.max(0, Math.min(fabPosition.value.bottom, Math.max(0, fabViewport.value.height - 48))),
  };
}
const fabStyle = computed(() => ({ right: fabPosition.value.right + 'px', bottom: fabPosition.value.bottom + 'px' }));
let stopFabDrag: (() => void) | undefined;
let fabDragged = false;
function startFabDrag(event: PointerEvent) {
  if (event.button !== 0) return;
  stopFabDrag?.();
  fabDragged = false;
  const x = event.clientX, y = event.clientY, { right, bottom } = fabPosition.value;
  const move = (next: PointerEvent) => {
    if (next.pointerId !== event.pointerId) return;
    const dx = next.clientX - x, dy = next.clientY - y;
    if (!fabDragged && Math.hypot(dx, dy) < 4) return;
    fabDragged = true;
    fabPosition.value = { right: right - dx, bottom: bottom - dy };
    clampFab();
  };
  const stop = (next?: PointerEvent) => {
    if (next && next.pointerId !== event.pointerId) return;
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', stop);
    window.removeEventListener('pointercancel', stop);
    if (fabDragged) writeJSON(fabKey, fabPosition.value);
    stopFabDrag = undefined;
  };
  stopFabDrag = () => stop();
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', stop);
  window.addEventListener('pointercancel', stop);
}
function clickFab() {
  if (fabDragged) { fabDragged = false; return; }
  toggle();
}
function moveFabWithKeys(event: KeyboardEvent) {
  const offsets: Record<string, [number, number]> = { ArrowLeft: [10, 0], ArrowRight: [-10, 0], ArrowUp: [0, 10], ArrowDown: [0, -10] };
  const offset = offsets[event.key];
  if (!offset) return;
  event.preventDefault();
  fabPosition.value = { right: fabPosition.value.right + offset[0], bottom: fabPosition.value.bottom + offset[1] };
  clampFab();
  writeJSON(fabKey, fabPosition.value);
}
function resizeFab() {
  fabViewport.value = { width: window.innerWidth, height: window.innerHeight };
  clampFab();
}
clampFab();
const normalOpen = computed(() => open.value && !drafting.value);
const chats = useChats(() => props.userId, normalOpen),
  messages = useMessages(() => props.userId, open, chat, () => drafting.value ? 'manual_decision_draft' : 'answer');
const recent = computed(() => chats.data.value?.pages.flatMap((page) => page.chats) ?? []);
const title = computed(
  () => drafting.value ? 'Draft with Aristotle' : recent.value.find((c) => c.id === chat.value)?.title || selectedTitle.value,
);
const turns = computed(() =>
  messages.isError.value
    ? []
    : (messages.data.value?.pages
        .slice()
        .reverse()
        .flatMap((p) => p.messages) ?? []),
);
const handledProposals = new Set<string>();
watch([turns, () => messages.isSuccess.value, () => messages.isFetching.value, () => props.appliedMessageId, () => props.manualDraftBusy], ([list, success]) => {
  if (!drafting.value || props.readonly || props.manualDraftBusy || !success) return;
  // The durable marker fences every older completion. Restore happens before
  // this window mounts, so a later human reversion is never replayed over.
  for (let index = list.length - 1; index >= 0; index--) {
    const message = list[index]!;
    if (message.id === props.appliedMessageId) return;
    if (message.kind !== 'answer') continue;
    const proposal = parseManualDecisionProposal(message.proposal);
    if (!proposal) continue;
    if (handledProposals.has(message.id)) return;
    const question = message.agent_run_id
      ? list.find(item => item.kind === 'question' && item.agent_run_id === message.agent_run_id)
      : undefined;
    if (question?.current_draft) {
      handledProposals.add(message.id);
      emit('manualProposal', { messageId: message.id, draft: proposal.draft, input: question.current_draft });
      return;
    }
    // A page may end between the answer and its frozen input. Read the prior
    // page before merging; never infer the input from the current form.
    if (messages.hasNextPage.value && !messages.isFetching.value) void messages.fetchNextPage();
    return;
  }
  if (messages.hasNextPage.value && !messages.isFetching.value) void messages.fetchNextPage();
}, { immediate: true });
const currentStudy = computed(() =>
  !drafting.value && props.studyId && props.studyTitle
    ? { ref: 'study:' + props.studyId, title: 'This study: ' + props.studyTitle }
    : null,
);
const picked = ref<{ ref: string; title: string } | null>(),
  context = computed(() => (picked.value === undefined ? currentStudy.value : picked.value));
watch(
  () => props.studyId,
  () => {
    picked.value = undefined;
  },
);
const harness = ref<'claude' | 'codex'>('claude');
const model = ref(''),
  draft = ref(''),
  sending = ref(false),
  error = ref(''),
  offline = ref(false),
  deleting = ref(''),
  deleteError = ref('');
const client = useQueryClient(),
  runtimes = useRuntimes();
const healthy = computed(() => (runtimes.data.value ?? []).filter((r) => !r.unhealthy_reason));
const clis = computed(() => ownOnlineClis(healthy.value, props.userId));
const models = computed(() => {
  const values = healthy.value
    .filter((r) => r.owner_id === props.userId && r.status === 'online' && r.harness === harness.value)
    .flatMap((r) => r.models ?? []);
  return Array.from(new Map(values.map((m) => [m.value, m])).values());
});
watch(models, (list) => {
  if (!list.some((m) => m.value === model.value)) model.value = '';
});
watch(clis, (list) => {
  if (!list.includes(harness.value) && list.length) { harness.value = list[0]!; model.value = ''; }
}, { immediate: true });
function chooseModel(id: string, cli: 'claude' | 'codex' = harness.value) {
  harness.value = cli;
  model.value = id;
  menu.value = '';
  composer.value?.focus();
}
const menuBox = ref<HTMLElement>();
const menuStyle = ref<{ top?: string; bottom?: string; maxHeight?: string }>({});
let menuOpener: HTMLElement | null = null;
function placeMenu() {
  const host = panel.value;
  const anchor = host?.querySelector<HTMLElement>('[data-lab-menu="' + menu.value + '"]');
  if (!anchor || !host) return;
  const a = anchor.getBoundingClientRect(),
    p = host.getBoundingClientRect(),
    gap = 6,
    margin = 8;
  const above = a.top - gap - margin,
    below = window.innerHeight - a.bottom - gap - margin;
  menuStyle.value =
    above >= below
      ? {
          bottom: p.bottom - a.top + gap + 'px',
          maxHeight: Math.max(0, Math.min(300, above)) + 'px',
        }
      : { top: a.bottom - p.top + gap + 'px', maxHeight: Math.max(0, Math.min(300, below)) + 'px' };
}
function closeMenu(restore = false) {
  menu.value = '';
  if (restore) (menuOpener?.isConnected ? menuOpener : minimize.value)?.focus();
  menuOpener = null;
}
function showMenu(
  value: Menu,
  opener: HTMLElement | null = document.activeElement as HTMLElement | null,
) {
  menuOpener = opener;
  menu.value = value;
  nextTick(() => {
    if (menu.value !== value || !menuBox.value) return;
    placeMenu();
    const first = menuBox.value.querySelector<HTMLElement>('input,button:not(:disabled),a[href]');
    (first ?? menuBox.value).focus();
  });
}
function toggleMenu(value: Menu, event: MouseEvent) {
  if (menu.value === value) closeMenu(true);
  else showMenu(value, event.currentTarget as HTMLElement);
}
watch(
  [style, logHeight],
  () => {
    if (menu.value) nextTick(placeMenu);
  },
  { flush: 'post' },
);
function toggle() {
  open.value = !open.value;
  menu.value = '';
  if (!open.value) focused.value = false;
  nextTick(() => {
    if (open.value) minimize.value?.focus();
    else fab.value?.focus();
  });
}
defineExpose({
  toggle,
  modelMenu: () => {
    const opener = document.activeElement as HTMLElement | null;
    if (!open.value) toggle();
    nextTick(() => showMenu('model', opener));
  },
});
function focus() {
  focused.value = true;
  if (!turns.value.length && (!chat.value || messages.isSuccess.value))
    placement.setHeight(Math.max(440, logHeight.value));
}
function switchChat(id: string, name = 'New chat') {
  if (sending.value) return;
  chat.value = id;
  selectedTitle.value = name;
  menu.value = '';
  error.value = '';
  offline.value = false;
  focused.value = false;
  placement.setHeight(id ? 340 : 0);
}
async function removeChat(id: string) {
  if (sending.value || deleting.value) return;
  const ws = workspaceId.value;
  deleting.value = id;
  deleteError.value = '';
  try {
    await deleteChat(ws, id);
    if (!alive || workspaceId.value !== ws) return;
    if (chat.value === id) switchChat('');
    await client.invalidateQueries({ queryKey: labKey(ws) });
  } catch (e) {
    if (alive && workspaceId.value === ws) deleteError.value = (e as Error).message;
  } finally {
    deleting.value = '';
  }
}
function pick(value: { ref: string; title: string }) {
  picked.value = value;
  menu.value = '';
  composer.value?.focus();
}
let alive = true;
const initialOwner = props.userId, initialWorkspace = workspaceId.value, initialChat = props.draftChat;
const currentVisit = () => alive && props.userId === initialOwner && workspaceId.value === initialWorkspace && props.draftChat === initialChat;
const cancelling = ref(false);
const pending = computed(() => !!messages.data.value?.pages[0]?.pending);
let retryTurn: { key: string; id: string } | undefined;
async function stopDraft() {
  const question = [...turns.value].reverse().find(message => message.kind === 'question');
  if (!drafting.value || props.readonly || !question || cancelling.value) return;
  cancelling.value = true; error.value = '';
  try {
    await api('POST', `/api/lab/chats/${encodeURIComponent(chat.value)}/questions/${encodeURIComponent(question.id)}/cancel`, undefined, undefined, { workspace: initialWorkspace });
    if (currentVisit()) await client.invalidateQueries({ queryKey: labKey(initialWorkspace) });
  } catch (e) { if (currentVisit()) error.value = (e as Error).message; }
  finally { if (currentVisit()) cancelling.value = false; }
}
async function ask(question = draft.value) {
  const text = question.trim();
  if (props.readonly || sending.value || deleting.value || !text || (drafting.value && pending.value)) return;
  draft.value = question;
  error.value = '';
  offline.value = false;
  menu.value = '';
  if (Array.from(text).length > 8000) {
    error.value = 'Questions can contain up to 8,000 characters.';
    return;
  }
  const ws = workspaceId.value;
  const payload = {
    text,
    model: model.value,
    harness: harness.value,
    context_ref: context.value?.ref,
  };
  sending.value = true;
  try {
    if (drafting.value) {
      const currentDraft = props.manualDraft && { ...props.manualDraft };
      if (!currentDraft || !props.submitManualQuestion) throw new Error('Load a draft before asking Aristotle.');
      const turn = { user_request: text, current_draft: currentDraft, harness: harness.value, model: model.value };
      const key = JSON.stringify(turn);
      if (retryTurn?.key !== key) retryTurn = { key, id: crypto.randomUUID() };
      await props.submitManualQuestion({ ...turn, request_id: retryTurn.id });
      retryTurn = undefined;
    } else {
    if (!chat.value) {
      const { chat: created } = await createChat(ws);
      if (!currentVisit()) return;
      chat.value = created.id;
      selectedTitle.value = created.title;
    }
    await askLab(ws, chat.value, payload);
    }
    if (!currentVisit()) return;
    if (draft.value === question) draft.value = '';
    focused.value = false;
    placement.setHeight(Math.max(300, logHeight.value));
    await client.invalidateQueries({ queryKey: labKey(ws) });
  } catch (e) {
    if (!currentVisit()) return;
    offline.value = e instanceof ApiError && e.status === 409 && e.body.code === 'no_machine';
    error.value = offline.value ? (drafting.value ? 'Your machine is offline. Your draft is saved; connect your machine to continue.' : 'Your machine is offline') : (e as Error).message;
  } finally {
    if (currentVisit()) sending.value = false;
  }
}
watch(
  () => turns.value.at(-1)?.id,
  async (id) => {
    if (id) {
      await nextTick();
      if (log.value) log.value.scrollTop = log.value.scrollHeight;
    }
  },
);
async function older() {
  const before = log.value?.scrollHeight ?? 0,
    top = log.value?.scrollTop ?? 0;
  await messages.fetchNextPage();
  await nextTick();
  if (log.value) log.value.scrollTop = top + (log.value.scrollHeight - before);
}
function localKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault();
    e.stopPropagation();
    if (menu.value) {
      closeMenu(true);
    } else toggle();
  }
  if (!drafting.value && e.key === '@' && e.target === composer.value && !draft.value) {
    e.preventDefault();
    showMenu('context', composer.value ?? null);
  }
}
function outside(e: Event) {
  if (menu.value && !panel.value?.contains(e.target as Node)) menu.value = '';
}
onMounted(() => {
  if (drafting.value) { placement.setHeight(340); nextTick(() => composer.value?.focus?.()); }
  document.addEventListener('pointerdown', outside);
  window.addEventListener('resize', placeMenu);
  window.addEventListener('resize', resizeFab);
});
onUnmounted(() => {
  alive = false;
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', placeMenu);
  window.removeEventListener('resize', resizeFab);
  stopFabDrag?.();
});
</script>
<template>
  <button
    v-if="!open"
    ref="fab"
    class="aristotle-fab"
    aria-label="Ask Aristotle"
    aria-keyshortcuts="Meta+J Control+J"
    :style="fabStyle"
    @pointerdown="startFabDrag"
    @keydown="moveFabWithKeys"
    @click="clickFab"
  >
    <Aristotle still />
  </button>
  <section
    v-else
    ref="panel"
    :style="style"
    class="lab-window"
    aria-label="Ask Aristotle"
    @keydown="localKey"
  >
    <div class="resize-edge" aria-hidden="true" @pointerdown="placement.start($event, 'resize')" />
    <header>
      <button ref="minimize" class="ib" aria-label="Minimize" title="Minimize" @click="toggle">
        <Minus :size="16" />
      </button>
      <button
        class="title"
        :aria-label="drafting ? 'Draft with Aristotle' : 'Switch chat'"
        :disabled="drafting"
        data-lab-menu="chats"
        :aria-expanded="menu === 'chats'"
        title="Switch chat"
        @click="toggleMenu('chats', $event)"
      >
        <span>{{ title }}</span
        ><ChevronDown v-if="!drafting" :size="13" />
      </button>
      <button
        class="ib grip"
        aria-label="Drag to move · Click for options"
        title="Drag to move · Click for options"
        data-lab-menu="options"
        :aria-expanded="menu === 'options'"
        @pointerdown="placement.start($event, 'drag')"
        @click="!placement.consumeDrag() && toggleMenu('options', $event)"
      >
        <GripVertical :size="16" />
      </button>
    </header>
    <div
      v-if="logHeight > 0 && (chat || focused)"
      ref="log"
      class="log"
      :style="{ height: logHeight + 'px' }"
      @click="menu = ''"
    >
      <button
        v-if="messages.hasNextPage.value"
        class="more"
        :disabled="messages.isFetching.value"
        @click="older"
      >
        Earlier messages
      </button>
      <p v-if="messages.isError.value" role="alert">
        Couldn’t load this conversation. <button @click="messages.refetch()">Retry</button>
      </p>
      <p v-else-if="chat && messages.isPending.value">Loading conversation…</p>
      <template v-else>
        <LabWelcome v-if="!drafting && focused && !turns.length" :can-ask="true" @ask="ask" />
        <template v-for="message in turns" :key="message.id">
          <div v-if="message.kind === 'question'" class="question">{{ message.body }}</div>
          <p v-if="drafting && message.kind === 'answer' && parseManualDecisionProposal(message.proposal)" class="proposal"><strong>Proposal</strong> · {{ parseManualDecisionProposal(message.proposal)?.message }}</p>
          <LabAnswer
            v-if="message.kind === 'answer'"
            :message="message"
            :evidence="evidence"
          />
          <div v-if="message.kind !== 'question' && message.kind !== 'answer'" class="failure">{{ message.body }}</div>
        </template>
        <div v-if="messages.data.value?.pages[0]?.pending" class="working" role="status">
          <RunStatus text="Working…" working />
        </div>
      </template>
    </div>
    <div v-if="context" class="chip">
      <span>{{ context.title }}</span
      ><button aria-label="Remove context" title="Remove context" @click="picked = null">
        <X :size="12" />
      </button>
    </div>
    <p v-if="error" class="error" role="alert">
      {{ error }} <RouterLink v-if="offline" to="/machines">Machines</RouterLink>
    </p>
    <p v-if="readonly" class="error">Archived conversation · Read-only</p>
    <form v-else class="composer" @submit.prevent="ask()">
      <button
        type="button"
        class="ib"
        v-if="!drafting"
        title="Add a study, record or file · @"
        aria-label="Add a study, record or file"
        data-lab-menu="context"
        :aria-expanded="menu === 'context'"
        @click="toggleMenu('context', $event)"
      >
        <Plus :size="17" />
      </button>
      <input
        ref="composer"
        :value="draft"
        @input="draft = ($event.target as HTMLInputElement).value"
        aria-label="Question"
        placeholder="Ask Aristotle"
        autocomplete="off"
        @focus="focus"
      />
      <button
        type="button"
        class="ib"
        title="Select model · ⌃⇧M"
        aria-label="Select model"
        data-lab-menu="model"
        :aria-expanded="menu === 'model'"
        @click="toggleMenu('model', $event)"
      >
        <Brain :size="16" />
      </button>
      <button
        type="button"
        class="ib"
        title="Show evidence"
        aria-label="Show evidence"
        :aria-pressed="evidence"
        @click="
          evidence = !evidence;
          menu = '';
          if (evidence && chat) placement.setHeight(Math.max(300, logHeight));
        "
      >
        <CircleDot :size="16" />
      </button>
      <button v-if="drafting && pending" type="button" class="ib" aria-label="Stop drafting" :disabled="cancelling" @click="stopDraft">Stop</button>
      <button
        class="send"
        type="submit"
        aria-label="Ask"
        title="Ask"
        :disabled="sending || !!deleting || !draft.trim() || (drafting && pending)"
      >
        <ArrowUp :size="17" />
      </button>
    </form>
    <div v-if="menu" class="menu" :class="{ 'context-menu': menu === 'context' }" ref="menuBox" tabindex="-1" :style="menuStyle">
      <template v-if="menu === 'options'">
        <button
          @click="
            placement.reset();
            menu = '';
          "
        >
          Move back to the corner
        </button>
        <button
          @click="
            placement.setHeight(440);
            menu = '';
          "
        >
          Show the whole conversation
        </button>
        <button
          @click="
            placement.setHeight(0);
            focused = false;
            menu = '';
          "
        >
          Composer only
        </button>
        <button v-if="!drafting" :disabled="sending" @click="switchChat('')">New chat</button>
      </template>
      <template v-else-if="menu === 'chats'">
        <div class="chat-menu-header">
          <small>Recent chats</small>
          <button class="chat-new" aria-label="New chat" title="New chat" :disabled="sending || !!deleting" @click="switchChat('')"><Plus :size="16" /></button>
        </div>
        <div v-for="c in recent" :key="c.id" class="chat-menu-row">
          <button class="chat-select" :disabled="sending || !!deleting" @click="switchChat(c.id, c.title)">{{ c.title || 'New chat' }}</button>
          <button class="chat-delete" :aria-label="'Delete ' + (c.title || 'chat')" title="Delete chat" :disabled="sending || !!deleting" @click="removeChat(c.id)"><Trash2 :size="15" /></button>
        </div>
        <p v-if="deleteError" role="alert" class="chat-delete-error">{{ deleteError }}</p>
        <button v-if="chats.isError.value" @click="chats.refetch()">Retry loading chats</button>
        <button
          v-if="chats.hasNextPage.value"
          :disabled="chats.isFetching.value"
          @click="chats.fetchNextPage()"
        >
          More chats
        </button>
      </template>
      <template v-else-if="menu === 'model'">
        <small>Your machines · Select model</small>
        <button v-for="cli in clis" :key="cli" @click="chooseModel('', cli)">
          {{ cli === 'claude' ? 'Claude Code' : 'Codex' }} · Default {{ harness === cli && !model ? '✓' : '' }}
        </button>
        <button v-for="m in models" :key="m.value" @click="chooseModel(m.value)">
          {{ m.label || m.value }} {{ m.value === model ? '✓' : '' }}
        </button>
        <RouterLink v-if="!clis.length" to="/machines">Connect a machine</RouterLink>
      </template>
      <LabContextPicker v-else @pick="pick" />
    </div>
  </section>
</template>
<style scoped>
.aristotle-fab,
.lab-window {
  position: fixed;
  z-index: 40;
  color: var(--text);
  box-sizing: border-box;
  box-shadow:
    0 6px 28px color-mix(in srgb, var(--text) 18%, transparent),
    0 1px 4px color-mix(in srgb, var(--text) 16%, transparent);
}
.aristotle-fab {
  right: max(20px, env(safe-area-inset-right));
  bottom: max(20px, env(safe-area-inset-bottom));
  width: 48px;
  height: 48px;
  border: 2px solid var(--surface);
  background: var(--palette-accent);
  border-radius: 50%;
  display: grid;
  place-items: center;
  padding: 0;
  cursor: grab;
  touch-action: none;
}
.aristotle-fab:active { cursor: grabbing; }
.aristotle-fab:hover {
  border-color: var(--primary);
}
.aristotle-fab .aristotle-art {
  width: 31px;
  height: 44px;
}
.lab-window {
  width: min(416px, calc(100vw - 16px));
  max-height: calc(100dvh - 16px);
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 26px;
  font-size: 13px;
  transform-origin: bottom right;
  animation: open-lab 160ms ease-out;
}
@keyframes open-lab {
  from {
    opacity: 0;
    transform: scale(0.95);
  }
}
@media (prefers-reduced-motion: reduce) {
  .lab-window {
    animation: none;
  }
}
.lab-window button {
  font: inherit;
  color: inherit;
  cursor: pointer;
}
.lab-window button:disabled {
  cursor: default;
}
.lab-window :is(button, a, input, summary):focus-visible,
.aristotle-fab:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 1px;
}
.resize-edge {
  position: absolute;
  left: 24px;
  right: 24px;
  top: -4px;
  height: 9px;
  cursor: ns-resize;
  touch-action: none;
}
header,
.composer {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 10px;
  flex: none;
}
header {
  height: 46px;
}
.composer {
  height: 56px;
  border-top: 1px solid var(--border);
}
.ib,
.send,
.title,
.chip button {
  display: grid;
  place-items: center;
  background: none;
  border: 0;
}
.ib {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  color: var(--muted) !important;
  flex: none;
}
.ib:hover,
.title:hover,
.chip button:hover {
  background: var(--input);
}
.ib[aria-pressed='true'] {
  background: color-mix(in srgb, var(--primary) 14%, var(--surface));
  color: var(--primary-text) !important;
}
.title {
  display: flex;
  gap: 6px;
  max-width: 75%;
  padding: 5px 9px;
  border-radius: 9px;
  font-weight: 500 !important;
}
.title span,
.chip span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.grip {
  margin-left: auto;
  cursor: grab !important;
  touch-action: none;
}
.grip:active {
  cursor: grabbing !important;
}
.log {
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  padding: 10px 16px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  border-top: 1px solid var(--border);
  box-sizing: border-box;
}
.log > * {
  flex: none;
}
.working {
  color: var(--muted);
  font-size: var(--type-small);
  line-height: 1.45;
}
.log p,
.error {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
}
.question {
  align-self: flex-end;
  max-width: 80%;
  border-radius: 14px;
  background: var(--input);
  padding: 7px 11px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.failure {
  color: var(--muted);
}
.log button.more,
.failure button,
.log p button {
  border: 0;
  background: var(--input);
  border-radius: 6px;
  color: var(--primary-text);
  padding: 4px 7px;
}
.chip {
  display: flex;
  align-items: center;
  gap: 5px;
  align-self: flex-start;
  max-width: calc(100% - 32px);
  margin: 5px 16px;
  color: var(--muted);
  background: var(--input);
  border-radius: 6px;
  font-size: 11px;
  padding: 2px 5px;
}
.chip button {
  padding: 2px;
  color: var(--muted);
}
.error {
  padding: 6px 16px;
}
.error a,
.menu a {
  color: var(--primary-text);
}
.composer input {
  width: 0;
  flex: 1;
  min-width: 0;
  border: 0;
  outline: none;
  box-shadow: none;
  background: none;
  color: var(--text);
  font: inherit;
  font-size: 14px;
  padding: 6px;
}
.lab-window .composer input:focus-visible {
  outline: none;
  box-shadow: none;
}
.composer input::placeholder {
  color: var(--muted);
}
.send {
  width: 36px;
  height: 36px;
  flex: none;
  border-radius: 50%;
  background: var(--primary);
  color: var(--on-primary) !important;
}
.send:disabled {
  opacity: 0.78;
}
.menu {
  position: absolute;
  z-index: 2;
  box-sizing: border-box;
  right: 10px;
  width: min(270px, calc(100% - 20px));
  display: grid;
  max-height: min(300px, calc(100dvh - 16px));
  overflow: auto;
  overscroll-behavior: contain;
  padding: 5px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: 0 5px 20px color-mix(in srgb, var(--text) 16%, transparent);
}
.menu.context-menu {
  right: auto;
  left: 10px;
}
.menu small,
.menu a {
  padding: 5px 9px;
}
.menu small {
  color: var(--muted);
  font-size: 11px;
}
.menu :deep(button) {
  display: flex;
  align-items: center;
  gap: 6px;
  border: 0;
  border-radius: 7px;
  padding: 7px 10px;
  background: none;
  color: var(--text);
  text-align: left;
  font: inherit;
  cursor: pointer;
}
.menu :deep(button:hover) {
  background: var(--input);
}
.chat-menu-header, .chat-menu-row { display: flex; align-items: center; min-width: 0; }
.chat-menu-header small { flex: 1; }
.menu .chat-new, .menu .chat-delete { flex: none; justify-content: center; width: 30px; height: 30px; padding: 0; color: var(--muted); }
.chat-menu-row .chat-select { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.chat-delete { opacity: 0; }
.chat-menu-row:hover .chat-delete, .chat-menu-row:focus-within .chat-delete { opacity: 1; }
@media (hover: none) { .chat-delete { opacity: 1; } }
.chat-delete-error { margin: 5px 9px; color: var(--danger-text); font-size: 12px; }
.menu button:disabled {
  color: var(--muted);
  cursor: default;
}
</style>
