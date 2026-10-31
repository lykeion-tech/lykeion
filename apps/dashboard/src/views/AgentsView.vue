<script setup lang="ts">
import Aristotle from '../components/Aristotle.vue';
import AvatarDisplay from '../components/AvatarDisplay.vue';
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api } from '../api/client';
import { useAgents, useMe, useMembers } from '../api/queries';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import type { AgentDefinition } from '../api/types';
import Modal from '../components/Modal.vue';
import Icon from '../components/Icon.vue';
import { backends, backendLabel } from '../lib/backends';
import { matchesQuery } from '../lib/search';
import { timeAgo } from '../lib/studies';
import { agentDisplayName, agentNameWithoutCli } from '../lib/agent-name';
defineProps<{ create?: boolean }>();
const list = useAgents(),
  me = useMe(),
  members = useMembers();
const client = useQueryClient(),
  router = useRouter();
const editor = ref(false),
  busy = ref(false),
  error = ref('');
const empty = () => ({
  name: '',
  description: '',
  instructions: '',
  harness: 'claude',
  model: '',
  visibility: 'workspace' as 'workspace' | 'private',
});
const draft = ref(empty());
let visit = 0;
const tab = ref<'mine' | 'all' | 'archived'>('mine'),
  search = ref(''),
  newestFirst = ref(true);
const filterOpen = ref(false);
const filterAnchor = ref<HTMLElement>();
const filterTrigger = ref<HTMLButtonElement>();
const filterOptions = ['mine', 'all', 'archived'] as const;
function closeFilter(restoreFocus = false) {
  filterOpen.value = false;
  if (restoreFocus) filterTrigger.value?.focus();
}
async function openFilter(position: 'selected' | 'first' | 'last' = 'selected') {
  filterOpen.value = true;
  await nextTick();
  const options = filterAnchor.value?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]');
  if (!options?.length) return;
  const index =
    position === 'first'
      ? 0
      : position === 'last'
        ? options.length - 1
        : filterOptions.indexOf(tab.value);
  options[index]?.focus();
}
function selectFilter(value: typeof tab.value) {
  tab.value = value;
  closeFilter(true);
}
function filterKeys(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    closeFilter(true);
    return;
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  const options = [
    ...(filterAnchor.value?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []),
  ];
  if (!options.length) return;
  event.preventDefault();
  const index = options.indexOf(document.activeElement as HTMLButtonElement);
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? options.length - 1
        : index < 0
          ? event.key === 'ArrowDown'
            ? 0
            : options.length - 1
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
  options[next]?.focus();
}
function leaveFilter(event: FocusEvent) {
  if (!filterAnchor.value?.contains(event.relatedTarget as Node | null)) closeFilter();
}
function outsideFilter(event: PointerEvent) {
  if (!filterAnchor.value?.contains(event.target as Node | null)) closeFilter();
}
onMounted(() => document.addEventListener('pointerdown', outsideFilter));
onUnmounted(() => document.removeEventListener('pointerdown', outsideFilter));
const inTab = (a: AgentDefinition, t: typeof tab.value) =>
  t === 'archived'
    ? !!a.archived_at
    : !a.archived_at && (t === 'all' || a.owner_id === me.data.value?.id);
const count = (t: typeof tab.value) => (list.data.value ?? []).filter((a) => inTab(a, t)).length;
const rows = computed(() =>
  (list.data.value ?? [])
    .filter(
      (a) =>
        inTab(a, tab.value) &&
        matchesQuery(`${a.name} ${agentDisplayName(a.name)} ${a.description}`, search.value),
    )
    .sort((a, b) => (newestFirst.value ? -1 : 1) * a.updated_at.localeCompare(b.updated_at)),
);
const owner = (id: string) => members.data.value?.find((m) => m.user_id === id);
watch(workspaceId, () => {
  visit++;
  editor.value = false;
  busy.value = false;
  closeFilter();
});
onUnmounted(() => visit++);
function edit() {
  draft.value = empty();
  editor.value = true;
  error.value = '';
}
async function save() {
  const ws = workspaceId.value,
    v = visit;
  busy.value = true;
  error.value = '';
  try {
    const saved = await api<AgentDefinition>('POST', '/api/agents', draft.value);
    if (v !== visit || ws !== workspaceId.value) return;
    editor.value = false;
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    if (v !== visit || ws !== workspaceId.value) return;
    await router.push('/agents/' + saved.id);
  } catch (e) {
    if (v === visit) error.value = (e as Error).message;
  } finally {
    if (v === visit) busy.value = false;
  }
}
</script>
<template>
  <section class="directory-page agent-directory">
    <template v-if="create"
      ><header class="agent-create-header">
        <RouterLink to="/agents" class="icon-button" aria-label="Back to agents"
          ><Icon name="back" :size="16"
        /></RouterLink>
        <div>
          <h1>Create an agent</h1>
          <p>Choose a starting point</p>
        </div>
      </header>
      <div class="agent-create-start">
        <small>Agent creation</small>
        <h2>How would you like to start?</h2>
        <div class="agent-create-options">
          <button class="agent-create-option" @click="edit">
            <span class="option-icon"><Icon name="file" :size="18" /></span>
            <strong>Start blank</strong>
            <span>Set the name, instructions, and harness yourself.</span>
            <em>Continue <Icon name="right" :size="12" /></em>
          </button>
          <button class="agent-create-option" disabled>
            <span class="option-icon"><Icon name="chat" :size="18" /></span>
            <strong>Build with AI</strong>
            <span>Create an editable configuration through conversation.</span>
            <em>Not available yet</em>
          </button>
        </div>
      </div></template
    ><template v-else
      ><Teleport to="#page-header-actions">
        <button class="button primary directory-header-action" @click="router.push('/agents/new')">
          <Icon name="plus" :size="14" />New agent
        </button>
      </Teleport>
      <div v-if="list.data.value?.length" class="agent-toolbar">
        <label class="agent-search"
          ><Icon name="search" :size="14" /><input
            v-model="search"
            placeholder="Search agents..."
            aria-label="Search agents"
        /></label>
        <div ref="filterAnchor" class="agent-filter menu-anchor" @focusout="leaveFilter">
          <button
            ref="filterTrigger"
            class="button"
            :class="{ active: tab !== 'mine' }"
            aria-haspopup="menu"
            :aria-expanded="filterOpen"
            aria-controls="agent-filter-menu"
            @click="filterOpen ? closeFilter(true) : openFilter()"
            @keydown.down.prevent="openFilter('first')"
            @keydown.up.prevent="openFilter('last')"
          >
            <Icon name="filter" :size="14" />Filter
          </button>
          <div
            v-if="filterOpen"
            id="agent-filter-menu"
            class="agent-filter-menu"
            role="menu"
            aria-label="Filter agents"
            @keydown="filterKeys"
          >
            <button
              v-for="t in filterOptions"
              :key="t"
              role="menuitemradio"
              :aria-checked="tab === t"
              tabindex="-1"
              @click="selectFilter(t)"
            >
              <span class="filter-check"><Icon v-if="tab === t" name="check" :size="12" /></span
              ><span>{{ t[0]!.toUpperCase() + t.slice(1) }}</span
              ><small>{{ count(t) }}</small>
            </button>
          </div>
        </div>
        <button class="button agent-sort" @click="newestFirst = !newestFirst">
          <Icon :name="newestFirst ? 'down' : 'up'" :size="13" />Last updated
        </button>
      </div>
      <div v-if="list.data.value?.length" class="agent-table" role="table" aria-label="Agents">
        <div class="agent-row agent-head" role="row">
          <span role="columnheader">Name</span><span role="columnheader">Status</span
          ><span role="columnheader">Owner</span><span role="columnheader">Access</span
          ><span role="columnheader">Harness</span
          ><span role="columnheader" aria-sort="descending">Last updated</span>
        </div>
        <RouterLink
          v-for="a in rows"
          :key="a.id"
          :to="'/agents/' + a.id"
          class="agent-row"
          role="row"
          ><span class="agent-cell" role="cell"
            ><span class="avatar"
              ><AvatarDisplay
                v-if="a.avatar_url"
                :value="a.avatar_url.slice(6)"
                :size="20"
              /><Aristotle v-else-if="a.built_in_key === 'aristotle'" still /><template v-else>{{
                agentDisplayName(a.name)[0]
              }}</template></span
            ><span
              ><b
                >{{ agentNameWithoutCli(a.name, a.harness)
                }}<i v-if="a.owner_id === me.data.value?.id" class="you-badge">You</i></b
              ><small>{{ a.description }}</small></span
            ></span
          ><span role="cell" :class="a.archived_at ? 'muted' : 'agent-active'">{{
            a.archived_at ? 'Archived' : 'Active'
          }}</span
          ><span role="cell" class="agent-owner"
            ><span class="avatar small">{{
              (owner(a.owner_id)?.name || owner(a.owner_id)?.email || '?')[0]
            }}</span
            ><span
              class="agent-owner-name"
              :title="owner(a.owner_id)?.name || owner(a.owner_id)?.email || 'Former member'"
              >{{ owner(a.owner_id)?.name || owner(a.owner_id)?.email || 'Former member' }}</span
            ></span
          ><span role="cell">{{ a.visibility === 'private' ? 'Owner only' : 'Workspace' }}</span
          ><span role="cell">{{ backendLabel(a.harness) }}</span
          ><span role="cell"
            ><span class="directory-last-value">{{ timeAgo(a.updated_at) }}</span></span
          ></RouterLink
        >
        <p v-if="!rows.length" class="agent-none">No matching agents</p>
      </div>
      <div v-if="list.isSuccess.value && !list.data.value?.length" class="directory-empty">
        <Icon name="bot" :size="30" />
        <h2>No agents yet</h2>
        <p>Create a profile to assign responsibility for a study.</p>
        <button class="button" @click="router.push('/agents/new')">Create agent</button>
      </div></template
    >
    <p v-if="list.error.value" class="inline-error" role="alert">
      {{ list.error.value.message }}
      <button @click="list.refetch()">Retry</button>
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <Modal v-if="editor" :title="'New agent'" @close="editor = false"
      ><form class="directory-form agent-form" @submit.prevent="save">
        <h2>New agent</h2>
        <label
          >Name<input v-model="draft.name" required maxlength="64" aria-label="Agent name" /></label
        ><label
          >Description<textarea
            v-model="draft.description"
            maxlength="1000"
            aria-label="Agent description"
          /></label
        ><label
          >Harness<select v-model="draft.harness" aria-label="Harness">
            <option v-for="backend in backends" :key="backend.id" :value="backend.id">
              {{ backend.label }}
            </option>
          </select></label
        ><label
          >Model<input
            v-model="draft.model"
            aria-label="Model"
            placeholder="Harness default" /></label
        ><label
          >Visibility<select v-model="draft.visibility" aria-label="Visibility">
            <option value="workspace">Workspace</option>
            <option value="private">Private</option>
          </select></label
        ><label
          >Instructions<textarea
            v-model="draft.instructions"
            aria-label="Agent instructions"
            class="instructions-input"
          />
        </label>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <footer>
          <button type="button" class="button" @click="editor = false">Cancel</button
          ><button class="button primary" :disabled="busy || !draft.name.trim()">
            Create agent
          </button>
        </footer>
      </form></Modal
    >
  </section>
</template>
<style src="./directory.css" />

<style scoped>
.agent-directory .agent-row > :not(:first-child) {
  text-align: center;
}
.agent-directory {
  --directory-columns: minmax(240px, 1fr) repeat(4, 122px) 128px;
}
.agent-directory .agent-row > :last-child {
  text-align: right;
}
.agent-directory .directory-last-value {
  display: inline-block;
  width: 80px;
  text-align: center;
}
.agent-directory .agent-owner {
  justify-content: center;
}
.agent-form {
  width: 100%;
  max-width: none;
  margin: 0;
}

.agent-directory {
  padding: 4px 4px 32px 20px;
}
.agent-filter {
  margin-left: auto;
}
.agent-sort {
  margin-left: 0;
}
.agent-filter-menu {
  position: absolute;
  z-index: 40;
  top: calc(100% + 4px);
  right: 0;
  min-width: 152px;
  padding: 4px;
  background: var(--fill-menu);
  border: 1px solid #ffffff55;
  border-radius: 8px;
  box-shadow: 0 6px 16px #00000012;
  color: var(--text);
  font-size: 14px;
}
.agent-filter-menu > button {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  min-height: 28px;
  padding: 4px 6px;
  text-align: left;
  border-radius: 4px;
  white-space: nowrap;
}
.agent-filter-menu > button:hover,
.agent-filter-menu > button:focus-visible {
  background: #00000008;
  outline: 0;
}
.filter-check {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 12px;
  flex: none;
}
.agent-filter-menu small {
  margin-left: auto;
  color: var(--muted);
  font-size: 12px;
}
</style>

<style scoped>
.avatar :deep(.aristotle-art) {
  width: 28px;
  height: 28px;
}
</style>
