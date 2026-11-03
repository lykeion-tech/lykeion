<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { ArrowUp, Brain, ChevronDown, CircleDot, GripVertical, Minus, Plus, X } from '@lucide/vue';
import Aristotle from '../coresearch/Aristotle.vue';
import { ownOnlineClis } from '../coresearch/api';
import { useRuntimes } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { ApiError } from '../../api/client';
import { askLab, createChat, labKey, useChats, useMessages } from './api';
import { useWindowPlacement } from './useWindowPlacement';
import LabWelcome from './LabWelcome.vue';
import LabAnswer from './LabAnswer.vue';
import LabContextPicker from './LabContextPicker.vue';

// Neither installed CLI currently enforces private lab execution under managed settings.
const labQuestionsAvailable = false;
const unavailableReason =
  'Private questions are unavailable. Claude Code and Codex cannot guarantee that a question stays within the approved tools and AI provider. You can still read past chats.';

const props = defineProps<{ userId: string; studyId?: string; studyTitle?: string }>();
const open = ref(false),
  focused = ref(false),
  evidence = ref(false);
type Menu = '' | 'chats' | 'options' | 'context' | 'model';
const menu = ref<Menu>(''),
  panel = ref<HTMLElement>(),
  fab = ref<HTMLButtonElement>(),
  minimize = ref<HTMLButtonElement>();
const log = ref<HTMLElement>(),
  composer = ref<HTMLInputElement>(),
  chat = ref(''),
  selectedTitle = ref('New chat');
const placement = useWindowPlacement(
  'lykeion.aristotle-window:' + workspaceId.value + ':' + props.userId,
  panel,
);
const { style, logHeight } = placement;
const chats = useChats(() => props.userId, open),
  messages = useMessages(() => props.userId, open, chat);
const recent = computed(() => chats.data.value?.pages.flatMap((page) => page.chats) ?? []);
const title = computed(
  () => recent.value.find((c) => c.id === chat.value)?.title || selectedTitle.value,
);
const turns = computed(() =>
  messages.isError.value
    ? []
    : (messages.data.value?.pages
        .slice()
        .reverse()
        .flatMap((p) => p.messages) ?? []),
);
const currentStudy = computed(() =>
  props.studyId && props.studyTitle
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
const model = ref(''),
  draft = ref(''),
  sending = ref(false),
  error = ref(''),
  offline = ref(false);
const client = useQueryClient(),
  runtimes = useRuntimes();
const healthy = computed(() => (runtimes.data.value ?? []).filter((r) => !r.unhealthy_reason));
const clis = computed(() => ownOnlineClis(healthy.value, props.userId));
const models = computed(() => {
  const values = healthy.value
    .filter((r) => r.owner_id === props.userId && r.status === 'online' && r.harness === 'claude')
    .flatMap((r) => r.models ?? []);
  return Array.from(new Map(values.map((m) => [m.value, m])).values());
});
watch(models, (list) => {
  if (!list.some((m) => m.value === model.value)) model.value = '';
});
function chooseModel(id: string) {
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
function pick(value: { ref: string; title: string }) {
  picked.value = value;
  menu.value = '';
  composer.value?.focus();
}
let alive = true;
async function ask(question = draft.value) {
  const text = question.trim();
  if (sending.value || !text) return;
  draft.value = question;
  error.value = '';
  offline.value = false;
  menu.value = '';
  if (Array.from(text).length > 8000) {
    error.value = 'Questions can contain up to 8,000 characters.';
    return;
  }
  if (!labQuestionsAvailable) {
    return;
  }
  const ws = workspaceId.value;
  const payload = {
    text,
    model: model.value,
    harness: 'claude' as const,
    context_ref: context.value?.ref,
  };
  sending.value = true;
  try {
    if (!chat.value) {
      const { chat: created } = await createChat(ws);
      if (!alive) return;
      chat.value = created.id;
      selectedTitle.value = created.title;
    }
    await askLab(ws, chat.value, payload);
    if (!alive) return;
    if (draft.value === question) draft.value = '';
    focused.value = false;
    placement.setHeight(Math.max(300, logHeight.value));
    await client.invalidateQueries({ queryKey: labKey(ws) });
  } catch (e) {
    if (!alive) return;
    offline.value = e instanceof ApiError && e.status === 409 && e.body.code === 'no_machine';
    error.value = offline.value ? 'Your machine is offline' : (e as Error).message;
  } finally {
    sending.value = false;
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
  if (e.key === '@' && e.target === composer.value && !draft.value) {
    e.preventDefault();
    showMenu('context', composer.value ?? null);
  }
}
function outside(e: Event) {
  if (menu.value && !panel.value?.contains(e.target as Node)) menu.value = '';
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  window.addEventListener('resize', placeMenu);
});
onUnmounted(() => {
  alive = false;
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', placeMenu);
});
</script>
<template>
  <button
    v-if="!open"
    ref="fab"
    class="aristotle-fab"
    aria-label="Ask Aristotle"
    aria-keyshortcuts="Meta+J Control+J"
    @click="toggle"
  >
    <Aristotle still /><span class="fab-tip">Ask Aristotle <kbd>⌘J</kbd></span>
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
        aria-label="Switch chat"
        data-lab-menu="chats"
        :aria-expanded="menu === 'chats'"
        title="Switch chat"
        @click="toggleMenu('chats', $event)"
      >
        <span>{{ title }}</span
        ><ChevronDown :size="13" />
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
        <LabWelcome v-if="focused && !turns.length" @ask="ask" />
        <template v-for="message in turns" :key="message.id">
          <div v-if="message.kind === 'question'" class="question">{{ message.body }}</div>
          <LabAnswer
            v-else-if="message.kind === 'answer'"
            :message="message"
            :evidence="evidence"
          />
          <div v-else class="failure">{{ message.body }}</div>
        </template>
        <p v-if="messages.data.value?.pages[0]?.pending" role="status">
          Reading the lab’s records…
        </p>
      </template>
    </div>
    <div v-if="context" class="chip">
      <span>{{ context.title }}</span
      ><button aria-label="Remove context" title="Remove context" @click="picked = null">
        <X :size="12" />
      </button>
    </div>
    <p v-if="!labQuestionsAvailable" class="error" role="status">{{ unavailableReason }}</p>
    <p v-if="error" class="error" role="alert">
      {{ error }} <RouterLink v-if="offline" to="/machines">Machines</RouterLink>
    </p>
    <form class="composer" @submit.prevent="ask()">
      <button
        type="button"
        class="ib"
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
      <button
        class="send"
        type="submit"
        aria-label="Ask"
        title="Ask"
        :disabled="!labQuestionsAvailable || sending || !draft.trim()"
      >
        <ArrowUp :size="17" />
      </button>
    </form>
    <div v-if="menu" class="menu" ref="menuBox" tabindex="-1" :style="menuStyle">
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
        <button :disabled="sending" @click="switchChat('')">New chat</button>
      </template>
      <template v-else-if="menu === 'chats'">
        <small>Recent chats</small>
        <button :disabled="sending" @click="switchChat('')">New chat</button>
        <button
          v-for="c in recent"
          :key="c.id"
          :disabled="sending"
          @click="switchChat(c.id, c.title)"
        >
          {{ c.title || 'New chat' }}
        </button>
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
        <button :disabled="!labQuestionsAvailable || !clis.includes('claude')" @click="chooseModel('')">
          Claude Code · Default {{ !model ? '✓' : '' }}
        </button>
        <button v-for="m in models" :key="m.value" :disabled="!labQuestionsAvailable" @click="chooseModel(m.value)">
          {{ m.label || m.value }} {{ m.value === model ? '✓' : '' }}
        </button>
        <small>Claude Code and Codex are unavailable for private questions.</small>
        <RouterLink v-if="labQuestionsAvailable && !clis.includes('claude')" to="/machines"
          >Connect a Claude Code machine</RouterLink
        >
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
  background: white;
  border-radius: 50%;
  display: grid;
  place-items: center;
  padding: 0;
  cursor: pointer;
}
.aristotle-fab:hover {
  border-color: var(--primary);
}
.aristotle-fab .aristotle-art {
  width: 31px;
  height: 44px;
}
.fab-tip {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  padding: 5px 10px;
  border-radius: 9px;
  background: var(--text);
  color: var(--surface);
  font-size: 12px;
  line-height: 1.5;
  white-space: nowrap;
  pointer-events: none;
  opacity: 0;
}
.aristotle-fab:is(:hover, :focus-visible) .fab-tip {
  opacity: 1;
}
.fab-tip kbd {
  border-radius: 4px;
  background: color-mix(in srgb, var(--surface) 22%, var(--text));
  padding: 1px 4px;
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
  gap: 10px;
  border-top: 1px solid var(--border);
  box-sizing: border-box;
}
.log > * {
  flex: none;
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
  background: none;
  color: var(--text);
  font: inherit;
  font-size: 14px;
  padding: 6px;
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
.menu button:disabled {
  color: var(--muted);
  cursor: default;
}
</style>
