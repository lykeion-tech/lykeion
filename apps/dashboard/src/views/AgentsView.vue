<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api, ApiError } from '../api/client';
import { useAgent, useAgents, useMe, useWorkspaces } from '../api/queries';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import type { AgentDefinition } from '../api/types';
import Modal from '../components/Modal.vue';
import Icon from '../components/Icon.vue';
const props = defineProps<{ id?: string }>();
const list = useAgents(),
  detail = useAgent(() => props.id ?? ''),
  me = useMe(),
  workspaces = useWorkspaces();
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
const denied = computed(
  () => detail.error.value instanceof ApiError && [403, 404].includes(detail.error.value.status),
);
const agent = computed(() => (denied.value ? undefined : detail.data.value));
const editable = computed(
  () =>
    agent.value &&
    !agent.value.archived_at &&
    (agent.value.owner_id === me.data.value?.id ||
      ['owner', 'admin'].includes(
        workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role ?? '',
      )),
);
watch(
  [workspaceId, () => props.id],
  () => {
    visit++;
    editor.value = false;
    draft.value = empty();
    error.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
watch(
  denied,
  (value) => {
    if (value) {
      editor.value = false;
      draft.value = empty();
      error.value = '';
    }
  },
  { flush: 'sync' },
);
onUnmounted(() => visit++);
function edit() {
  const a = agent.value;
  draft.value = a
    ? {
        name: a.name,
        description: a.description,
        instructions: a.instructions,
        harness: a.harness,
        model: a.model,
        visibility: a.visibility,
      }
    : empty();
  editor.value = true;
  error.value = '';
}
async function save() {
  const ws = workspaceId.value,
    id = props.id,
    v = visit;
  busy.value = true;
  error.value = '';
  try {
    const saved = await api<AgentDefinition>(
      id ? 'PATCH' : 'POST',
      id ? `/api/agents/${id}` : '/api/agents',
      draft.value,
    );
    if (v !== visit || ws !== workspaceId.value) return;
    editor.value = false;
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    if (v !== visit || ws !== workspaceId.value) return;
    if (id) await client.invalidateQueries({ queryKey: keys.agent(ws, id) });
    else await router.push('/agents/' + saved.id);
  } catch (e) {
    if (v === visit) error.value = (e as Error).message;
  } finally {
    if (v === visit) busy.value = false;
  }
}
async function archive() {
  if (!agent.value || !confirm(`Archive ${agent.value.name}? Existing study assignments remain.`))
    return;
  const ws = workspaceId.value,
    id = props.id!,
    v = visit;
  try {
    await api('POST', `/api/agents/${id}/archive`);
    if (v !== visit) return;
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    await client.invalidateQueries({ queryKey: keys.agent(ws, id) });
  } catch (e) {
    if (v === visit) error.value = (e as Error).message;
  }
}
</script>
<template>
  <section class="directory-page">
    <template v-if="id"
      ><RouterLink to="/agents" class="text-button">← Agents</RouterLink>
      <div v-if="agent" class="research-detail">
        <header>
          <h1>{{ agent.name }}</h1>
          <template v-if="editable"
            ><button class="button" @click="edit">Edit agent</button
            ><button class="button" @click="archive">Archive agent</button></template
          >
        </header>
        <p v-if="agent.archived_at" class="muted">Archived</p>
        <p class="description">{{ agent.description || 'No description.' }}</p>
        <dl class="agent-facts">
          <dt>Harness</dt>
          <dd>{{ agent.harness === 'claude' ? 'Claude Code' : 'Codex' }}</dd>
          <dt>Model</dt>
          <dd>{{ agent.model || 'Harness default' }}</dd>
          <dt>Visibility</dt>
          <dd>{{ agent.visibility }}</dd>
        </dl>
        <h2>Instructions</h2>
        <pre class="agent-instructions">{{ agent.instructions || 'No instructions.' }}</pre>
      </div></template
    ><template v-else
      ><div class="directory-toolbar">
        <p class="muted">Agent profiles for study responsibility.</p>
        <button class="button primary" @click="edit">New agent</button>
      </div>
      <div class="directory-list">
        <RouterLink v-for="a in list.data.value ?? []" :key="a.id" :to="'/agents/' + a.id"
          ><span class="avatar">{{ a.name[0] }}</span
          ><span
            >{{ a.name
            }}<small
              >{{ a.harness }} · {{ a.visibility }}{{ a.archived_at ? ' · Archived' : '' }}</small
            ></span
          ><Icon name="right"
        /></RouterLink>
      </div>
      <div v-if="list.isSuccess.value && !list.data.value?.length" class="directory-empty">
        <Icon name="bot" :size="30" />
        <h2>No agents yet</h2>
        <p>Create a profile to assign responsibility for a study.</p>
        <button class="button" @click="edit">Create agent</button>
      </div></template
    >
    <p v-if="id ? detail.error.value : list.error.value" class="inline-error" role="alert">
      {{ (id ? detail.error.value : list.error.value)?.message }}
      <button @click="id ? detail.refetch() : list.refetch()">Retry</button>
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <Modal v-if="editor" :title="id ? 'Edit agent' : 'New agent'" @close="editor = false"
      ><form class="directory-form agent-form" @submit.prevent="save">
        <h2>{{ id ? 'Edit agent' : 'New agent' }}</h2>
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
            {{ id ? 'Save agent' : 'Create agent' }}
          </button>
        </footer>
      </form></Modal
    >
  </section>
</template>
<style src="./directory.css" />
