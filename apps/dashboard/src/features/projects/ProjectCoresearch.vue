<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api, downloadFile } from '../../api/client';
import { keys } from '../../api/keys';
import { useMe } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { timeAgo } from '../../lib/studies';
import Icon from '../../ui/Icon.vue';
import FilterButton from '../../ui/FilterButton.vue';
import AssistantMessage from '../coresearch/AssistantMessage.vue';
import CellOutput from '../coresearch/CellOutput.vue';
import ConversationActions from '../coresearch/ConversationActions.vue';
import { outputsOf } from '../coresearch/notebook-model';
import ProjectEmptyState from './ProjectEmptyState.vue';

interface ProjectChat {
  id: string;
  title: string;
  mode: 'experiment' | 'cowrite';
  updated_at: string;
  pinned_at: string | null;
  archived_at: string | null;
  is_owner: boolean;
  project_id?: string;
  chat_id?: string;
}

interface SharedChat {
  title: string;
  mode: ProjectChat['mode'];
  messages: { id: string; kind: string; body: string; created_at: string }[];
  artifacts: { id: string; path: string; kind: string }[];
  cells: { id: string; code: string; language: string; position: number; outputs?: unknown }[];
}

const props = defineProps<{ projectId: string }>();
const me = useMe();
const member = computed(() => me.data.value?.id ?? '');
const archived = ref(false);
const selected = ref('');
const back = ref<HTMLButtonElement>();
const filterRoot = ref<HTMLElement>();
const popup = ref<'filter' | 'display'>();
const mode = ref<ProjectChat['mode']>();
const modes = ['experiment', 'cowrite'] as const;
let menuTrigger: HTMLButtonElement | undefined;
const visibilityOptions = [
  { label: 'Active', archived: false },
  { label: 'Archived', archived: true },
];
let rowTrigger: HTMLElement | null = null;
const enabled = computed(() => Boolean(workspaceId.value && member.value && props.projectId));
const base = (project: string) => `/api/researches/${encodeURIComponent(project)}/coresearch`;
const chats = useQuery({
  queryKey: computed(
    () => [...keys.researchCoresearch(workspaceId.value, props.projectId), member.value] as const,
  ),
  enabled,
  queryFn: ({ queryKey, signal }) =>
    api<ProjectChat[]>('GET', base(queryKey[3]), undefined, undefined, {
      workspace: queryKey[1],
      signal,
    }),
});
const hasChats = computed(() => Boolean(chats.data.value?.length));
const showCreate = computed(() => !archived.value && !mode.value);
const detail = useQuery({
  queryKey: computed(
    () =>
      [
        ...keys.researchCoresearchChat(workspaceId.value, props.projectId, selected.value),
        member.value,
      ] as const,
  ),
  enabled: computed(() => enabled.value && !!selected.value),
  queryFn: ({ queryKey, signal }) =>
    api<SharedChat>(
      'GET',
      `${base(queryKey[3])}/${encodeURIComponent(queryKey[4])}`,
      undefined,
      undefined,
      { workspace: queryKey[1], signal },
    ),
});
const visible = computed(() =>
  (chats.data.value ?? [])
    .filter(
      (chat) =>
        Boolean(chat.archived_at) === archived.value && (!mode.value || chat.mode === mode.value),
    )
    .sort(
      (a, b) =>
        Number(Boolean(b.pinned_at)) - Number(Boolean(a.pinned_at)) ||
        b.updated_at.localeCompare(a.updated_at) ||
        a.id.localeCompare(b.id),
    ),
);

watch(
  [workspaceId, member, () => props.projectId],
  () => {
    selected.value = '';
    archived.value = false;
    mode.value = undefined;
    popup.value = undefined;
    menuTrigger = undefined;
    rowTrigger = null;
  },
  { flush: 'sync' },
);

const modeLabel = (mode: ProjectChat['mode']) => (mode === 'cowrite' ? 'Cowrite' : 'Experiment');
const messageLabel = (kind: string) =>
  kind === 'turn'
    ? 'Researcher'
    : kind === 'reply'
      ? 'Assistant'
      : kind === 'comment'
        ? 'Comment'
        : 'Error';

function ownerRoute(chat: ProjectChat) {
  if (!chat.is_owner) return undefined;
  if (chat.mode === 'cowrite' && chat.project_id && chat.chat_id)
    return `/coresearch/${chat.project_id}/${chat.chat_id}`;
  return `/coresearch/conversations/${chat.id}`;
}
function focusFilter() {
  filterRoot.value?.querySelector('button')?.focus();
}
async function toggleMenu(name: 'filter' | 'display', event: Event) {
  if (popup.value === name) {
    closeMenu();
    return;
  }
  const trigger = event.currentTarget as HTMLButtonElement;
  menuTrigger = trigger;
  popup.value = name;
  await nextTick();
  const menu = trigger.parentElement?.querySelector('[role="menu"]');
  (
    menu?.querySelector<HTMLButtonElement>('[aria-checked="true"]') ?? menu?.querySelector('button')
  )?.focus();
}
function closeMenu() {
  popup.value = undefined;
  menuTrigger?.focus();
}
function setVisibility(value: boolean) {
  archived.value = value;
  closeMenu();
}
function setMode(value: ProjectChat['mode']) {
  mode.value = mode.value === value ? undefined : value;
  closeMenu();
}
function menuKeys(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    closeMenu();
    return;
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  const options = Array.from(
    (event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('button'),
  );
  if (!options.length) return;
  event.preventDefault();
  const current = options.indexOf(document.activeElement as HTMLButtonElement);
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? options.length - 1
        : (current + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
  options[next]?.focus();
}
function dismissMenu(name: 'filter' | 'display', event: FocusEvent) {
  const root = event.currentTarget as HTMLElement;
  if (popup.value === name && !root.contains(event.relatedTarget as Node | null))
    popup.value = undefined;
}
async function viewChat(chat: ProjectChat, event: MouseEvent) {
  rowTrigger = event.currentTarget as HTMLElement;
  selected.value = chat.id;
  await nextTick();
  back.value?.focus();
}
async function closeChat() {
  const id = selected.value;
  const trigger = rowTrigger;
  selected.value = '';
  rowTrigger = null;
  await nextTick();
  if (trigger?.isConnected) trigger.focus();
  else {
    const row = document.getElementById(`project-coresearch-open-${id}`);
    if (row) row.focus();
    else focusFilter();
  }
}
function download(file: string) {
  if (detail.isError.value || detail.isFetching.value || !selected.value || !enabled.value) return;
  downloadFile(
    `${base(props.projectId)}/${encodeURIComponent(selected.value)}/artifacts/${encodeURIComponent(file)}`,
    workspaceId.value,
  );
}
</script>

<template>
  <section class="project-coresearch">
    <div v-if="!selected && chats.isSuccess.value" class="study-toolbar">
      <div class="toolbar-group push-right">
        <div ref="filterRoot" class="menu-anchor" @focusout="dismissMenu('filter', $event)">
          <FilterButton
            :expanded="popup === 'filter'"
            :count="archived ? 1 : 0"
            @click="toggleMenu('filter', $event)"
            @keydown.down.prevent="toggleMenu('filter', $event)"
          />
          <div
            v-if="popup === 'filter'"
            class="chat-options"
            role="menu"
            aria-label="Chat visibility"
            @keydown="menuKeys"
          >
            <button
              v-for="option in visibilityOptions"
              :key="option.label"
              type="button"
              role="menuitemradio"
              :aria-checked="archived === option.archived"
              @click="setVisibility(option.archived)"
            >
              {{ option.label }}
              <span class="chat-option-check" aria-hidden="true">
                <Icon v-if="archived === option.archived" name="check" :size="12" />
              </span>
            </button>
          </div>
        </div>
        <div class="menu-anchor" @focusout="dismissMenu('display', $event)">
          <button
            type="button"
            class="button compact"
            :class="{ active: mode }"
            aria-label="Display"
            :aria-description="mode"
            title="Display"
            aria-haspopup="menu"
            :aria-expanded="popup === 'display'"
            @click="toggleMenu('display', $event)"
            @keydown.down.prevent="toggleMenu('display', $event)"
          >
            <Icon name="filters" :size="14" />
          </button>
          <div
            v-if="popup === 'display'"
            class="chat-options"
            role="menu"
            aria-label="Chat mode"
            @keydown="menuKeys"
          >
            <button
              v-for="option in modes"
              :key="option"
              type="button"
              role="menuitemcheckbox"
              :aria-checked="mode === option"
              @click="setMode(option)"
            >
              <span class="chat-option-check" aria-hidden="true">
                <Icon v-if="mode === option" name="check" :size="12" />
              </span>
              {{ option }}
            </button>
          </div>
        </div>
      </div>
    </div>
    <template v-if="selected">
      <header class="coresearch-view-header">
        <button ref="back" type="button" class="button compact" @click="closeChat">
          <Icon name="back" :size="15" /> Back to chats
        </button>
      </header>
      <p v-if="detail.isError.value" class="coresearch-state" role="alert">
        This chat could not be opened.
        <button type="button" class="text-button" @click="detail.refetch()">Try again</button>
      </p>
      <p
        v-else-if="detail.isPending.value || detail.isFetching.value"
        class="coresearch-state"
        role="status"
      >
        Loading chat…
      </p>
      <article v-else-if="detail.data.value" class="shared-chat">
        <header>
          <h2>{{ detail.data.value.title }}</h2>
          <p>{{ modeLabel(detail.data.value.mode) }} · Read only</p>
        </header>
        <section aria-label="Conversation">
          <h3>Conversation</h3>
          <p v-if="!detail.data.value.messages.length">No messages yet.</p>
          <div
            v-for="message in detail.data.value.messages"
            :key="message.id"
            class="shared-message"
          >
            <strong>{{ messageLabel(message.kind) }}</strong>
            <p v-if="message.kind === 'turn'" class="shared-message-body">{{ message.body }}</p>
            <AssistantMessage v-else :text="message.body" redact-local-paths prose />
          </div>
        </section>
        <section v-if="detail.data.value.cells.length" aria-label="Notebook">
          <h3>Notebook</h3>
          <div v-for="cell in detail.data.value.cells" :key="cell.id" class="shared-cell">
            <strong>{{ cell.language }}</strong>
            <pre><code>{{ cell.code }}</code></pre>
            <CellOutput
              v-for="(output, index) in outputsOf(cell.outputs)"
              :key="index"
              :output="output"
            />
          </div>
        </section>
        <section aria-label="Artifacts">
          <h3>Artifacts</h3>
          <p v-if="!detail.data.value.artifacts.length">No saved artifacts.</p>
          <ul v-else class="shared-artifacts">
            <li v-for="artifact in detail.data.value.artifacts" :key="artifact.id">
              <button type="button" class="button compact" @click="download(artifact.id)">
                <Icon name="download" :size="14" />
                <span>{{ artifact.path }}</span>
              </button>
            </li>
          </ul>
        </section>
      </article>
    </template>
    <template v-else>
      <p v-if="chats.isError.value" class="coresearch-state" role="alert">
        Could not load chats.
        <button type="button" class="text-button" @click="chats.refetch()">Try again</button>
      </p>
      <p v-else-if="chats.isPending.value" class="coresearch-state" role="status">Loading chats…</p>
      <ProjectEmptyState
        v-else-if="!visible.length"
        icon="chat"
        :title="
          mode
            ? 'No chats match these filters'
            : archived
              ? 'No archived chats'
              : hasChats
                ? 'No active chats'
                : 'No Coresearches linked'
        "
        :description="
          showCreate ? 'Start a new chat or add an existing one from Coresearch.' : undefined
        "
      >
        <RouterLink v-if="showCreate" to="/coresearch/new" class="button primary compact">
          <Icon name="plus" :size="14" aria-hidden="true" />New chat
        </RouterLink>
      </ProjectEmptyState>
      <table
        v-else
        class="directory-data-table project-coresearch-table"
        aria-label="Coresearch chats"
      >
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Mode</th>
            <th scope="col">Last activity</th>
            <th scope="col" aria-label="Actions"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="chat in visible" :key="chat.id">
            <td>
              <RouterLink
                v-if="ownerRoute(chat)"
                :to="ownerRoute(chat)!"
                class="coresearch-row-link"
              >
                <span>{{ chat.title }}</span>
                <Icon v-if="chat.pinned_at" name="pin" :size="14" aria-label="Pinned" />
              </RouterLink>
              <button
                v-else
                :id="`project-coresearch-open-${chat.id}`"
                type="button"
                class="coresearch-row-link"
                @click="viewChat(chat, $event)"
              >
                <span>{{ chat.title }}</span>
                <Icon v-if="chat.pinned_at" name="pin" :size="14" aria-label="Pinned" />
              </button>
            </td>
            <td>{{ modeLabel(chat.mode) }}</td>
            <td>
              <time :datetime="chat.updated_at">{{ timeAgo(chat.updated_at) }}</time>
            </td>
            <td>
              <ConversationActions
                v-if="chat.is_owner"
                :conversation="{ ...chat, research_id: projectId }"
                variant="project"
              />
            </td>
          </tr>
        </tbody>
      </table>
    </template>
  </section>
</template>

<style scoped>
.project-coresearch {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  overflow: auto;
  color: var(--text);
}
.menu-anchor {
  position: relative;
}
.chat-options {
  position: absolute;
  z-index: 40;
  top: calc(100% + 4px);
  right: 0;
  width: max-content;
  padding: 4px;
  border: 1px solid #ffffff55;
  border-radius: 8px;
  background: var(--fill-menu);
  box-shadow: 0 6px 16px #00000012;
  color: var(--text);
  font-size: 14px;
}
.chat-options button {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  min-height: 28px;
  padding: 4px 6px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
}
.chat-options button:hover,
.chat-options button:focus-visible {
  background: var(--hover);
}
.chat-option-check {
  display: grid;
  width: 16px;
  place-items: center;
}
[role='menuitemradio'] > .chat-option-check {
  margin-inline-start: auto;
}
.coresearch-view-header {
  display: flex;
  gap: 6px;
  padding: 8px 16px 10px;
}
.coresearch-view-header button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.coresearch-state {
  padding: 36px 16px;
  text-align: center;
  color: var(--muted);
}
.project-coresearch-table {
  table-layout: fixed;
  width: calc(100% - 2 * var(--project-view-inset));
  margin-inline: var(--project-view-inset);
}
.project-coresearch-table :is(th, td) {
  padding: 8px 12px;
}
.project-coresearch-table th {
  border-bottom: 0;
}
.project-coresearch-table :is(th, td):first-child {
  padding-inline-start: 0;
}
.project-coresearch-table th:nth-child(2) {
  width: 110px;
}
.project-coresearch-table th:nth-child(3) {
  width: 118px;
}
.project-coresearch-table th:last-child {
  width: 42px;
}
.project-coresearch-table td:last-child {
  padding-inline: 0;
  text-align: center;
}
.coresearch-row-link {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  max-width: 100%;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  text-align: start;
  text-decoration: none;
  cursor: pointer;
}
.coresearch-row-link :deep(svg) {
  flex: none;
}
.coresearch-row-link span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.shared-chat {
  max-width: 820px;
  width: 100%;
  margin: 0 auto;
  padding: 16px 24px 48px;
  overflow-wrap: anywhere;
}
.shared-chat section {
  margin-top: 24px;
}
.shared-chat header p {
  color: var(--muted);
}
.shared-message,
.shared-cell {
  margin: 0 0 10px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 6px;
}
.shared-message-body {
  white-space: pre-wrap;
}
.shared-cell pre {
  overflow: auto;
}
.shared-cell :deep(img) {
  max-width: 100%;
}
.shared-artifacts {
  display: grid;
  gap: 6px;
  padding: 0;
  list-style: none;
}
.shared-artifacts button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  text-align: start;
}
.shared-artifacts button :deep(svg) {
  flex: none;
}
@media (max-width: 680px) {
  .project-coresearch-table :is(th, td) {
    padding-inline: 6px;
  }
  .project-coresearch-table th:nth-child(2) {
    width: 74px;
  }
  .project-coresearch-table th:nth-child(3) {
    width: 92px;
  }
  .project-coresearch-table th:last-child {
    width: 36px;
  }
}
</style>
