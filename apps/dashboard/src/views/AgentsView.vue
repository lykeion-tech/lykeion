<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api } from '../api/client';
import { useAgents, useCards, useMe, useMembers } from '../api/queries';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import type { AgentDefinition } from '../api/types';
import Modal from '../components/Modal.vue';
import Icon from '../components/Icon.vue';
import { matchesQuery } from '../lib/search';
import { timeAgo } from '../lib/studies';
defineProps<{ create?: boolean }>();
const list = useAgents(),
  me = useMe(),
  members = useMembers(),
  cards = useCards();
const client = useQueryClient(),
  router = useRouter();
const editor = ref(false),
  busy = ref(false),
  error = ref('');
const empty = () => ({
  name: '',
  description: '',
  instructions: '',
  harness: 'claude' as 'claude' | 'codex',
  model: '',
  visibility: 'workspace' as 'workspace' | 'private',
});
const draft = ref(empty());
let visit = 0;
const tab = ref<'mine' | 'all' | 'archived'>('mine'),
  search = ref(''),
  newestFirst = ref(true);
const inTab = (a: AgentDefinition, t: typeof tab.value) =>
  t === 'archived'
    ? !!a.archived_at
    : !a.archived_at && (t === 'all' || a.owner_id === me.data.value?.id);
const count = (t: typeof tab.value) => (list.data.value ?? []).filter((a) => inTab(a, t)).length;
const rows = computed(() =>
  (list.data.value ?? [])
    .filter((a) => inTab(a, tab.value) && matchesQuery(`${a.name} ${a.description}`, search.value))
    .sort((a, b) => (newestFirst.value ? -1 : 1) * a.updated_at.localeCompare(b.updated_at)),
);
const owner = (id: string) => members.data.value?.find((m) => m.user_id === id);
const studies = (id: string) =>
  (cards.data.value ?? []).filter((c) => c.assignee_type === 'agent' && c.assignee_id === id)
    .length;
watch(workspaceId, () => {
  visit++;
  editor.value = false;
  busy.value = false;
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
  <section class="directory-page">
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
      ><Teleport to="#page-header-count">{{ list.isSuccess.value ? count('all') : '' }}</Teleport
      ><Teleport to="#page-header-actions">
        <button class="button primary" @click="router.push('/agents/new')">
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
        <div class="agent-tabs" role="tablist">
          <button
            v-for="t in ['mine', 'all', 'archived'] as const"
            :key="t"
            role="tab"
            :aria-selected="tab === t"
            @click="tab = t"
          >
            {{ t[0]!.toUpperCase() + t.slice(1) }} <small>{{ count(t) }}</small>
          </button>
        </div>
        <button class="button agent-sort" @click="newestFirst = !newestFirst">
          <Icon :name="newestFirst ? 'down' : 'up'" :size="13" />Last updated
        </button>
      </div>
      <div v-if="list.data.value?.length" class="agent-table" role="table" aria-label="Agents">
        <div class="agent-row agent-head" role="row">
          <span role="columnheader">Agent</span><span role="columnheader">Status</span
          ><span role="columnheader">Owner</span><span role="columnheader">Access</span
          ><span role="columnheader">Harness</span
          ><span role="columnheader" aria-sort="descending">Last updated</span
          ><span role="columnheader">Studies</span>
        </div>
        <RouterLink
          v-for="a in rows"
          :key="a.id"
          :to="'/agents/' + a.id"
          class="agent-row"
          role="row"
          ><span class="agent-cell" role="cell"
            ><span class="avatar">{{ a.name[0] }}</span
            ><span
              ><b
                >{{ a.name }}<Icon v-if="a.visibility === 'private'" name="shield" :size="12" /><i
                  v-if="a.owner_id === me.data.value?.id"
                  class="you-badge"
                  >You</i
                ></b
              ><small>{{ a.description }}</small></span
            ></span
          ><span role="cell" :class="a.archived_at ? 'muted' : 'agent-active'">{{
            a.archived_at ? 'Archived' : 'Active'
          }}</span
          ><span role="cell" class="agent-owner"
            ><span class="avatar small">{{
              (owner(a.owner_id)?.name || owner(a.owner_id)?.email || '?')[0]
            }}</span
            >{{ owner(a.owner_id)?.name || owner(a.owner_id)?.email || 'Former member' }}</span
          ><span role="cell">{{ a.visibility === 'private' ? 'Owner only' : 'Workspace' }}</span
          ><span role="cell">{{ a.harness === 'claude' ? 'Claude Code' : 'Codex' }}</span
          ><span role="cell">{{ timeAgo(a.updated_at) }}</span
          ><span role="cell" class="agent-studies">{{ studies(a.id) }}</span></RouterLink
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
            <option value="claude">Claude Code</option>
            <option value="codex">Codex</option>
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
