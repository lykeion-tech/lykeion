<script setup lang="ts">
import { computed, nextTick, ref, watch, onMounted, onUnmounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { Pin, ListX } from '@lucide/vue';
import Icon from '../../ui/Icon.vue';
import { useWorkspaceTabs, type Tab } from '../../state/workspace-tabs';
const props = defineProps<{ title: string; icon: string; collapsed: boolean }>();
const emit = defineEmits<{ toggle: [] }>();
const route = useRoute();
const router = useRouter();
const { tabs, selected, openTab: add, ensureTab } = useWorkspaceTabs();
ensureTab(route.fullPath, props.title, props.icon);
const active = computed(() => tabs.value.find((t) => t.id === selected.value)!);
let navigating = false;
const context = ref<{ tabId: number; x: number; y: number } | null>(null);
const contextMenu = ref<HTMLElement>();
const contextTab = computed(() => tabs.value.find((t) => t.id === context.value?.tabId));
const canCloseOthers = computed(() =>
  tabs.value.some((t) => t.id !== contextTab.value?.id && !t.pinned),
);
function record() {
  if (navigating) return;
  const t = active.value;
  if (t.path !== route.fullPath) {
    t.history = t.history.slice(0, t.index + 1);
    if (t.path === '/coresearch' && route.path.startsWith('/coresearch/')) {
      t.history[t.index] = route.fullPath;
    } else {
      t.history.push(route.fullPath);
      t.index = t.history.length - 1;
    }
  }
  t.path = route.fullPath;
  t.title = props.title;
  t.icon = props.icon;
}
watch(() => [route.fullPath, props.title], record, { flush: 'post', immediate: true });
// A tab opened from anywhere, the new-tab button included, is selected first and shown here.
watch(selected, () => {
  if (!navigating) void select(active.value);
});
async function select(t: Tab) {
  context.value = null;
  navigating = true;
  selected.value = t.id;
  try {
    await router.push(t.path);
  } finally {
    navigating = false;
  }
}
async function history(delta: number) {
  const t = active.value;
  let index = t.index + delta;
  if (delta < 0 && t.history[index] === '/coresearch' && t.path.startsWith('/coresearch/'))
    index--;
  if (index < 0 || index >= t.history.length) return;
  t.index = index;
  t.path = t.history[index]!;
  await select(t);
  t.title = props.title;
  t.icon = props.icon;
}
function close(t: Tab) {
  if (t.pinned || tabs.value.length === 1) return;
  context.value = null;
  const i = tabs.value.indexOf(t);
  tabs.value.splice(i, 1);
  if (t.id === selected.value) void select(tabs.value[Math.max(0, i - 1)]!);
}
function closeOthers() {
  const t = contextTab.value;
  if (!t) return;
  tabs.value = tabs.value.filter((item) => item.id === t.id || item.pinned);
  context.value = null;
  if (!tabs.value.some((item) => item.id === selected.value)) void select(t);
  else focusTab(t.id);
}
function focusTab(id: number) {
  void nextTick(() => document.getElementById('workspace-tab-' + id)?.focus());
}
function pinTab() {
  const t = contextTab.value;
  if (!t) return;
  t.pinned = !t.pinned;
  context.value = null;
  focusTab(t.id);
}
async function openContext(t: Tab, event: MouseEvent | KeyboardEvent) {
  event.preventDefault();
  const target = event.currentTarget as HTMLElement;
  const bounds = target.getBoundingClientRect();
  const x =
    event instanceof MouseEvent ? event.clientX : bounds.left + Math.min(bounds.width / 2, 60);
  const y = event instanceof MouseEvent ? event.clientY + 4 : bounds.bottom + 4;
  context.value = {
    tabId: t.id,
    x: Math.min(Math.max(x, 4), window.innerWidth - 156),
    y: Math.min(Math.max(y, 4), window.innerHeight - 112),
  };
  await nextTick();
  contextMenu.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
}
function contextKey(event: KeyboardEvent) {
  const buttons = [
    ...(contextMenu.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') || []),
  ];
  let i = buttons.indexOf(document.activeElement as HTMLButtonElement);
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault();
    if (event.key === 'Home') i = 0;
    else if (event.key === 'End') i = buttons.length - 1;
    else i = (i + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
    buttons[i]?.focus();
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    const id = context.value?.tabId;
    context.value = null;
    if (id) focusTab(id);
  }
  if (event.key === 'Tab') context.value = null;
}
function tabKey(event: KeyboardEvent, t: Tab) {
  if (event.shiftKey && event.key === 'F10') {
    void openContext(t, event);
    return;
  }
  if (
    ['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key) &&
    !event.metaKey &&
    !event.ctrlKey
  ) {
    event.preventDefault();
    let i = tabs.value.indexOf(t);
    if (event.key === 'Home') i = 0;
    else if (event.key === 'End') i = tabs.value.length - 1;
    else i = (i + (event.key === 'ArrowRight' ? 1 : -1) + tabs.value.length) % tabs.value.length;
    const next = tabs.value[i]!;
    void select(next);
    focusTab(next.id);
  }
}
function key(e: KeyboardEvent) {
  if (!(e.metaKey || e.ctrlKey) || document.querySelector('dialog[open]')) return;
  const letter = e.key.toLowerCase();
  if (letter === 't') {
    e.preventDefault();
    add();
  }
  if (letter === 'w') {
    e.preventDefault();
    close(active.value);
  }
  if (e.key === '[') {
    e.preventDefault();
    void history(-1);
  }
  if (e.key === ']') {
    e.preventDefault();
    void history(1);
  }
  if (e.ctrlKey && e.key === 'Tab') {
    e.preventDefault();
    const next =
      tabs.value[
        (tabs.value.indexOf(active.value) + (e.shiftKey ? -1 : 1) + tabs.value.length) %
          tabs.value.length
      ]!;
    void select(next);
    focusTab(next.id);
  }
}
function outside(event: PointerEvent) {
  if (!(event.target as HTMLElement).closest('.tab-context-menu')) context.value = null;
}
const dragged = ref<number | null>(null);
function drop(t: Tab) {
  const from = tabs.value.findIndex((x) => x.id === dragged.value);
  const to = tabs.value.indexOf(t);
  if (from >= 0) {
    const item = tabs.value.splice(from, 1)[0]!;
    tabs.value.splice(to, 0, item);
  }
  dragged.value = null;
}
onMounted(() => {
  if (active.value.path !== route.fullPath) void select(active.value);
  window.addEventListener('keydown', key);
  window.addEventListener('pointerdown', outside);
});
onUnmounted(() => {
  window.removeEventListener('keydown', key);
  window.removeEventListener('pointerdown', outside);
});
</script>
<template>
  <header class="workspace-tabs">
    <div class="window-navigation">
      <button
        class="icon-button"
        aria-label="Go back"
        :disabled="active.index === 0"
        @click="history(-1)"
      >
        <Icon name="back" :size="14" /></button
      ><button
        class="icon-button"
        aria-label="Go forward"
        :disabled="active.index === active.history.length - 1"
        @click="history(1)"
      >
        <Icon name="forward" :size="14" /></button
      ><button class="icon-button" aria-label="Toggle left sidebar" @click="emit('toggle')">
        <Icon name="sidebar" :size="15" />
      </button>
    </div>
    <div class="tab-strip" role="tablist" aria-label="Workspace tabs">
      <div
        v-for="t in tabs"
        :key="t.id"
        class="workspace-tab"
        :class="{ active: t.id === selected, pinned: t.pinned, 'short-title': t.title.length <= 10 }"
        draggable="true"
        @dragstart="dragged = t.id"
        @dragover.prevent
        @drop="drop(t)"
        @contextmenu="openContext(t, $event)"
      >
        <button
          :id="'workspace-tab-' + t.id"
          role="tab"
          :tabindex="t.id === selected ? 0 : -1"
          :aria-selected="t.id === selected"
          :aria-label="t.title + (t.pinned ? ' · pinned' : '')"
          aria-haspopup="menu"
          @click="select(t)"
          @keydown="tabKey($event, t)"
        >
          <Icon :name="t.icon" :size="13" /><span>{{ t.title }}</span
          ><Pin v-if="t.pinned" class="pinned-marker" :size="11" :stroke-width="1.7" /></button
        ><button
          v-if="tabs.length > 1 && !t.pinned"
          class="tab-control"
          aria-label="Close tab"
          @click="close(t)"
        >
          <Icon name="close" :size="12" />
        </button>
      </div>
      <button class="icon-button new-tab" aria-label="New tab" @click="add()">
        <Icon name="plus" :size="15" />
      </button>
    </div>
  </header>
  <div
    v-if="context && contextTab"
    ref="contextMenu"
    class="tab-context-menu"
    role="menu"
    aria-label="Tab actions"
    :style="{ left: context.x + 'px', top: context.y + 'px' }"
    @keydown="contextKey"
  >
    <button role="menuitem" @click="pinTab">
      <Pin :size="15" :stroke-width="1.7" />{{ contextTab.pinned ? 'Unpin tab' : 'Pin tab' }}
    </button>
    <div class="menu-separator" />
    <button
      role="menuitem"
      class="close-action"
      :disabled="contextTab.pinned || tabs.length === 1"
      @click="close(contextTab)"
    >
      <Icon name="close" :size="15" />Close tab</button
    ><button role="menuitem" class="close-action" :disabled="!canCloseOthers" @click="closeOthers">
      <ListX :size="15" :stroke-width="1.7" />Close other tabs
    </button>
  </div>
</template>
<style scoped>
.workspace-tabs {
  position: absolute;
  inset: 0 0 auto;
  height: 62px;
  display: flex;
  align-items: center;
  -webkit-app-region: drag;
}
.window-navigation {
  width: 224px;
  flex: none;
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  padding-right: 12px;
}
.window-navigation button,
.tab-strip {
  -webkit-app-region: no-drag;
}
.tab-strip {
  display: flex;
  align-items: center;
  gap: 5px;
  overflow: auto;
  max-width: calc(100% - 224px);
  padding: 2px 10px;
}
.workspace-tab {
  position: relative;
  width: 94px;
  display: flex;
  align-items: center;
  height: 30px;
  border-radius: 5px;
  color: var(--muted);
  flex: none;
}
.workspace-tab.active {
  background: var(--selected);
  color: var(--text);
}
.workspace-tab.short-title {
  width: max-content;
  min-width: 94px;
  max-width: 152px;
}
.workspace-tab > button[role='tab'] {
  display: flex;
  align-items: center;
  gap: 7px;
  height: 30px;
  flex: 1;
  min-width: 0;
  padding: 0 10px;
  font-size: 14px;
  line-height: 20px;
}
.workspace-tab.short-title > button[role='tab'],
.workspace-tab:has(.tab-control):hover > button[role='tab'],
.workspace-tab:has(.tab-control:focus-visible) > button[role='tab'] {
  padding-right: 24px;
}
.workspace-tab span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.workspace-tab .icon {
  display: block;
  flex-shrink: 0;
  color: var(--primary-text);
}
.pinned-marker {
  color: var(--muted);
  flex-shrink: 0;
}
.tab-control {
  position: absolute;
  right: 2px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 18px;
  height: 24px;
  padding: 0;
  line-height: 1;
  opacity: 0;
  pointer-events: none;
}
.workspace-tab:hover .tab-control,
.tab-control:focus-visible {
  opacity: 1;
  pointer-events: auto;
}
.new-tab {
  margin-left: 0;
}
.sidebar-hidden .window-navigation {
  width: 200px;
}
.sidebar-hidden .tab-strip {
  max-width: calc(100% - 200px);
}
.tab-context-menu {
  position: fixed;
  z-index: 80;
  width: 152px;
  border-radius: 7px;
  padding: 4px;
  background: color-mix(in srgb, var(--input) 90%, var(--text) 10%);
  border: 1px solid color-mix(in srgb, var(--border) 75%, transparent);
  box-shadow: 0 7px 22px #00000014;
  backdrop-filter: blur(12px);
  font-size: 14px;
  -webkit-app-region: no-drag;
}
.tab-context-menu button {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 28px;
  padding: 4px 6px;
  text-align: left;
  white-space: nowrap;
  border-radius: 4px;
}
.tab-context-menu button:hover:not(:disabled),
.tab-context-menu button:focus-visible {
  background: color-mix(in srgb, var(--surface) 28%, transparent);
  outline: none;
}
.tab-context-menu .close-action {
  color: #d64660;
}
.tab-context-menu button:disabled {
  opacity: 0.5;
}
.menu-separator {
  height: 1px;
  background: color-mix(in srgb, var(--surface) 35%, transparent);
  margin: 4px -4px;
}
</style>
