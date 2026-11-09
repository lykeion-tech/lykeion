<script setup lang="ts">
import AvatarDisplay from '../../ui/AvatarDisplay.vue';
import Aristotle from '../coresearch/Aristotle.vue';
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api } from '../../api/client';
import { useAgents, useMe, useMembers, useRuntimes } from '../../api/queries';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import type { AgentDefinition } from '../../api/types';
import Modal from '../../ui/Modal.vue';
import Icon from '../../ui/Icon.vue';
import ModelSwitcher from '../coresearch/cowrite/ModelSwitcher.vue';
import '../coresearch/l2.css';
import { backends, backendLabel } from '../../lib/backends';
import { matchesQuery } from '../../lib/search';
import { columnName, stages, timeAgo } from '../../lib/studies';
import { agentDisplayName, agentNameWithoutCli } from '../../lib/agent-name';
import { agentAvailable as isAgentAvailable } from './availability';
const props = defineProps<{ create?: boolean }>();
const list = useAgents(),
  me = useMe(),
  members = useMembers(),
  runtimes = useRuntimes();
const client = useQueryClient(),
  router = useRouter();
const editor = ref(false),
  busy = ref(false),
  error = ref('');
const directory = ref<HTMLElement>();
const scrollbarWidth = ref(0);
let directoryObserver: ResizeObserver | undefined;
function measureScrollbar() {
  if (directory.value) {
    const width = directory.value.offsetWidth - directory.value.clientWidth;
    scrollbarWidth.value = Number.isFinite(width) ? width : 0;
  }
}
const empty = () => ({
  name: '',
  description: '',
  instructions: '',
  harness: 'claude',
  model: '',
  stage: 'grounding' as string,
  visibility: 'workspace' as 'workspace' | 'private',
});
const draft = ref(empty());
const modelChoices = computed(() => {
  const runtime = runtimes.data.value?.find(
    (r) =>
      r.owner_id === me.data.value?.id &&
      r.status === 'online' &&
      r.harness === draft.value.harness,
  );
  const models = runtime?.models ?? [];
  if (draft.value.harness === 'claude') {
    return [
      { value: '', label: 'Default (Recommended)' },
      ...models.filter((model) => model.value !== '' && model.value !== 'default'),
    ];
  }
  return models.some((model) => model.value === '')
    ? models
    : [{ value: '', label: 'Default' }, ...models];
});
watch(
  () => draft.value.harness,
  () => {
    draft.value.model = '';
  },
);
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
onMounted(() => {
  document.addEventListener('pointerdown', outsideFilter);
  if (directory.value && typeof ResizeObserver !== 'undefined') {
    directoryObserver = new ResizeObserver(measureScrollbar);
    directoryObserver.observe(directory.value);
  }
  measureScrollbar();
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outsideFilter);
  directoryObserver?.disconnect();
});
watch(() => list.data.value?.length, () => nextTick(measureScrollbar));
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
const agentAvailable = (agent: AgentDefinition) =>
  isAgentAvailable(agent, runtimes.data.value ?? [], me.data.value?.id);
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
watch(() => props.create, (create) => {
  if (create) edit();
}, { immediate: true });
function closeEditor() {
  if (busy.value) return;
  editor.value = false;
  if (props.create) void router.replace('/agents');
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
  <section ref="directory" class="directory-page agent-directory" :style="{ '--agent-scrollbar-width': `${scrollbarWidth}px` }">
    <Teleport to="#page-header-actions">
      <button class="button primary directory-header-action" @click="edit">
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
          <Icon name="up" :size="13" :class="{ 'sort-descending': newestFirst }" />Last updated
        </button>
      </div>
      <div v-if="list.data.value?.length" class="agent-table" role="table" aria-label="Agents">
        <div class="agent-row agent-head" role="row">
          <span role="columnheader">Name</span><span role="columnheader">Stage</span
          ><span role="columnheader">Owner</span><span role="columnheader">Harness</span
          ><span role="columnheader" aria-sort="descending"
            ><span class="directory-last-value">Last updated</span></span
          >
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
                v-if="a.built_in_key === 'aristotle' && a.avatar_url"
                :value="a.avatar_url.slice(6)"
                :size="20"
              /><Aristotle v-else-if="a.built_in_key === 'aristotle'" still /><Icon
                v-else-if="a.source === 'built_in' || a.built_in_key"
                name="bot"
                :size="18"
              /><AvatarDisplay
                v-else-if="a.avatar_url"
                :value="a.avatar_url.slice(6)"
                :size="20"
              /><template v-else>{{ agentDisplayName(a.name)[0] }}</template>
              <span v-if="!a.archived_at" class="avatar-active-dot" :class="{ inactive: !agentAvailable(a) }" role="img" :aria-label="agentAvailable(a) ? 'Active' : 'Inactive'" :title="agentAvailable(a) ? 'Active' : 'Inactive'"></span></span
            ><span
              ><b
                >{{ agentNameWithoutCli(a.name, a.harness)
                }}<i
                  v-if="
                    a.owner_id === me.data.value?.id && a.source !== 'built_in' && !a.built_in_key
                  "
                  class="you-badge"
                  >You</i
                ></b
              ><small>{{ a.description }}</small></span
            ></span
          ><span role="cell"
            ><template v-if="a.stage">{{ columnName(a.stage) }}</template
            ><template v-else
              >No stage<button
                v-if="!a.laboratory && a.built_in_key !== 'aristotle'"
                type="button"
                class="text-button agent-choose-stage"
                @click.prevent.stop="router.push({ path: '/agents/' + a.id, query: { tab: 'settings', field: 'stage' } })"
              >
                Choose a stage
              </button></template
            ></span
          ><span role="cell" class="agent-owner"
            ><span class="avatar small">{{
              (owner(a.owner_id)?.name || owner(a.owner_id)?.email || '?')[0]
            }}</span
            ><span
              class="agent-owner-name"
              :title="owner(a.owner_id)?.name || owner(a.owner_id)?.email || 'Former member'"
              >{{ owner(a.owner_id)?.name || owner(a.owner_id)?.email || 'Former member' }}</span
            ></span
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
        <button class="button" @click="edit">Create agent</button>
      </div>
    <p v-if="list.error.value" class="inline-error" role="alert">
      {{ list.error.value.message }}
      <button @click="list.refetch()">Retry</button>
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <Modal v-if="editor" title="Create Agent" @close="closeEditor">
      <form class="directory-form agent-form" @submit.prevent="save">
        <div class="agent-form-heading">
          <div><h2>Create Agent</h2><p>Create a new AI agent for your workspace.</p></div>
          <button type="button" class="icon-button" aria-label="Close" :disabled="busy" @click="closeEditor"><Icon name="close" :size="16" /></button>
        </div>
        <label>Name<input v-model="draft.name" required maxlength="64" aria-label="Agent name" placeholder="e.g. Deep Research Agent" /></label>
        <label>Description<input v-model="draft.description" maxlength="1000" aria-label="Agent description" placeholder="What does this agent do?" /></label>
        <fieldset class="agent-visibility">
          <legend>Visibility</legend>
          <div class="agent-visibility-options">
            <label :class="{ selected: draft.visibility === 'workspace' }"><input v-model="draft.visibility" type="radio" value="workspace" /><span><b>Workspace</b><small>All members can assign</small></span></label>
            <label :class="{ selected: draft.visibility === 'private' }"><input v-model="draft.visibility" type="radio" value="private" /><span><b>Private</b><small>Only you can assign</small></span></label>
          </div>
        </fieldset>
        <label>Stage<select v-model="draft.stage" required aria-label="Agent stage"><option v-for="s in stages" :key="s" :value="s">{{ columnName(s) }}</option></select></label>
        <div class="new-agent-model-field">
          <span>Harness</span>
          <div class="coresearch new-agent-model-picker">
            <ModelSwitcher v-model="draft.harness" :models="backends.map((backend) => ({ value: backend.id, label: backend.label }))" label="Harness" open-up />
          </div>
        </div>
        <div class="new-agent-model-field">
          <span>Model</span>
          <div class="coresearch new-agent-model-picker">
            <ModelSwitcher v-model="draft.model" :models="modelChoices" label="Model" open-up />
          </div>
        </div>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <footer>
          <button type="button" class="button" :disabled="busy" @click="closeEditor">Cancel</button>
          <button class="button primary" :disabled="busy || !draft.name.trim() || !draft.stage">Create</button>
        </footer>
      </form>
    </Modal>
  </section>
</template>
<style src="../../ui/directory.css" />

<style scoped>
.new-agent-model-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.coresearch.new-agent-model-picker {
  height: auto;
  background: transparent;
  --surface-1: var(--surface);
  --surface-2: var(--input);
  --ink: var(--text);
  --ink-subtle: var(--muted);
  --ink-tertiary: var(--muted);
  --hairline-strong: var(--border);
  --type-body: 14px;
  --type-small: 13px;
  --type-meta: 12px;
}
.new-agent-model-picker :deep(.cli-switcher),
.new-agent-model-picker :deep(.cli-pill) {
  width: 100%;
}
.new-agent-model-picker :deep(.cli-pill) {
  justify-content: space-between;
  background: var(--input);
  color: var(--text);
  height: 37px;
  padding: 8px 10px;
  border-radius: 5px;
  font-weight: 400;
}
.coresearch.new-agent-model-picker :deep(.cli-chevron) {
  color: var(--text);
}

.agent-directory .agent-row > :not(:first-child) {
  text-align: center;
}
.agent-directory {
  --directory-columns: minmax(240px, 1fr) repeat(3, 122px) 128px;
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
  gap: 14px;
}
.agent-directory :deep(dialog.modal) {
  width: 440px;
}
.agent-form-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.agent-form-heading p {
  margin-top: 4px;
  color: var(--muted);
  font-size: 13px;
}
.agent-visibility {
  border: 0;
  padding: 0;
  min-width: 0;
}
.agent-visibility legend,
.new-agent-model-field > span {
  color: var(--muted);
  font-size: 13px;
  margin-bottom: 6px;
}
.agent-visibility-options {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.agent-visibility-options label {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: 7px;
  color: var(--text);
  cursor: pointer;
}
.agent-visibility-options label.selected {
  border-color: var(--text);
  background: var(--input);
}
.agent-visibility-options input {
  width: auto;
  flex: none;
}
.agent-visibility-options b,
.agent-visibility-options small {
  display: block;
}
.agent-visibility-options small {
  color: var(--muted);
  font-size: 13px;
  margin-top: 2px;
}

.agent-directory {
  padding: 4px 20px 32px;
}
.agent-filter {
  margin-left: auto;
}
.agent-directory .agent-toolbar {
  padding-inline: 0;
  margin-right: calc(-1 * var(--agent-scrollbar-width, 0px));
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
.agent-cell > .avatar {
  position: relative;
  overflow: visible;
}
.avatar-active-dot {
  position: absolute;
  right: 0;
  bottom: 0;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #39a35a;
}
.avatar-active-dot.inactive {
  background: var(--error);
}
.avatar :deep(.aristotle-art) {
  width: 28px;
  height: 28px;
}
</style>
