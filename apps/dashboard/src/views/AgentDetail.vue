<script setup lang="ts">
import { useRoute } from 'vue-router';
import Aristotle from '../components/Aristotle.vue';
import { defaultStarters, type Starter } from '../coresearch/aristotle';
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../api/client';
import {
  useAgent,
  useCards,
  useMe,
  useMembers,
  useRuntimes,
  useWorkspace,
  useWorkspaces,
} from '../api/queries';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import type { AgentDefinition, Card } from '../api/types';
import Icon from '../components/Icon.vue';
import BackendDetailsDialog from '../components/BackendDetailsDialog.vue';
import { backends, backendLabel, backendDefaultModel } from '../lib/backends';
import { matchesQuery } from '../lib/search';
import { agentConnectorIntegrationAvailable } from '../lib/agent-connectors';
import { boardColumnOf, boardColumns, studyNumber, timeAgo } from '../lib/studies';
import { notify } from '../state/notice';

const props = defineProps<{ id: string }>();
const route = useRoute();
const starters = ref<Starter[]>([]);
const builtin = computed(() => agent.value?.built_in_key === 'aristotle');
const validStarters = computed(
  () =>
    starters.value.length <= 12 &&
    starters.value.every(
      (s) =>
        !!s.label.trim() &&
        [...s.label].length <= 120 &&
        !!s.prompt.trim() &&
        new TextEncoder().encode(s.prompt).length <= 16384 &&
        !s.label.includes('\0') &&
        !s.prompt.includes('\0'),
    ),
);
function requestedTab() {
  return route.query.tab === 'settings' ? 'settings' : 'overview';
}
const detail = useAgent(() => props.id),
  me = useMe(),
  members = useMembers(),
  cards = useCards(),
  workspace = useWorkspace(),
  workspaces = useWorkspaces();
const client = useQueryClient();
const runtimes = useRuntimes();
const backendDetails = ref(false);

const tab = ref<'overview' | 'work' | 'capabilities' | 'settings'>('overview'),
  section = ref<'general' | 'access'>('general'),
  menu = ref(false),
  search = ref(''),
  collapsed = ref<string[]>([]),
  busy = ref(false),
  error = ref('');
const general = ref({ name: '', description: '', harness: 'claude', model: '' }),
  visibility = ref<'workspace' | 'private'>('workspace'),
  instructions = ref('');

const denied = computed(
  () => detail.error.value instanceof ApiError && [403, 404].includes(detail.error.value.status),
);
const agent = computed(() => (denied.value ? undefined : detail.data.value));
const mine = computed(() => agent.value?.owner_id === me.data.value?.id);
const editable = computed(
  () =>
    !!agent.value &&
    !agent.value.archived_at &&
    (mine.value ||
      ['owner', 'admin'].includes(
        workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role ?? '',
      )),
);
const owner = computed(() => {
  const m = members.data.value?.find((x) => x.user_id === agent.value?.owner_id);
  return m ? m.name || m.email : 'Former member';
});
const harnessName = backendLabel;
const assigned = computed(() =>
  (cards.data.value ?? [])
    .filter((c) => c.assignee_type === 'agent' && c.assignee_id === props.id)
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
);
const active = computed(() => assigned.value.filter((c) => !c.ending && (c.queued || c.role)));
const groups = computed(() =>
  boardColumns.map((col) => ({
    ...col,
    cards: assigned.value.filter(
      (c) =>
        boardColumnOf(c) === col.id &&
        matchesQuery(
          `${studyNumber(workspace.data.value, c)} ${c.title} ${c.question}`,
          search.value,
        ),
    ),
  })),
);
const number = (c: Card) => studyNumber(workspace.data.value, c);

// Forms start from the stored agent whenever it changes or a save lands.
watch(
  agent,
  (a: AgentDefinition | undefined) => {
    if (!a) return;
    general.value = {
      name: a.name,
      description: a.description,
      harness: a.harness,
      model: a.model,
    };
    visibility.value = a.visibility;
    instructions.value = a.instructions;
    starters.value = (a.conversation_starters ?? []).map((s) => ({ ...s }));
  },
  { immediate: true },
);
let editGeneration = 0;
onUnmounted(() => {
  editGeneration++;
});
watch(
  [workspaceId, () => props.id, () => me.data.value?.id],
  () => {
    editGeneration++;
    busy.value = false;
    tab.value = requestedTab();
    error.value = '';
    menu.value = false;
    backendDetails.value = false;
  },
  { flush: 'sync' },
);

watch(
  () => route.query.tab,
  () => {
    tab.value = requestedTab();
  },
  { immediate: true },
);

async function patch(body: Record<string, unknown>) {
  const ws = workspaceId.value,
    id = props.id,
    member = me.data.value?.id;
  const generation = ++editGeneration;
  const current = () =>
    generation === editGeneration &&
    ws === workspaceId.value &&
    id === props.id &&
    member === me.data.value?.id;
  busy.value = true;
  error.value = '';
  try {
    const saved = await api<AgentDefinition>('PATCH', `/api/agents/${id}`, body, undefined, {
      workspace: ws,
    });
    if (!current()) return;
    client.setQueryData(keys.agent(ws, id), saved);
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    if (current()) notify('Agent saved.');
  } catch (e) {
    if (current()) error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
async function archive() {
  menu.value = false;
  if (!agent.value || !confirm(`Archive ${agent.value.name}? Existing study assignments remain.`))
    return;
  const ws = workspaceId.value,
    id = props.id;
  try {
    await api('POST', `/api/agents/${id}/archive`);
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    await client.invalidateQueries({ queryKey: keys.agent(ws, id) });
  } catch (e) {
    error.value = (e as Error).message;
  }
}
function outside(event: PointerEvent) {
  if (!(event.target as HTMLElement).closest('.agent-more')) menu.value = false;
}
function toggle(id: string) {
  collapsed.value = collapsed.value.includes(id)
    ? collapsed.value.filter((x) => x !== id)
    : [...collapsed.value, id];
}
onMounted(() => document.addEventListener('pointerdown', outside));
onUnmounted(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <section class="agent-page">
    <template v-if="agent">
      <header class="agent-hero">
        <nav class="agent-crumbs" aria-label="Breadcrumb">
          <RouterLink to="/agents">Agents</RouterLink><span>/</span
          ><span aria-current="page">{{ agent.name }}</span>
        </nav>
        <div class="agent-identity">
          <span class="agent-avatar"
            ><Aristotle v-if="builtin" still /><Icon v-else name="bot" :size="26"
          /></span>
          <div>
            <div class="agent-title">
              <h1>{{ agent.name }}</h1>
              <span class="agent-status" :class="{ archived: agent.archived_at }">{{
                agent.archived_at ? 'Archived' : 'Active'
              }}</span>
            </div>
            <p v-if="agent.description" class="agent-description">{{ agent.description }}</p>
            <p class="agent-meta">
              <span><Icon name="bot" :size="13" />{{ agent.model || 'Harness default' }}</span
              ><span><Icon name="monitor" :size="13" />{{ harnessName(agent.harness) }}</span
              ><span
                ><Icon name="shield" :size="13" />{{
                  agent.visibility === 'private' ? 'Personal' : 'Workspace'
                }}</span
              >
            </p>
          </div>
          <div class="agent-actions">
            <div v-if="editable && !builtin" class="agent-more">
              <button
                class="icon-button"
                aria-label="Agent actions"
                :aria-expanded="menu"
                @click="menu = !menu"
              >
                <Icon name="more" :size="16" />
              </button>
              <div v-if="menu" class="agent-menu" role="menu">
                <button role="menuitem" @click="archive">
                  <Icon name="archive" :size="14" />Archive agent
                </button>
              </div>
            </div>
          </div>
        </div>
      </header>
      <div class="agent-tabs-bar" role="tablist">
        <button
          v-for="t in ['overview', 'work', 'capabilities', 'settings'] as const"
          :key="t"
          role="tab"
          :aria-selected="tab === t"
          @click="tab = t"
        >
          {{ t[0]!.toUpperCase() + t.slice(1) }}
        </button>
      </div>
      <p v-if="error" class="inline-error agent-error" role="alert">{{ error }}</p>

      <div v-if="tab === 'overview'" class="agent-overview">
        <div class="agent-activity">
          <h2>
            Now<small>{{
              active.length ? `${active.length} in progress` : 'No active work reported'
            }}</small>
          </h2>
          <p v-if="!active.length" class="muted">No assigned study is running.</p>
          <RouterLink v-for="c in active" :key="c.id" :to="'/studies/' + c.id" class="agent-study"
            ><small>{{ number(c) }}</small
            ><span>{{ c.title || c.question }}</span
            ><em>{{ c.role || 'Queued' }}</em></RouterLink
          >
          <hr />
          <h2>Recent work</h2>
          <p v-if="!assigned.length" class="muted">No studies assigned yet.</p>
          <RouterLink
            v-for="c in assigned.slice(0, 8)"
            :key="c.id"
            :to="'/studies/' + c.id"
            class="agent-study"
            ><small>{{ number(c) }}</small
            ><span>{{ c.title || c.question }}</span
            ><em>{{ timeAgo(c.updated_at) }}</em></RouterLink
          >
        </div>
        <aside class="agent-panel">
          <h3>Agent</h3>
          <dl>
            <dt>Owner</dt>
            <dd>{{ owner }}</dd>
            <dt>Access</dt>
            <dd>
              <Icon name="shield" :size="12" />{{
                agent.visibility === 'private' ? 'Personal' : 'Workspace'
              }}
            </dd>
            <dt>Harness</dt>
            <dd>{{ harnessName(agent.harness) }}</dd>
            <dt>Model</dt>
            <dd>{{ agent.model || 'Harness default' }}</dd>
          </dl>
          <h3>
            Studies <small>{{ assigned.length }}</small>
          </h3>
          <div class="agent-stats">
            <div>
              <b>{{ active.length }}</b
              ><small>in progress</small>
            </div>
            <div>
              <b>{{ assigned.filter((c) => boardColumnOf(c) === 'blocked').length }}</b
              ><small>blocked</small>
            </div>
            <div>
              <b>{{ assigned.filter((c) => c.board_column === 'done').length }}</b
              ><small>done</small>
            </div>
          </div>
        </aside>
      </div>

      <div v-else-if="tab === 'work'" class="agent-work">
        <div class="agent-work-toolbar">
          <label class="agent-search"
            ><Icon name="search" :size="14" /><input
              v-model="search"
              placeholder="Search studies..."
              aria-label="Search studies"
          /></label>
        </div>
        <section v-for="g in groups" :key="g.id" class="agent-group">
          <button
            class="agent-group-head"
            :class="'group-' + g.id"
            :aria-expanded="!collapsed.includes(g.id)"
            @click="toggle(g.id)"
          >
            <Icon name="down" :size="12" /><span class="stage-ring" />{{ g.name
            }}<small>{{ g.cards.length }}</small>
          </button>
          <template v-if="!collapsed.includes(g.id)">
            <RouterLink
              v-for="c in g.cards"
              :key="c.id"
              :to="'/studies/' + c.id"
              class="agent-study"
              ><small>{{ number(c) }}</small
              ><span>{{ c.title || c.question }}</span
              ><em>{{ timeAgo(c.updated_at) }}</em></RouterLink
            >
            <p v-if="!g.cards.length" class="agent-group-empty">No studies</p>
          </template>
        </section>
      </div>

      <div v-else class="agent-split">
        <nav class="agent-subnav">
          <template v-if="tab === 'capabilities'"
            ><button aria-current="page">Instructions</button
            ><button v-if="agentConnectorIntegrationAvailable">Connectors</button></template
          >
          <template v-else
            ><button
              v-for="s in ['general', 'access'] as const"
              :key="s"
              :aria-current="section === s ? 'page' : undefined"
              @click="section = s"
            >
              {{ s[0]!.toUpperCase() + s.slice(1) }}
            </button></template
          >
        </nav>
        <form
          v-if="tab === 'capabilities'"
          class="agent-pane"
          @submit.prevent="patch({ instructions })"
        >
          <h2>Instructions</h2>
          <p class="agent-lede">
            Set the instructions this agent works from. Markdown is supported.
          </p>
          <label class="agent-label" for="agent-instructions">System prompt</label>
          <textarea
            id="agent-instructions"
            v-model="instructions"
            class="agent-prompt"
            aria-label="Agent instructions"
            :readonly="!editable"
          />
          <footer v-if="editable">
            <button class="button primary" :disabled="busy || instructions === agent.instructions">
              Save
            </button>
          </footer>
        </form>
        <form
          v-else-if="section === 'general'"
          class="agent-pane"
          @submit.prevent="
            validStarters &&
            patch({ ...general, ...(builtin ? { conversation_starters: starters } : {}) })
          "
        >
          <h2>General</h2>
          <h3>Profile</h3>
          <div class="agent-card">
            <label class="agent-field"
              >Name<input
                v-model="general.name"
                required
                maxlength="64"
                aria-label="Agent name"
                :readonly="!editable"
            /></label>
            <label class="agent-field"
              >Description<span>
                <textarea
                  v-model="general.description"
                  maxlength="1000"
                  aria-label="Agent description"
                  :readonly="!editable"
                /><small>{{ general.description.length }} / 1000</small></span
              ></label
            >
          </div>
          <h3>Execution</h3>
          <p class="agent-hint">An empty model uses the harness default.</p>
          <div class="agent-card agent-execution">
            <label
              >Harness<select v-model="general.harness" aria-label="Harness" :disabled="!editable">
                <option v-for="backend in backends" :key="backend.id" :value="backend.id">
                  {{ backend.label }}
                </option>
                <option
                  v-if="!backends.some((backend) => backend.id === general.harness)"
                  :value="general.harness"
                >
                  {{ backendLabel(general.harness) }}
                </option>
              </select></label
            ><label
              >Model<input
                v-model="general.model"
                aria-label="Model"
                placeholder="Harness default"
                :readonly="!editable"
            /></label>
          </div>
          <div class="agent-card" style="padding: 14px 18px; margin-top: 14px">
            <div
              style="display: flex; align-items: center; justify-content: space-between; gap: 12px"
            >
              <b>{{ backendLabel(general.harness) }}</b
              ><button type="button" class="text-button" @click="backendDetails = true">
                Backend details
              </button>
            </div>
            <p class="agent-hint">
              {{
                backendDefaultModel(general.harness)
                  ? `Leave blank to use the adapter default: ${backendDefaultModel(general.harness)}.`
                  : 'Leave blank to use the harness default.'
              }}
            </p>
          </div>
          <template v-if="builtin"
            ><h3>Conversation starters</h3>
            <p class="agent-hint">
              Settings apply to new conversations. Leave this list empty to use the defaults.
            </p>
            <div
              v-for="(starter, index) in starters"
              :key="index"
              class="starter-editor agent-card"
            >
              <label
                >Label<input
                  v-model="starter.label"
                  :aria-label="`Starter ${index + 1} label`"
                  maxlength="120"
                  :readonly="!editable"
                  required /></label
              ><label
                >Prompt<textarea
                  v-model="starter.prompt"
                  :aria-label="`Starter ${index + 1} prompt`"
                  :readonly="!editable"
                  required
                /></label
              ><button
                v-if="editable"
                type="button"
                class="text-button"
                :aria-label="`Remove starter ${index + 1}`"
                @click="starters.splice(index, 1)"
              >
                Remove
              </button>
            </div>
            <button
              v-if="editable"
              type="button"
              class="button"
              :disabled="starters.length >= 12"
              @click="starters.push({ label: '', prompt: '' })"
            >
              Add starter
            </button>
            <div v-if="!starters.length" class="default-starters">
              <p v-for="starter in defaultStarters" :key="starter.label">
                <b>{{ starter.label }}</b
                ><small>{{ starter.prompt }}</small>
              </p>
            </div>
            <p v-if="!validStarters" role="alert">
              Each starter needs a label (up to 120 characters) and a prompt (up to 16 KiB).
            </p></template
          >
          <footer v-if="editable">
            <button
              class="button primary"
              :disabled="busy || !general.name.trim() || (builtin && !validStarters)"
            >
              Save changes
            </button>
          </footer>
        </form>
        <form v-else class="agent-pane" @submit.prevent="patch({ visibility })">
          <h2>Access</h2>
          <h3>Who can assign studies to this agent</h3>
          <div class="agent-card agent-radios" role="radiogroup" aria-label="Access">
            <label
              ><input
                v-model="visibility"
                type="radio"
                value="private"
                :disabled="!editable || builtin"
              /><span
                ><b>{{ mine ? 'Only me' : 'Only the owner' }}</b
                ><small>{{
                  mine ? 'Only you can assign this agent.' : `Only ${owner} can assign this agent.`
                }}</small></span
              ></label
            ><label
              ><input
                v-model="visibility"
                type="radio"
                value="workspace"
                :disabled="!editable || builtin"
              /><span
                ><b>Entire workspace</b
                ><small>Every workspace member can assign this agent.</small></span
              ></label
            >
          </div>
          <footer v-if="editable">
            <button class="button primary" :disabled="busy || visibility === agent.visibility">
              Save changes
            </button>
          </footer>
        </form>
      </div>
    </template>
    <p v-if="detail.error.value" class="inline-error agent-error" role="alert">
      {{ detail.error.value.message }} <button @click="detail.refetch()">Retry</button>
    </p>
  </section>
  <BackendDetailsDialog
    v-if="backendDetails"
    :key="workspaceId + id"
    :harness="general.harness"
    :runtimes="runtimes.data.value"
    @close="backendDetails = false"
  />
</template>
<style src="./agent-detail.css" />

<style scoped>
.agent-avatar :deep(.aristotle-art) {
  width: 44px;
  height: 48px;
}
.agent-avatar :deep(.aristotle-art > span) {
  width: 119px;
  height: 166px;
  transform: translate(-29px, -6px);
}
.starter-editor {
  padding: 16px;
  margin-bottom: 10px;
  display: grid;
  gap: 10px;
}
.starter-editor label {
  display: grid;
  gap: 6px;
  font-size: 13px;
}
.starter-editor input,
.starter-editor textarea {
  border: 1px solid var(--border);
  background: var(--input);
  border-radius: 5px;
  padding: 8px;
  font: inherit;
}
.starter-editor textarea {
  min-height: 80px;
}
.default-starters {
  margin-top: 16px;
}
.default-starters p {
  margin-bottom: 12px;
  font-size: 13px;
}
.default-starters small {
  display: block;
  color: var(--muted);
  line-height: 20px;
}
</style>
