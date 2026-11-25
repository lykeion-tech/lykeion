<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router';
import Aristotle from '../coresearch/Aristotle.vue';
import { defaultStarters, type Starter } from '../coresearch/aristotle';
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../../api/client';
import {
  useAgent,
  useAgentWork,
  useAgents,
  useCards,
  useMe,
  useMembers,
  useRuntimes,
  useWorkspace,
  useWorkspaces,
} from '../../api/queries';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import type { AgentDefinition, Card, Runtime } from '../../api/types';
import Icon from '../../ui/Icon.vue';
import Modal from '../../ui/Modal.vue';
import EmojiIcon from '../../ui/EmojiIcon.vue';
import { useStudyComposer } from '../../state/study-composer';
import { backends, backendLabel } from '../../lib/backends';
import { matchesQuery } from '../../lib/search';
import {
  boardColumnOf,
  boardColumns,
  columnName,
  stages,
  studyDisplayTitle,
  studyNumber,
  timeAgo,
} from '../../lib/studies';
import { notify } from '../../state/notice';
import { agentNameWithoutCli } from '../../lib/agent-name';
import ModelSwitcher from '../coresearch/cowrite/ModelSwitcher.vue';
import AgentCustomize from './AgentCustomize.vue';
import { agentAvailable } from './availability';
import '../coresearch/l2.css';

const props = defineProps<{ id: string }>();
const route = useRoute();
const router = useRouter();
const { openComposer } = useStudyComposer();
const starters = ref<Starter[]>([]);
const reportTemplate = ref('');
const defaultReportTemplate = ref('');
const builtin = computed(() => agent.value?.built_in_key === 'aristotle');
const studyUnavailable = ref(false);
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
  agents = useAgents(),
  me = useMe(),
  members = useMembers(),
  cards = useCards(),
  workspace = useWorkspace(),
  workspaces = useWorkspaces();
const client = useQueryClient();
const runtimes = useRuntimes();

const tab = ref<'overview' | 'work' | 'capabilities' | 'settings'>('overview'),
  section = ref<'general' | 'access'>('general'),
  capabilitySection = ref<'instructions' | 'customize'>('instructions'),
  search = ref(''),
  collapsed = ref<string[]>([]),
  busy = ref(false),
  error = ref('');
const general = ref({
    name: '',
    description: '',
    harness: 'claude',
    model: '',
    stage: '' as string,
  }),
  visibility = ref<'workspace' | 'private'>('workspace'),
  instructions = ref('');
const modelChoices = computed(() => {
  const runtime = runtimes.data.value?.find(
    (r) =>
      r.owner_id === me.data.value?.id &&
      r.status === 'online' &&
      r.harness === general.value.harness,
  );
  const models = runtime?.models ?? [];
  const current = general.value.model;
  return [
    ...(current && !models.some((m) => m.value === current)
      ? [{ value: current, label: current }]
      : []),
    ...models,
  ];
});
const harnessChoices = computed(() => [
  ...backends.map((backend) => ({ value: backend.id, label: backend.label })),
  ...(backends.some((backend) => backend.id === general.value.harness)
    ? []
    : [{ value: general.value.harness, label: backendLabel(general.value.harness) }]),
]);

const denied = computed(
  () => detail.error.value instanceof ApiError && [403, 404].includes(detail.error.value.status),
);
const agent = computed(() => (denied.value ? undefined : detail.data.value));
const work = useAgentWork(() => (builtin.value ? '' : agent.value?.id ?? ''));
const workLoading = computed(() => !builtin.value && (work.isPending.value || cards.isPending.value));
const available = computed(
  () => !!agent.value && agentAvailable(agent.value, runtimes.data.value ?? [], me.data.value?.id),
);
const modelLabel = computed(() => {
  const current = agent.value;
  if (!current?.model) return 'Harness default';
  const labels = new Set(
    (runtimes.data.value ?? [])
      .filter(
        (runtime) =>
          runtime.owner_id === me.data.value?.id &&
          runtime.status === 'online' &&
          runtime.harness === current.harness,
      )
      .flatMap((runtime) => runtime.models ?? [])
      .filter((model) => model.value === current.model)
      .map((model) => model.label),
  );
  return labels.size === 1 ? [...labels][0]! : current.model;
});
const mine = computed(() => agent.value?.owner_id === me.data.value?.id);
const lab = computed(() => agent.value?.laboratory === 'built-in');
const customAgent = computed(
  () => !lab.value && !builtin.value && agents.data.value?.find((item) => item.id === props.id)?.source === 'custom',
);
const machineName = (r: Runtime) => r.custom_name || r.device_name;
const boundMachine = computed(() =>
  (runtimes.data.value ?? []).find((r) => r.id === agent.value?.runtime_id),
);
const boundMachineLabel = computed(() => {
  const machine = boundMachine.value;
  if (!machine) return 'Not bound to a machine';
  const member = members.data.value?.find((m) => m.user_id === machine.owner_id);
  return `${member ? member.name || member.email : 'Former member'}'s ${machineName(machine)}`;
});
const mayDelete = computed(
  () =>
    !!agent.value &&
    !lab.value &&
    !builtin.value &&
    agents.data.value?.find((item) => item.id === props.id)?.source === 'custom' &&
    (mine.value ||
      ['owner', 'admin'].includes(
        workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role ?? '',
      )),
);
const editable = computed(
  () =>
    !!agent.value &&
    !lab.value &&
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
const studyWork = computed(() => new Map(work.data.value?.map((row) => [row.card_id, row.active_role])));
const directlyAssigned = (card: Card) => card.assignee_type === 'agent' && card.assignee_id === props.id;
const activeRole = (card: Card) =>
  studyWork.value.get(card.id) ?? (directlyAssigned(card) ? card.role || (card.queued ? 'Queued' : '') : '');
const assigned = computed(() =>
  (cards.data.value ?? [])
    .filter((c) => directlyAssigned(c) || studyWork.value.has(c.id))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
);
const active = computed(() =>
  assigned.value.filter((c) => !c.ending && c.board_column !== 'done' && activeRole(c)),
);
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
      name: agentNameWithoutCli(a.name, a.harness),
      description: a.description,
      harness: a.harness,
      model: a.model,
      stage: a.stage,
    };
    visibility.value = a.visibility;
    instructions.value = a.instructions;
    defaultReportTemplate.value = a.default_report_template ?? '';
    reportTemplate.value = a.report_template || defaultReportTemplate.value;
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
  },
  { flush: 'sync' },
);

// "Choose a stage" on the Agents page lands on the stage field.
watch(
  [() => route.query.field, agent],
  async ([field, a]) => {
    if (field !== 'stage' || !a) return;
    tab.value = 'settings';
    section.value = 'general';
    await nextTick();
    document.getElementById('agent-stage')?.querySelector('button')?.focus();
  },
  { immediate: true },
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
    client.setQueryData(keys.agent(ws, id), { ...saved, default_report_template: defaultReportTemplate.value });
    void detail.refetch();
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    if (current()) notify('Agent saved.');
  } catch (e) {
    if (current()) error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
async function deleteAgent() {
  if (
    busy.value ||
    !agent.value ||
    !confirm(`Delete ${agent.value.name}? Past studies and run history will be kept.`)
  )
    return;
  const ws = workspaceId.value,
    id = props.id,
    generation = editGeneration;
  busy.value = true;
  error.value = '';
  try {
    await api('DELETE', `/api/agents/${id}`, undefined, undefined, { workspace: ws });
    await client.invalidateQueries({ queryKey: keys.agents(ws) });
    client.removeQueries({ queryKey: keys.agent(ws, id) });
    if (generation === editGeneration) await router.push('/agents');
  } catch (e) {
    if (generation === editGeneration) error.value = (e as Error).message;
  } finally {
    if (generation === editGeneration) busy.value = false;
  }
}
function toggle(id: string) {
  collapsed.value = collapsed.value.includes(id)
    ? collapsed.value.filter((x) => x !== id)
    : [...collapsed.value, id];
}
</script>
<template>
  <section class="agent-page agent-detail-page">
    <template v-if="agent">
      <header class="agent-page-header">
        <nav class="detail-breadcrumb" aria-label="Agent navigation">
          <RouterLink to="/agents"><Icon name="back" :size="14" />Agents</RouterLink>
          <span aria-hidden="true">/</span>
          <span aria-current="page">{{ agentNameWithoutCli(agent.name, agent.harness) }}</span>
        </nav>
        <div class="agent-actions">
          <button v-if="mayDelete" class="button" :disabled="busy" @click="deleteAgent">
            <Icon name="trash" :size="14" />Delete agent
          </button>
          <button
            v-if="!agent.archived_at && (agent.visibility === 'workspace' || mine)"
            class="button primary"
            @click="builtin ? (studyUnavailable = true) : openComposer({ type: 'agent', id })"
          >
            <Icon name="plus" :size="14" />New study
          </button>
        </div>
      </header>
      <div class="agent-layout">
        <div class="agent-sidebar">
          <aside class="agent-inspector">
            <div class="agent-identity">
              <EmojiIcon
                :key="workspaceId + ':' + id"
                :class="{ 'aristotle-avatar': builtin && !agent.avatar_url }"
                :value="agent.avatar_url || ''"
                :editable="editable"
                :busy="busy"
                fallback="bot"
                @select="(value) => patch({ avatar_url: value })"
              >
                <Aristotle v-if="builtin" still /><Icon v-else name="bot" :size="26" />
              </EmojiIcon>
              <div>
                <div class="agent-title">
                  <h1
                    :class="{
                      'long-name': [...agentNameWithoutCli(agent.name, agent.harness)].length > 20,
                    }"
                  >
                    {{ agentNameWithoutCli(agent.name, agent.harness) }}
                  </h1>
                  {{ ' '
                  }}<span
                    class="agent-status"
                    :class="{
                      archived: agent.archived_at,
                      unavailable: !agent.archived_at && !available,
                    }"
                    >{{ agent.archived_at ? 'Archived' : available ? 'Active' : 'Inactive' }}</span
                  >
                </div>
                <p v-if="agent.description" class="agent-description">{{ agent.description }}</p>
              </div>
            </div>
            <div class="agent-panel">
              <h3>Properties</h3>
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
                <dd>
                  {{ harnessName(lab && boundMachine ? boundMachine.harness : agent.harness) }}
                </dd>
                <dt>Model</dt>
                <dd>{{ modelLabel }}</dd>
                <dt>Stage</dt>
                <dd>{{ agent.stage ? columnName(agent.stage) : 'No stage' }}</dd>
                <template v-if="lab">
                  <dt>Machine</dt>
                  <dd>
                    {{ boundMachineLabel }}
                    <p
                      v-if="
                        boundMachine &&
                        boundMachine.os !== 'linux' &&
                        (members.data.value?.length ?? 0) > 1
                      "
                      class="agent-hint"
                    >
                      {{ machineName(boundMachine) }} runs only its owner's studies.
                    </p>
                  </dd>
                </template>
              </dl>
              <h3>Studies</h3>
              <p v-if="workLoading" class="muted" role="status">Loading study activity…</p>
              <p v-if="work.isError.value" class="muted" role="status">
                Team study activity is unavailable.
                <button type="button" class="text-button" @click="work.refetch()">Retry</button>
              </p>
              <div v-if="!workLoading" class="agent-stats">
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
            </div>
          </aside>
        </div>
        <div class="agent-content">
          <div class="agent-tabs-bar" role="tablist">
            <button
              v-for="t in ['overview', 'capabilities', 'work', 'settings'] as const"
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
                In progress<small v-if="active.length">{{ active.length }}</small>
              </h2>
              <p v-if="!active.length && !workLoading" class="muted">No study work in progress.</p>
              <RouterLink
                v-for="c in active"
                :key="c.id"
                :to="'/studies/' + c.id"
                class="agent-study"
                ><small>{{ number(c) }}</small
                ><span>{{ studyDisplayTitle(c.title || c.question) }}</span
                ><em>{{ activeRole(c) }}</em></RouterLink
              >
              <hr />
              <h2>Recent work</h2>
              <p v-if="!assigned.length && !workLoading" class="muted">No study work yet.</p>
              <RouterLink
                v-for="c in assigned.slice(0, 8)"
                :key="c.id"
                :to="'/studies/' + c.id"
                class="agent-study"
                ><small>{{ number(c) }}</small
                ><span>{{ studyDisplayTitle(c.title || c.question) }}</span
                ><em>{{ timeAgo(c.updated_at) }}</em></RouterLink
              >
            </div>
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
                }}<small v-if="!workLoading">{{ g.cards.length }}</small>
              </button>
              <template v-if="!collapsed.includes(g.id)">
                <RouterLink
                  v-for="c in g.cards"
                  :key="c.id"
                  :to="'/studies/' + c.id"
                  class="agent-study"
                  ><small>{{ number(c) }}</small
                  ><span>{{ studyDisplayTitle(c.title || c.question) }}</span
                  ><em>{{ timeAgo(c.updated_at) }}</em></RouterLink
                >
                <p v-if="!g.cards.length && !workLoading" class="agent-group-empty">No studies</p>
              </template>
            </section>
          </div>

          <div v-else class="agent-split">
            <nav class="agent-subnav">
              <template v-if="tab === 'capabilities'">
                <button
                  v-for="item in ['instructions', 'customize'] as const"
                  :key="item"
                  :aria-current="capabilitySection === item ? 'page' : undefined"
                  @click="capabilitySection = item"
                >
                  {{ item === 'instructions' ? 'Instructions' : 'Customize' }}
                </button>
              </template>
              <template v-else
                ><button
                  v-for="s in ['general', 'access'] as const"
                  :key="s"
                  :aria-current="section === s ? 'page' : undefined"
                  @click="section = s"
                >
                  {{ s === 'general' ? 'General' : 'Access' }}
                </button></template
              >
            </nav>
            <form
              v-if="tab === 'capabilities' && capabilitySection === 'instructions'"
              class="agent-pane"
              @submit.prevent="patch({ instructions })"
            >
              <h2>Instructions</h2>
              <p class="agent-lede">Set the instructions this agent works from.</p>
              <textarea
                id="agent-instructions"
                v-model="instructions"
                class="agent-prompt"
                aria-label="Agent instructions"
                :readonly="!editable"
              />
              <footer v-if="editable">
                <button
                  class="button primary"
                  :disabled="busy || instructions === agent.instructions"
                >
                  Save
                </button>
              </footer>
            </form>
            <AgentCustomize v-else-if="tab === 'capabilities'" :id="id" :editable="editable" />
            <form
              v-else-if="section === 'general'"
              class="agent-pane"
              @submit.prevent="
                validStarters &&
                patch({
                  ...general,
                  stage: general.stage || undefined,
                  name:
                    general.name === agentNameWithoutCli(agent.name, agent.harness)
                      ? agent.name
                      : general.name,
                  conversation_starters: starters,
                  ...(customAgent ? { report_template: reportTemplate === defaultReportTemplate ? '' : reportTemplate } : {}),
                })
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
                      class="agent-description-input"
                      maxlength="1000"
                      aria-label="Agent description"
                      :readonly="!editable"
                    /><small>{{ general.description.length }} / 1000</small></span
                  ></label
                >
              </div>
              <h3>Stage</h3>
              <div class="agent-card agent-execution">
                <div class="agent-model-field"
                  ><span>Stage</span><span v-if="builtin" class="agent-stage-none">No stage</span
                  ><div v-else id="agent-stage" class="coresearch agent-model-picker">
                    <ModelSwitcher
                      v-model="general.stage"
                      label="Stage"
                      :models="[
                        ...(!general.stage ? [{ value: '', label: 'No stage' }] : []),
                        ...stages.map((s) => ({ value: s, label: columnName(s) })),
                      ]"
                      open-up
                      :disabled="!editable"
                    />
                  </div></div
                >
              </div>
              <h3>Execution</h3>
              <p class="agent-hint">An empty model uses the harness default.</p>
              <div class="agent-card agent-execution">
                <div class="agent-model-field">
                  <span>Harness</span>
                  <div class="coresearch agent-model-picker">
                    <ModelSwitcher
                      v-model="general.harness"
                      :models="harnessChoices"
                      label="Harness"
                      open-up
                      :disabled="!editable"
                    />
                  </div>
                </div>
                <div class="agent-model-field">
                  <span>Model</span>
                  <div class="coresearch agent-model-picker">
                    <ModelSwitcher
                      v-model="general.model"
                      :models="modelChoices"
                      :disabled="!editable"
                    />
                  </div>
                </div>
              </div>
              <h3>Study report template</h3>
              <p v-if="!builtin" class="agent-hint">
                {{
                  customAgent
                    ? 'Customize how this agent reports its findings in new Study runs.'
                    : 'These instructions tell this agent how to report its findings in a Study.'
                }}
              </p>
              <p v-if="builtin" class="agent-hint">This agent does not run Studies.</p>
              <textarea
                v-else
                v-model="reportTemplate"
                class="agent-prompt report-template"
                aria-label="Study report template"
                :readonly="!editable || !customAgent"
              />
              <h3>Conversation starters</h3>
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
                class="button add-starter"
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
              </p>
              <footer v-if="editable">
                <button
                  class="button primary"
                  :disabled="busy || !general.name.trim() || !validStarters"
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
                      mine
                        ? 'Only you can assign this agent.'
                        : `Only ${owner} can assign this agent.`
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
        </div>
      </div>
    </template>
    <p v-if="detail.error.value" class="inline-error agent-error" role="alert">
      {{ detail.error.value.message }} <button @click="detail.refetch()">Retry</button>
    </p>
    <Modal v-if="studyUnavailable" class="study-unavailable" title="Study unavailable" aria-describedby="study-unavailable-description" @close="studyUnavailable = false">
      <div class="study-unavailable-heading">
        <span class="study-unavailable-icon" aria-hidden="true"><Icon name="bot" :size="20" /></span>
        <h2>Study unavailable</h2>
      </div>
      <p id="study-unavailable-description">Aristotle cannot be assigned to a study. Choose an agent with a research stage instead.</p>
      <div class="study-unavailable-actions">
        <button class="button primary" @click="studyUnavailable = false">Got it</button>
      </div>
    </Modal>
  </section>
</template>
<style src="./agent-detail.css" />

<style scoped>
.study-unavailable {
  width: min(420px, calc(100vw - 32px));
  padding: 28px;
}
.study-unavailable-heading { display: flex; align-items: center; gap: 12px; }
.study-unavailable-icon { display: grid; place-items: center; width: 40px; height: 40px; flex: none; border-radius: 12px; background: var(--hover); color: var(--muted); }
.study-unavailable h2 { margin: 0; font-size: 18px; }
.study-unavailable p { margin: 16px 0 24px; color: var(--muted); line-height: 1.5; }
.study-unavailable-actions { display: flex; justify-content: flex-end; }
:deep(.aristotle-avatar .agent-avatar) {
  background: var(--palette-accent);
}
:deep(.agent-avatar .aristotle-art) {
  width: 44px;
  height: 48px;
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
