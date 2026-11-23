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

const props = defineProps<{ projectId: string; filterAnchor?: HTMLElement }>();
const me = useMe();
const member = computed(() => me.data.value?.id ?? '');
const archived = ref(false);
const selected = ref('');
const back = ref<HTMLButtonElement>();
const filterRoot = ref<HTMLElement>();
const filterOpen = ref(false);
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
    .filter((chat) => Boolean(chat.archived_at) === archived.value)
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
    filterOpen.value = false;
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
async function toggleFilter() {
  filterOpen.value = !filterOpen.value;
  if (!filterOpen.value) return;
  await nextTick();
  filterRoot.value?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus();
}
function closeFilter() {
  filterOpen.value = false;
  focusFilter();
}
function setVisibility(value: boolean) {
  archived.value = value;
  closeFilter();
}
function filterKeys(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    closeFilter();
    return;
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  const options = Array.from(
    filterRoot.value?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? [],
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
function dismissFilter(event: FocusEvent) {
  if (!filterRoot.value?.contains(event.relatedTarget as Node | null)) filterOpen.value = false;
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
    <Teleport v-if="filterAnchor && !selected && chats.data.value?.length" :to="filterAnchor">
      <div ref="filterRoot" class="chat-filter" @focusout="dismissFilter">
        <FilterButton
          :expanded="filterOpen"
          :count="archived ? 1 : 0"
          @click="toggleFilter"
          @keydown.down.prevent="toggleFilter"
        />
        <div
          v-if="filterOpen"
          class="chat-filter-menu"
          role="menu"
          aria-label="Chat visibility"
          @keydown="filterKeys"
        >
          <button
            v-for="option in visibilityOptions"
            :key="option.label"
            type="button"
            role="menuitemradio"
            :aria-checked="archived === option.archived"
            @click="setVisibility(option.archived)"
          >
            <span class="chat-filter-check" aria-hidden="true">
              <Icon v-if="archived === option.archived" name="check" :size="12" />
            </span>
            {{ option.label }}
          </button>
        </div>
      </div>
    </Teleport>
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
        :title="archived ? 'No archived chats' : 'No Coresearches linked'"
        :description="archived ? undefined : 'Start a new chat or add an existing one from Coresearch.'"
      >
        <RouterLink v-if="!archived" to="/coresearch/new" class="button primary compact">
          <Icon name="plus" :size="14" />New chat
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
.chat-filter {
  position: relative;
}
.chat-filter-menu {
  position: absolute;
  z-index: 40;
  top: calc(100% + 4px);
  right: 0;
  min-width: 152px;
  padding: 4px;
  border: 1px solid #ffffff55;
  border-radius: 8px;
  background: var(--fill-menu);
  box-shadow: 0 6px 16px #00000012;
  color: var(--text);
  font-size: 14px;
}
.chat-filter-menu button {
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
.chat-filter-menu button:hover,
.chat-filter-menu button:focus-visible {
  background: var(--hover);
}
.chat-filter-check {
  display: grid;
  width: 16px;
  place-items: center;
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
  width: 100%;
}
.project-coresearch-table :is(th, td) {
  padding: 8px 12px;
}
.project-coresearch-table th {
  border-bottom: 0;
}
.project-coresearch-table th:first-child {
  padding-left: 12px;
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
