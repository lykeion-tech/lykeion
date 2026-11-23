<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { Pin } from '@lucide/vue';
import { useRoute, useRouter } from 'vue-router';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { useMe } from '../../api/queries';
import { workspaceId } from '../../api/session';
import type { Research } from '../../api/types';
import { guardResourceDrafts } from '../../state/resource-draft';
import Icon from '../../ui/Icon.vue';
import ProjectPicker from '../projects/ProjectPicker.vue';

type ActionChat = {
  id: string;
  title: string;
  project_id?: string;
  research_id?: string | null;
  pinned_at?: string | null;
  archived_at?: string | null;
};
type Update = {
  title?: string;
  research_id?: string | null;
  new_project_name?: string;
  pinned?: boolean;
  archived?: boolean;
};

const props = withDefaults(
  defineProps<{ conversation: ActionChat; variant?: 'rail' | 'project' }>(),
  {
    variant: 'rail',
  },
);
const emit = defineEmits<{ changed: [] }>();
const client = useQueryClient(),
  route = useRoute(),
  router = useRouter(),
  me = useMe();
const scope = computed(() =>
  [workspaceId.value, me.data.value?.id, props.conversation.id].join(':'),
);
const view = ref<'menu' | 'rename' | 'project'>(),
  busy = ref(false),
  error = ref('');
const trigger = ref<HTMLButtonElement>(),
  panel = ref<HTMLElement>();
const name = ref('');
const panelID = useId();
const projects = useQuery({
  queryKey: computed(() => keys.researches(workspaceId.value)),
  queryFn: async ({ queryKey, signal }) =>
    (await api<Research[] | null>('GET', '/api/researches', undefined, undefined, {
      workspace: queryKey[1],
      signal,
    })) ?? [],
  enabled: computed(() => !!workspaceId.value && view.value === 'project'),
});
function place() {
  const anchor = trigger.value?.getBoundingClientRect?.();
  const box = panel.value?.getBoundingClientRect?.();
  if (!anchor || !box) return;
  const edge = 8,
    gap = 5;
  const left = props.variant === 'rail' ? anchor.left : anchor.right - box.width;
  panel.value!.style.left = `${Math.max(edge, Math.min(left, window.innerWidth - edge - box.width))}px`;
  panel.value!.style.top = `${Math.max(edge, Math.min(anchor.bottom + gap, window.innerHeight - edge - box.height))}px`;
}
function outside(event: PointerEvent) {
  if (busy.value) return;
  if (
    !panel.value?.contains(event.target as Node) &&
    !trigger.value?.contains(event.target as Node)
  )
    close(false);
}
function unlisten() {
  document.removeEventListener('pointerdown', outside);
  if (typeof window !== 'undefined') {
    window.removeEventListener('resize', place);
    window.removeEventListener('scroll', place, true);
  }
}
function close(restore = true) {
  if (panel.value?.matches?.(':popover-open')) panel.value.hidePopover();
  view.value = undefined;
  unlisten();
  if (restore) trigger.value?.focus();
}
async function show(next: NonNullable<typeof view.value>, last = false) {
  if (busy.value) return;
  const first = !view.value;
  error.value = '';
  if (next === 'rename') name.value = props.conversation.title;
  view.value = next;
  await nextTick();
  if (view.value !== next) return;
  if (first) {
    panel.value?.showPopover?.();
    document.addEventListener('pointerdown', outside);
    if (typeof window !== 'undefined') {
      window.addEventListener('resize', place);
      window.addEventListener('scroll', place, true);
    }
  }
  place();
  const options = panel.value?.querySelectorAll?.<HTMLElement>('[role="menuitem"]');
  if (next === 'menu') options?.[last ? options.length - 1 : 0]?.focus();
  else panel.value?.querySelector?.('input')?.focus();
}
function toggle() {
  if (busy.value) return;
  return view.value ? close() : show('menu');
}
function keysForTrigger(event: KeyboardEvent) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  event.preventDefault();
  void show('menu', event.key === 'ArrowUp');
}
function keysForPanel(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    if (!busy.value) close();
  } else if (event.key === 'Tab' && view.value === 'menu') close();
  else if (view.value === 'menu' && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    const buttons = Array.from(
      panel.value?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [],
    );
    if (!buttons.length) return;
    event.preventDefault();
    const index = buttons.indexOf(document.activeElement as HTMLElement);
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? buttons.length - 1
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
    buttons[next]?.focus();
  }
}
watch(error, async () => {
  if (!view.value) return;
  await nextTick();
  place();
});
watch(
  scope,
  () => {
    close(false);
    busy.value = false;
    error.value = '';
  },
  { flush: 'sync' },
);
onBeforeUnmount(unlisten);

function opensChat(chat: ActionChat) {
  return (
    route.params.conversationId === chat.id ||
    (!!chat.project_id && route.params.projectId === chat.project_id)
  );
}
async function mutate(method: 'PATCH' | 'DELETE', update?: Update) {
  if (busy.value) return;
  const chat = props.conversation,
    ws = workspaceId.value,
    visit = scope.value;
  busy.value = true;
  error.value = '';
  try {
    if (method === 'DELETE') {
      if (
        !confirm(
          `Delete “${chat.title}” and its Coresearch files and paper? This cannot be undone.`,
        )
      )
        return;
      if (opensChat(chat) && !(await guardResourceDrafts())) return;
    }
    if (visit !== scope.value) return;
    await api(
      method,
      `/api/coresearch/conversations/${encodeURIComponent(chat.id)}`,
      update,
      undefined,
      {
        workspace: ws,
      },
    );
    if (visit !== scope.value) return;
    close();
    if (method === 'DELETE' && opensChat(chat)) await router.push('/coresearch/new');
    const invalidations = [
      client.invalidateQueries({ queryKey: keys.coresearchConversations(ws) }),
      client.invalidateQueries({ queryKey: keys.researchCoresearchAll(ws) }),
    ];
    if (update?.new_project_name)
      invalidations.push(client.invalidateQueries({ queryKey: keys.researches(ws) }));
    if (method === 'DELETE') {
      invalidations.push(
        client.invalidateQueries({ queryKey: keys.coresearchProjects(ws) }),
        client.invalidateQueries({
          queryKey: ['ws', ws, 'aristotle', me.data.value?.id ?? '', 'bootstrap'],
        }),
      );
    }
    await Promise.all(invalidations);
    if (visit === scope.value) emit('changed');
  } catch (failure) {
    if (visit === scope.value) error.value = (failure as Error).message;
  } finally {
    if (visit === scope.value) busy.value = false;
  }
}
function rename() {
  if (!name.value.trim()) return;
  return mutate('PATCH', { title: name.value.trim() });
}
</script>

<template>
  <span class="chat-actions">
    <button
      ref="trigger"
      type="button"
      class="chat-actions-trigger icon-button"
      :aria-label="`Actions for ${conversation.title}`"
      :aria-controls="view ? panelID : undefined"
      :aria-expanded="!!view"
      aria-haspopup="menu"
      :aria-busy="busy"
      @click.stop="toggle"
      @keydown="keysForTrigger"
    >
      <Icon name="more" :size="15" />
    </button>
    <div
      v-if="view"
      :id="panelID"
      ref="panel"
      popover="manual"
      class="chat-actions-panel"
      :class="{ 'chat-actions-picker': view !== 'menu' }"
      :role="view === 'menu' ? 'menu' : 'dialog'"
      :aria-label="
        view === 'menu' ? 'Chat actions' : view === 'project' ? 'Add to project' : 'Rename chat'
      "
      @keydown="keysForPanel"
    >
      <template v-if="view === 'menu'">
        <button
          v-if="variant === 'project'"
          type="button"
          role="menuitem"
          :disabled="busy"
          @click="mutate('PATCH', { pinned: !conversation.pinned_at })"
        >
          <Pin :size="14" aria-hidden="true" />{{ conversation.pinned_at ? 'Unpin' : 'Pin' }}
        </button>
        <button type="button" role="menuitem" :disabled="busy" @click="show('rename')">
          <Icon name="edit" :size="14" />Rename
        </button>
        <button
          v-if="variant === 'rail'"
          type="button"
          role="menuitem"
          aria-haspopup="dialog"
          :disabled="busy"
          @click="show('project')"
        >
          <Icon name="folder" :size="14" />Add to project<Icon
            name="right"
            :size="13"
            class="chat-actions-end"
          />
        </button>
        <button
          v-else
          type="button"
          role="menuitem"
          :disabled="busy"
          @click="mutate('PATCH', { archived: !conversation.archived_at })"
        >
          <Icon name="archive" :size="14" />{{ conversation.archived_at ? 'Unarchive' : 'Archive' }}
        </button>
        <div class="chat-actions-divider" role="separator" />
        <button
          type="button"
          class="chat-actions-danger"
          role="menuitem"
          :disabled="busy"
          @click="mutate('DELETE')"
        >
          <Icon name="trash" :size="14" />Delete
        </button>
      </template>
      <ProjectPicker
        v-else-if="view === 'project'"
        :projects="projects.data.value"
        :pending="projects.isPending.value"
        :failed="projects.isError.value"
        :busy="busy"
        :current-project="conversation.research_id"
        back-label="Back to chat actions"
        @back="show('menu')"
        @select="(project) => mutate('PATCH', { research_id: project })"
        @create="(name) => mutate('PATCH', { new_project_name: name })"
        @retry="projects.refetch()"
        @resize="place"
      />
      <template v-else>
        <header class="chat-actions-heading">
          <button
            type="button"
            class="icon-button"
            aria-label="Back to chat actions"
            :disabled="busy"
            @click="show('menu')"
          >
            <Icon name="left" :size="15" />
          </button>
          <strong>Rename chat</strong>
        </header>
        <form aria-label="Rename chat" class="chat-actions-form" @submit.prevent="rename">
          <input aria-label="Chat title" v-model="name" maxlength="120" :disabled="busy" />
          <button type="submit" class="button compact" :disabled="busy || !name.trim()">
            Save
          </button>
        </form>
      </template>
      <p v-if="error" class="chat-actions-error" role="alert">{{ error }}</p>
    </div>
  </span>
</template>

<style scoped>
.chat-actions {
  display: inline-flex;
  flex: none;
}
.chat-actions-trigger {
  width: 26px;
  height: 26px;
  color: var(--muted);
}
.chat-actions-panel {
  position: fixed;
  inset: auto;
  margin: 0;
  box-sizing: border-box;
  width: min(182px, calc(100vw - 16px));
  max-height: calc(100vh - 16px);
  overflow: auto;
  padding: 4px;
  color: var(--text);
  background: var(--fill-menu);
  border: 1px solid var(--border);
  border-radius: 9px;
  box-shadow: 0 6px 20px #0002;
  font-size: 13px;
}
.chat-actions-picker {
  width: min(290px, calc(100vw - 16px));
  padding: 8px;
}
.chat-actions-panel button:not(.button) {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 30px;
  width: 100%;
  padding: 4px 8px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: inherit;
  text-align: left;
}
.chat-actions-panel button:hover:not(:disabled),
.chat-actions-panel button:focus-visible {
  background-color: var(--hover);
}
.chat-actions-panel button.chat-actions-danger,
.chat-actions-error {
  color: var(--danger-text);
}
.chat-actions-panel button:disabled:not([aria-pressed='true']) {
  opacity: 0.6;
}
.chat-actions-end {
  margin-left: auto;
  flex-shrink: 0;
}
.chat-actions-divider {
  height: 1px;
  margin: 3px 4px;
  background: var(--border);
}
.chat-actions-heading,
.chat-actions-form {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 0;
}
.chat-actions-heading button.icon-button {
  width: 25px;
  min-height: 25px;
  padding: 4px;
}
.chat-actions-error {
  margin: 5px 8px;
  line-height: 1.4;
  overflow-wrap: anywhere;
}
.chat-actions-picker input {
  box-sizing: border-box;
  min-width: 0;
  width: 100%;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--surface);
  color: var(--text);
  font: inherit;
}
.chat-actions-form button {
  flex: none;
}
</style>
