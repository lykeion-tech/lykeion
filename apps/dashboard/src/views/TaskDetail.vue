<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import RunTurn from '../components/RunTurn.vue';
import ExecutionLog from '../components/ExecutionLog.vue';
import { usePaneResize } from '../lib/resize';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import {
  useAgents,
  useResearches,
  useCard,
  useCardEvents,
  useCards,
  useComments,
  useGates,
  useMe,
  useMembers,
  useRecords,
  useRuns,
  useRuntimes,
  useWorkspace,
} from '../api/queries';
import type { Card, Column } from '../api/types';
import {
  actionLabel,
  canResume,
  sendBackTargets,
  type ComposerAction,
  checkLine,
  columnName,
  earlierColumns,
  endLabel,
  gateOf,
  jevThresholds,
  recordLine,
  routeBackLimit,
  stages,
  stateLabel,
  studyNumber,
  transcript,
} from '../lib/studies';
import { notify } from '../state/notice';
import { usePins } from '../state/pins';
import { useTaskComposer } from '../state/task-composer';
import { readText, writeText } from '../storage';

const props = defineProps<{ id: string; embedded?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const card = useCard(() => props.id);
const cards = useCards();
const gates = useGates(() => props.id);
const comments = useComments(() => props.id);
const events = useCardEvents(() => props.id);
const runs = useRuns(() => props.id);
const members = useMembers();
const agents = useAgents();
const researches = useResearches();
const inspector = usePaneResize('lykeion.study-inspector-width', 364, 240, 480, 1, () =>
  Math.max(160, window.innerWidth - (Number(readText('lykeion.sidebar-width')) || 224) - 400),
);
const detailsOpen = ref(true);
const propertyBusy = ref(false);
const assigneeValue = computed(() =>
  c.value?.assignee_id ? `${c.value.assignee_type}:${c.value.assignee_id}` : '',
);
const assignableAgents = computed(() =>
  (agents.data.value ?? []).filter(
    (a) => !a.archived_at && (a.visibility === 'workspace' || a.owner_id === c.value?.owner_id),
  ),
);
async function saveProperty(body: Record<string, unknown>) {
  const scope = mutationScope();
  propertyBusy.value = true;
  error.value = '';
  try {
    const saved = await api<Card>('PATCH', `/api/cards/${scope.id}/properties`, body);
    if (!scope.current()) return;
    client.setQueryData(keys.card(scope.workspace, scope.id), saved);
    await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
    await client.invalidateQueries({ queryKey: keys.researches(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) propertyBusy.value = false;
  }
}
function assign(event: Event) {
  const [type, id] = (event.target as HTMLSelectElement).value.split(':');
  void saveProperty({ assignee_type: type || null, assignee_id: id || null });
}

const runtimes = useRuntimes();
const workspace = useWorkspace();
const me = useMe();
const { pins, togglePin } = usePins();
const { openComposer } = useTaskComposer();

const c = computed(() => card.data.value);
const creator = computed(
  () =>
    events.data.value?.find(
      (e) => e.type === 'card.created' && e.actor_type === 'user' && e.actor_id,
    )?.actor_id,
);
const updatedAt = computed(
  () => c.value?.updated_at || cards.data.value?.find((x) => x.id === props.id)?.updated_at,
);
const number = computed(() => (c.value ? studyNumber(workspace.data.value, c.value) : ''));
const stage = computed(() =>
  c.value && stages.includes(c.value.board_column) ? c.value.board_column : '',
);
const records = useRecords(() => props.id, stage);
const allGates = computed(() => (gates.data.value ?? []).map(gateOf));
const latestGate = computed(() =>
  [...allGates.value].reverse().find((g) => g.column === stage.value),
);
const earlier = computed(() => (c.value ? earlierColumns(c.value, allGates.value) : []));
const expanded = ref<Column | ''>('');
const earlierRecords = useRecords(() => props.id, expanded);
const openChecks = ref<string[]>([]);
const resourceReads = computed(() =>
  [
    { label: 'Gates', query: gates },
    { label: 'Comments', query: comments },
    { label: 'Events', query: events },
    { label: 'Runs', query: runs },
    ...(stage.value ? [{ label: 'Records', query: records }] : []),
    ...(expanded.value ? [{ label: 'Earlier records', query: earlierRecords }] : []),
  ].map(({ label, query }) => ({
    label,
    error: query.error.value,
    pending: query.isPending.value,
    fetching: query.isFetching.value,
    retry: () => query.refetch(),
  })),
);
const readErrors = computed(() => resourceReads.value.filter((read) => read.error));
const transcriptPending = computed(() => resourceReads.value.some((read) => read.pending));
const entries = computed(() =>
  transcript(comments.data.value ?? [], runs.data.value ?? [], events.data.value ?? []),
);
const showMeta = ref(readText('lykeion.study-meta') !== 'false');
watch(showMeta, (v) => writeText('lykeion.study-meta', String(v)));
const editingBudget = ref(false);
const budgetDraft = ref(0);
const comment = ref('');
const busy = ref(false);
const error = ref('');
const action = ref<ComposerAction>({ kind: 'comment' });
const picker = ref<'' | 'main' | 'send-back'>('');
const placeholders: Record<ComposerAction['kind'], string> = {
  comment: 'Leave a comment… the stage roles read it next round.',
  'send-back': 'Why it goes back; the redone roles read this.',
  stop: 'Why the study stops.',
  resume: 'Optional: a comment for the next round.',
};
let draftRevision = 0;
let acknowledgedResumeComment: string | null = null;
watch(
  [comment, action],
  () => {
    draftRevision++;
    acknowledgedResumeComment = null;
  },
  { flush: 'sync' },
);
function pick(a: ComposerAction) {
  action.value = a;
  picker.value = '';
}

let generation = 0;
watch(
  [workspaceId, () => props.id],
  () => {
    generation++;
    detailsOpen.value = true;
    propertyBusy.value = false;
    editingBudget.value = false;
    budgetDraft.value = 0;
    comment.value = '';
    busy.value = false;
    error.value = '';
    expanded.value = '';
    openChecks.value = [];
    action.value = { kind: 'comment' };
    picker.value = '';
    acknowledgedResumeComment = null;
  },
  { flush: 'sync' },
);
onUnmounted(() => {
  generation++;
});

// A response belongs to one visit, including when navigation returns to its id.
function mutationScope() {
  const workspace = workspaceId.value;
  const id = props.id;
  const ownGeneration = generation;
  return {
    workspace,
    id,
    current: () =>
      generation === ownGeneration && workspaceId.value === workspace && props.id === id,
  };
}

function name(userId: string) {
  const m = members.data.value?.find((x) => x.user_id === userId);
  return m ? m.name || m.email : 'Former member';
}
function machine(runtimeId: string | null) {
  const r = runtimes.data.value?.find((x) => x.id === runtimeId);
  return r ? r.custom_name || r.device_name : '';
}
const usd = (n: number) => `$${n.toFixed(2)}`;
const time = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
const toggleCheck = (id: string) =>
  (openChecks.value = openChecks.value.includes(id)
    ? openChecks.value.filter((x) => x !== id)
    : [...openChecks.value, id]);
const verdictLabel = { pass: 'Passed', fail: 'Routed back', cannot_proceed: 'Stopped' };

// Each continuation, including the second Resume write, belongs to its captured visit.
async function send() {
  const text = comment.value.trim();
  const a = action.value;
  if (busy.value || (!text && a.kind !== 'resume') || !c.value) return;
  if (a.kind === 'stop' && !confirm(`Stop ${number.value}? Ended studies are never reopened.`))
    return;
  const scope = mutationScope();
  const ownDraftRevision = draftRevision;
  const base = `/api/cards/${scope.id}`;
  busy.value = true;
  error.value = '';
  try {
    if (
      a.kind === 'comment' ||
      (a.kind === 'resume' && text && acknowledgedResumeComment !== text)
    ) {
      await api('POST', `${base}/comments`, { body: text });
      if (!scope.current()) return;
      // Retry only the remaining Resume after this comment was acknowledged.
      if (a.kind === 'resume' && ownDraftRevision === draftRevision)
        acknowledgedResumeComment = text;
    }
    if (!scope.current()) return;
    if (a.kind === 'send-back') await api('POST', `${base}/send-back`, { to: a.to, reason: text });
    if (a.kind === 'stop') await api('POST', `${base}/stop`, { reason: text });
    if (a.kind === 'resume') await api('POST', `${base}/resume`);
    if (!scope.current()) return;
    if (ownDraftRevision === draftRevision) {
      comment.value = '';
      action.value = { kind: 'comment' };
      acknowledgedResumeComment = null;
    }
    await client.invalidateQueries({ queryKey: ['ws', scope.workspace, 'card', scope.id] });
    if (scope.current()) await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) busy.value = false;
  }
}
async function saveBudget() {
  const scope = mutationScope();
  const usd = Math.max(0, Number(budgetDraft.value) || 0);
  try {
    await api('POST', `/api/cards/${scope.id}/budget`, { usd });
    if (!scope.current()) return;
    editingBudget.value = false;
    await client.invalidateQueries({ queryKey: keys.card(scope.workspace, scope.id) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  }
}
async function remove() {
  if (!c.value || !confirm(`Delete ${number.value} “${c.value.title}”? This cannot be undone.`))
    return;
  const scope = mutationScope();
  try {
    await api('DELETE', `/api/cards/${scope.id}`);
    if (!scope.current()) return;
    await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
    if (scope.current()) emit('close');
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  }
}
function pin() {
  if (!togglePin(props.id)) notify('The pin could not be saved on this device.');
}
</script>
<template>
  <section
    class="task-detail"
    :class="embedded ? 'embedded-task' : 'desktop-task'"
    :style="{ '--properties-width': inspector.width.value + 'px' }"
    aria-label="Study"
  >
    <aside v-if="showMeta && c" class="task-properties study-meta" aria-label="Study details">
      <header class="properties-title"><h2>Properties</h2></header>
      <div class="property-row">
        <small>Stage</small>
        <div class="v">
          <span class="stage-dot" :class="'status-' + c.board_column" />{{
            columnName(c.board_column)
          }}<span v-if="stateLabel(c)" class="chip" :class="'chip-' + c.state">{{
            stateLabel(c)
          }}</span
          ><span v-if="c.ending" class="chip ending-chip" :class="'chip-' + c.ending">{{
            endLabel(c)
          }}</span>
        </div>
      </div>
      <div class="property-row">
        <label for="study-assignee">Assignee</label
        ><span class="avatar property-avatar">{{
          c.assignee_type === 'agent'
            ? (agents.data.value?.find((a) => a.id === c?.assignee_id)?.name[0] ?? 'A')
            : c.assignee_id
              ? name(c.assignee_id)[0]
              : '?'
        }}</span
        ><select
          id="study-assignee"
          aria-label="Assignee"
          :value="assigneeValue"
          :disabled="propertyBusy || members.isPending.value || agents.isPending.value"
          @change="assign"
        >
          <option value="">Unassigned</option>
          <optgroup label="Members">
            <option
              v-for="m in members.data.value ?? []"
              :key="m.user_id"
              :value="'member:' + m.user_id"
            >
              {{ m.name || m.email }}
            </option>
          </optgroup>
          <optgroup label="Agents">
            <option v-for="a in assignableAgents" :key="a.id" :value="'agent:' + a.id">
              {{ a.name }}
            </option>
          </optgroup>
          <option
            v-if="
              c.assignee_type === 'agent' && !assignableAgents.some((a) => a.id === c?.assignee_id)
            "
            :value="assigneeValue"
            disabled
          >
            {{
              agents.data.value?.find((a) => a.id === c?.assignee_id)?.name ?? 'Unavailable agent'
            }}
            (archived or unavailable)
          </option>
        </select>
      </div>
      <div class="property-row">
        <label for="study-research">Research</label><Icon name="folder" :size="13" /><select
          id="study-research"
          aria-label="Research"
          :value="c.research_id ?? ''"
          :disabled="propertyBusy || researches.isPending.value"
          @change="
            saveProperty({ research_id: ($event.target as HTMLSelectElement).value || null })
          "
        >
          <option value="">No research</option>
          <option v-for="r in researches.data.value ?? []" :key="r.id" :value="r.id">
            {{ r.name }}
          </option></select
        ><RouterLink
          v-if="c.research_id"
          :to="'/projects/' + c.research_id"
          aria-label="Open Research"
          ><Icon name="right" :size="12"
        /></RouterLink>
      </div>
      <div
        v-for="read in [
          { query: members, label: 'members' },
          { query: agents, label: 'agents' },
          { query: researches, label: 'Researches' },
        ].filter((x) => x.query.error.value)"
        :key="read.label"
        class="inline-error"
      >
        {{ read.query.error.value?.message }}
        <button @click="read.query.refetch()">Retry {{ read.label }}</button>
      </div>
      <ExecutionLog :runs="runs.data.value ?? []" :title="number + ' · ' + c.title" />
      <button
        class="details-heading"
        :aria-expanded="detailsOpen"
        @click="detailsOpen = !detailsOpen"
      >
        Details <Icon :name="detailsOpen ? 'down' : 'right'" :size="11" />
      </button>
      <div v-if="detailsOpen" class="details-body">
        <small>Owner</small>
        <div class="v metadata-owner">
          <span class="avatar">{{ name(c.owner_id)[0] }}</span
          >{{ name(c.owner_id) }}
        </div>
        <small>Budget</small>
        <div v-if="!editingBudget" class="v">
          {{ usd(c.spent_usd) }} {{ c.budget_usd > 0 ? `of ${usd(c.budget_usd)}` : '· no limit'
          }}<button
            v-if="!c.ending"
            class="text-button push-right"
            @click="
              budgetDraft = c.budget_usd;
              editingBudget = true;
            "
          >
            Change
          </button>
        </div>
        <form v-else class="v" @submit.prevent="saveBudget">
          <input
            v-model.number="budgetDraft"
            type="number"
            min="0"
            step="0.5"
            aria-label="Budget in USD"
          /><button class="button compact">Save budget</button>
        </form>
        <div v-if="c.budget_usd > 0" class="budget-bar">
          <i :style="{ width: Math.min(100, (100 * c.spent_usd) / c.budget_usd) + '%' }" />
        </div>
        <small>Deadline</small>
        <div class="v">{{ c.deadline ? new Date(c.deadline).toLocaleDateString() : 'None' }}</div>
        <small>Round</small>
        <div class="v">
          {{ c.round }} · {{ c.route_backs }} route-back{{ c.route_backs === 1 ? '' : 's' }} of
          {{ routeBackLimit(workspace.data.value?.settings) }}
        </div>
        <template v-if="c.follows_from.length">
          <small>Follows from</small>
          <div class="v">
            <RouterLink
              v-for="fid in c.follows_from"
              :key="fid"
              class="link"
              :to="'/studies/' + fid"
              >{{ cards.data.value?.find((x) => x.id === fid)?.title ?? 'Study' }}</RouterLink
            >
          </div>
        </template>
        <small>Created</small>
        <div class="v">{{ new Date(c.created_at).toLocaleString() }}</div>
        <small>Updated</small>
        <div class="v">{{ updatedAt ? new Date(updatedAt).toLocaleString() : '—' }}</div>
        <template v-if="creator"
          ><small>Created by</small>
          <div class="v">{{ name(creator) }}</div></template
        >
      </div>
    </aside>
    <div
      v-if="showMeta && c"
      class="task-splitter"
      role="separator"
      tabindex="0"
      aria-orientation="vertical"
      aria-label="Resize study inspector"
      :aria-valuenow="inspector.width.value"
      :aria-valuemin="inspector.minimum.value"
      :aria-valuemax="inspector.maximum.value"
      @pointerdown="inspector.pointerdown($event)"
      @keydown="inspector.keydown"
    />
    <div class="task-main">
      <header class="task-heading">
        <button
          v-if="!embedded"
          class="icon-button back-task"
          aria-label="Back to studies"
          @click="emit('close')"
        >
          <Icon name="back" /></button
        ><span>{{ number }} {{ c?.title }}</span>
        <div class="push-right">
          <button
            class="icon-button"
            :aria-pressed="pins.includes(id)"
            aria-label="Pin"
            @click="pin"
          >
            <Icon name="tag" />
          </button>
          <template v-if="c?.board_column === 'backlog'"
            ><button class="icon-button" aria-label="Edit study" @click="openComposer(id)">
              <Icon name="edit" /></button
            ><button class="icon-button" aria-label="Delete study" @click="remove">
              <Icon name="close" /></button
          ></template>
          <button
            class="icon-button"
            :aria-pressed="showMeta"
            aria-label="Toggle details"
            @click="showMeta = !showMeta"
          >
            <Icon name="panel" />
          </button>
        </div>
      </header>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <div v-if="c" class="transcript-scroll">
        <div class="transcript-content study-content">
          <h1 class="study-title">{{ c.title }}</h1>
          <p class="study-question">{{ c.question }}</p>
          <div v-for="col in earlier" :key="col" class="stage-box collapsed-stage">
            <button
              :aria-expanded="expanded === col"
              @click="expanded = expanded === col ? '' : col"
            >
              <Icon :name="expanded === col ? 'down' : 'right'" :size="12" /><span
                class="stage-dot"
                :class="'status-' + col"
              />{{ columnName(col) }}<span v-if="c.stale.includes(col)" class="chip">stale</span>
            </button>
            <template v-if="expanded === col">
              <div
                v-for="r in earlierRecords.data.value ?? []"
                :key="r.id"
                class="record-line"
                :class="{ superseded: r.superseded_at }"
              >
                <span class="key">{{ recordLine(r).key }}</span
                ><span>{{ recordLine(r).text }}</span
                ><span v-if="recordLine(r).source" class="source">{{ recordLine(r).source }}</span
                ><span v-if="r.superseded_at" class="superseded-mark">superseded</span>
              </div>
              <div
                v-for="(g, i) in allGates.filter((x) => x.column === col)"
                :key="i"
                class="gate-line"
              >
                Gate · round {{ g.round }} · {{ verdictLabel[g.verdict]
                }}<span v-if="g.reason" class="why"> · {{ g.reason }}</span>
              </div>
            </template>
          </div>
          <div v-if="stage" class="stage-box">
            <h4>
              <span class="stage-dot" :class="'status-' + stage" />{{ columnName(stage) }} · round
              {{ c.round
              }}<span v-if="stateLabel(c)" class="chip" :class="'chip-' + c.state">{{
                stateLabel(c)
              }}</span
              ><span v-if="c.flag" class="chip" :class="'chip-' + c.flag">{{
                c.flag.replace(/_/g, ' ')
              }}</span>
            </h4>
            <div
              v-for="r in records.data.value ?? []"
              :key="r.id"
              class="record-line"
              :class="{ superseded: r.superseded_at }"
            >
              <span class="key">{{ recordLine(r).key }}</span
              ><span>{{ recordLine(r).text }}</span
              ><span v-if="recordLine(r).source" class="source">{{ recordLine(r).source }}</span
              ><span v-if="r.superseded_at" class="superseded-mark">superseded</span>
            </div>
            <p v-if="records.isSuccess.value && !records.data.value?.length" class="muted">
              No records yet.
            </p>
          </div>
          <div v-if="latestGate" class="stage-box">
            <h4>
              Gate · round {{ latestGate.round
              }}<span class="chip" :class="'gate-' + latestGate.verdict">{{
                verdictLabel[latestGate.verdict]
              }}</span>
            </h4>
            <p v-if="latestGate.route?.reason || latestGate.reason" class="gate-reason">
              {{ latestGate.route?.reason || latestGate.reason }}
            </p>
            <template v-for="check in latestGate.checks" :key="check.id">
              <div class="gate-line gate-check">
                <span :class="checkLine(check).tone">{{ checkLine(check).mark }}</span
                ><span class="name">{{ checkLine(check).name }}</span
                ><span class="why">{{ checkLine(check).why }}</span
                ><button
                  v-if="checkLine(check).jev"
                  class="text-button more"
                  @click="toggleCheck(check.id)"
                >
                  Details {{ openChecks.includes(check.id) ? '▾' : '▸' }}
                </button>
              </div>
              <div v-if="openChecks.includes(check.id)" class="gate-details">
                <p>Jev asked: {{ check.question }}</p>
                <p>
                  It chose: {{ check.choice ?? 'no answer' }}, with probability
                  {{ check.p?.toFixed(2) ?? '—' }}
                </p>
                <p>
                  Threshold: passes at p ≥ {{ jevThresholds.pass }}, fails below
                  {{ jevThresholds.fail }}; between, the reviewer decides.
                </p>
              </div>
            </template>
          </div>
          <div
            v-for="read in readErrors"
            :key="read.label"
            class="inline-error study-read-error"
            role="alert"
          >
            {{ read.label }}: {{ read.error?.message }}
            <button class="text-button" :disabled="read.fetching" @click="read.retry()">
              Retry {{ read.label }}
            </button>
          </div>
          <p v-if="transcriptPending" role="status">Loading study activity…</p>
          <header class="transcript-heading"><h2>Transcript</h2></header>
          <template
            v-for="entry in entries"
            :key="
              entry.kind +
              (entry.kind === 'event'
                ? entry.id
                : entry.kind === 'run'
                  ? entry.run.id
                  : entry.comment.id)
            "
          >
            <template v-if="entry.kind === 'comment'">
              <div class="who" :class="{ mine: entry.comment.author_id === me.data.value?.id }">
                <span class="avatar">{{ name(entry.comment.author_id)[0] }}</span
                >{{ name(entry.comment.author_id) }}<span class="tm">{{ time(entry.at) }}</span>
              </div>
              <p class="bubble" :class="{ mine: entry.comment.author_id === me.data.value?.id }">
                {{ entry.comment.body }}
              </p>
            </template>
            <RunTurn
              v-else-if="entry.kind === 'run'"
              :run="entry.run"
              :machine="machine(entry.run.runtime_id)"
            />
            <div v-else class="event-line">
              <span class="dot" />{{ entry.text }} · {{ time(entry.at) }}
            </div>
          </template>
        </div>
      </div>
      <div v-if="c && !c.ending" class="pinned-composer" @keydown.esc="picker = ''">
        <form class="task-comment" @submit.prevent="send">
          <textarea
            v-model="comment"
            :placeholder="placeholders[action.kind]"
            aria-label="Comment"
            @keydown.meta.enter.prevent="send"
            @keydown.ctrl.enter.prevent="send"
          />
          <footer>
            <div class="action-anchor">
              <button
                type="button"
                class="action-mode"
                aria-label="Action"
                aria-haspopup="menu"
                :aria-expanded="picker !== ''"
                @click="picker = picker ? '' : 'main'"
              >
                {{ actionLabel(action) }} <Icon name="down" :size="11" />
              </button>
              <div v-if="picker === 'main'" class="action-menu" role="menu" aria-label="Actions">
                <button type="button" role="menuitem" @click="pick({ kind: 'comment' })">
                  Comment<small>next round</small>
                </button>
                <button
                  v-if="sendBackTargets(c).length"
                  type="button"
                  role="menuitem"
                  aria-haspopup="menu"
                  @click="picker = 'send-back'"
                >
                  Send back to<small>▸</small>
                </button>
                <button
                  type="button"
                  role="menuitem"
                  class="danger"
                  @click="pick({ kind: 'stop' })"
                >
                  Stop<small>ends the study</small>
                </button>
                <button
                  v-if="canResume(c)"
                  type="button"
                  role="menuitem"
                  @click="pick({ kind: 'resume' })"
                >
                  Resume<small>clears the block</small>
                </button>
              </div>
              <div
                v-if="picker === 'send-back'"
                class="action-menu"
                role="menu"
                aria-label="Send back to"
              >
                <button
                  v-for="to in sendBackTargets(c)"
                  :key="to"
                  type="button"
                  role="menuitem"
                  @click="pick({ kind: 'send-back', to })"
                >
                  {{ columnName(to) }}
                </button>
              </div>
            </div>
            <button
              class="round-send"
              type="submit"
              :disabled="busy || (!comment.trim() && action.kind !== 'resume')"
              aria-label="Send"
            >
              <Icon name="up" :size="18" />
            </button>
          </footer>
        </form>
      </div>
      <p v-if="card.isPending.value" class="detail-loading" role="status">Loading study…</p>
      <p v-else-if="card.isError.value" class="inline-error" role="alert">
        {{ card.error.value?.message }}
        <button class="text-button" :disabled="card.isFetching.value" @click="card.refetch()">
          Retry study
        </button>
      </p>
    </div>
  </section>
</template>
<style scoped>
.property-menu-anchor {
  position: relative;
}
.property-add-menu {
  position: absolute;
  left: -8px;
  top: 100%;
  z-index: 30;
  width: 176px;
  padding: 0 4px;
  background: var(--modal);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 6px 18px #0001;
}
.property-add-menu button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  height: 34px;
  text-align: left;
  font-size: 12px;
  border-radius: 5px;
  padding: 0 8px;
}
.property-add-menu button:hover,
.property-add-menu button:focus-visible {
  background: var(--hover);
}
.property-rows input {
  background: transparent;
  border: 0;
  border-radius: 4px;
  padding: 4px;
  min-width: 0;
  width: calc(100% - 68px);
  font-size: 14px;
}
.subtask-row {
  padding-bottom: 26px;
  border-bottom: 1px solid var(--border);
}

.add-subtasks {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--muted);
  padding: 12px 0;
  margin: 8px 0 0;
}

.desktop-task {
  inset: 0;
  position: absolute;
  background: var(--surface);
  display: flex;
  z-index: 12;
  overflow: hidden;
}
.task-preview {
  left: auto;
  width: min(1180px, calc(100% - 48px));
  border-left: 1px solid var(--border);
  box-shadow: -16px 0 40px #00000010;
}
.task-properties {
  flex: 0 0 var(--properties-width);
  width: var(--properties-width);
  overflow: auto;
  padding: 0 8px 24px;
  font-size: 14px;
}
.task-properties > header {
  height: 60px;
  padding: 0 12px;
  display: flex;
  align-items: center;
  gap: 4px;
}
.task-properties > header h2 {
  font-size: 20px;
  line-height: 28px;
  font-weight: 600;
}
.property-rows label {
  display: flex;
  align-items: center;
  min-height: 34px;
  gap: 4px;
}
.property-rows label > span {
  color: var(--muted);
  width: 64px;
  flex: none;
}
.property-rows select {
  border: 0;
  background: transparent;
  padding: 4px;
  max-width: calc(100% - 68px);
  font-size: 14px;
  min-width: 100px;
  border-radius: 4px;
}
.property-rows select:hover {
  background: var(--input);
}
.state-note {
  font-size: 12px;
  padding: 0 0 4px 68px;
  color: var(--muted);
}
.add-property {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--muted);
  padding: 7px 0;
  font-size: 14px;
}
.properties-section {
  margin-top: 28px;
}
.properties-section summary {
  display: flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  list-style: none;
  font-size: 14px;
  line-height: 20px;
}
.properties-section summary::-webkit-details-marker {
  display: none;
}
.properties-section:not([open]) summary > .icon {
  transform: rotate(-90deg);
}
.properties-section > p {
  color: var(--muted);
  font-size: 14px;
  line-height: 22px;
  margin: 12px 8px 0;
}
.task-metadata > p {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-left: 0;
}
.task-metadata p > span {
  min-width: 72px;
  color: var(--muted);
}
.task-splitter {
  flex: none;
  width: 1px;
  background: var(--border);
  cursor: col-resize;
  position: relative;
  touch-action: none;
}
.task-splitter::before {
  content: '';
  position: absolute;
  inset: 0 -3px;
  z-index: 2;
}
.task-splitter:hover {
  background: var(--primary);
}
.task-main {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.task-heading {
  height: 56px;
  flex: none;
  padding: 0 20px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
}
.task-heading > span {
  white-space: nowrap;
  text-overflow: ellipsis;
  overflow: hidden;
}
.task-heading > .push-right {
  display: flex;
}
.task-heading .back-task {
  width: 20px;
  margin-left: -8px;
}
.transcript-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.transcript-content {
  max-width: 1002px;
  padding: 0 16px 24px;
  margin: 0 auto;
}
.task-description {
  padding: 8px 0 12px;
  min-height: 100px;
}
.task-title {
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
  padding: 0;
  text-align: left;
  display: block;
  margin-bottom: 20px;
}
.task-description-text {
  padding: 0;
  text-align: left;
  white-space: pre-wrap;
  line-height: 23px;
  display: block;
  color: var(--muted);
}
.task-title-input {
  width: 100%;
  font-size: 24px;
  font-weight: 600;
  border: 0;
  background: var(--input);
  padding: 6px;
  border-radius: 5px;
  margin-bottom: 12px;
}
.task-description-input {
  width: 100%;
  min-height: 100px;
  resize: vertical;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  padding: 8px;
  line-height: 22px;
  margin-bottom: 12px;
}
.transcript-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 32px 0 24px;
}
.transcript-heading h2 {
  font-size: 16px;
  font-weight: 600;
}
.transcript-heading > span {
  font-size: 12px;
  color: var(--muted);
}
.transcript-entry {
  display: flex;
  gap: 12px;
  margin: 14px 16px;
  align-items: flex-start;
  line-height: 23px;
  font-size: 14px;
}
.transcript-entry > .avatar {
  margin-top: 1px;
}
.transcript-entry > div {
  min-width: 0;
  flex: 1;
}
.transcript-entry header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}
.transcript-entry strong {
  font-weight: 500;
}
.transcript-entry time {
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
.transcript-entry p {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.creation-entry {
  font-size: 12px;
  color: var(--muted);
  align-items: center;
}
.creation-entry > time {
  margin-left: auto;
}
.task-comment {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 8px 12px;
  min-height: 72px;
  margin-top: 24px;
}
.task-comment textarea {
  height: 26px;
  display: block;
  border: 0;
  background: none;
  outline: none;
  width: 100%;
  min-height: 26px;
  max-height: 180px;
  resize: vertical;
  line-height: 22px;
  font-size: 14px;
  padding: 0;
}
.task-comment footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  min-height: 24px;
  gap: 12px;
}
.task-comment footer > small {
  margin-right: auto;
  color: var(--muted);
  font-size: 11px;
}
.task-comment .round-send {
  width: 26px;
  height: 26px;
}
.pinned-composer {
  width: 100%;
  max-width: 1002px;
  margin: 0 auto;
  padding: 0 16px 24px;
  flex: none;
}
.pinned-composer .task-comment {
  margin-top: 8px;
}
.task-claims {
  margin-top: 30px;
}
.task-claims h2 {
  font-size: 16px;
}
.task-claims article {
  border-top: 1px solid var(--border);
  padding: 16px 0;
  line-height: 23px;
}
.task-claims small {
  color: var(--muted);
  overflow-wrap: anywhere;
}
.detail-loading {
  padding: 24px;
  color: var(--muted);
  font-size: 14px;
}
@media (max-width: 1150px) {
  .task-properties {
    flex-basis: min(var(--properties-width), 280px);
    width: min(var(--properties-width), 280px);
  }
  .transcript-content,
  .pinned-composer {
    padding-left: 24px;
    padding-right: 24px;
  }
  .task-preview .task-properties {
    flex-basis: 240px;
    width: 240px;
  }
  .task-heading {
    padding: 0 16px;
  }
}
.has-inspector .transcript-content,
.has-inspector .pinned-composer {
  padding-left: 30px;
  padding-right: 30px;
}
.inspector-focused > .task-properties,
.inspector-focused > .task-splitter,
.inspector-focused > .task-main {
  display: none;
}
.task-inspector {
  width: var(--inspector-width);
  flex: none;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--surface);
  overflow: hidden;
}
.inspector-focused .task-inspector {
  width: 100%;
  flex: 1;
}
.inspector-splitter {
  width: 4px;
  background: var(--border);
  flex: none;
  cursor: col-resize;
  touch-action: none;
  position: relative;
}
.inspector-splitter::before {
  content: '';
  position: absolute;
  inset: 0 -2px;
  z-index: 2;
}
.inspector-splitter:hover {
  background: var(--primary);
}
.inspector-toolbar {
  height: 44px;
  display: flex;
  align-items: center;
  padding: 0 8px;
  flex: none;
  gap: 3px;
}
.inspector-tabs {
  display: flex;
  align-items: center;
  flex: 1;
  min-width: 0;
  overflow: auto;
  gap: 5px;
  height: 100%;
}
.inspector-tab {
  display: flex;
  align-items: center;
  position: relative;
  flex: none;
  height: 30px;
  color: var(--muted);
}
.inspector-tab.active {
  color: var(--text);
}
.inspector-tab > button[role='tab'] {
  padding: 6px 2px;
  display: flex;
  align-items: center;
  gap: 5px;
  line-height: 20px;
  font-size: 14px;
  border-bottom: 2px solid transparent;
  white-space: nowrap;
}
.inspector-tab.active > button[role='tab'] {
  border-bottom-color: var(--text);
}
.inspector-tab-close {
  opacity: 0;
  padding: 2px;
  width: 18px;
  height: 24px;
}
.inspector-tab:hover .inspector-tab-close,
.inspector-tab-close:focus-visible {
  opacity: 1;
}
.inspector-toolbar > .icon-button {
  width: 26px;
  height: 26px;
}
.inspector-panel {
  margin: 0 8px 8px;
  flex: 1;
  min-height: 0;
  background: var(--canvas);
  border: 1px solid var(--border);
  border-radius: 12px;
  position: relative;
  overflow: auto;
  padding: 16px 12px;
  display: flex;
  flex-direction: column;
}
.inspector-panel > header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 20px;
}
.inspector-panel h2 {
  font-size: 16px;
  font-weight: 600;
  line-height: 20px;
}
.inspector-panel > header > small {
  margin-left: auto;
  font-size: 12px;
  color: var(--muted);
}
.inspector-panel > p {
  font-size: 14px;
  line-height: 21px;
  color: var(--muted);
  margin-top: 16px;
}
.notebook-footer {
  margin: auto -12px -16px;
  background: var(--border);
  padding: 8px 12px 6px;
  color: var(--muted);
  font-size: 12px;
  line-height: 20px;
}
.notebook-environment {
  display: flex;
  gap: 12px;
  overflow: hidden;
  white-space: nowrap;
}
.notebook-environment label {
  display: flex;
  align-items: center;
  gap: 5px;
}
.notebook-environment select {
  background: color-mix(in srgb, var(--surface) 25%, transparent);
  border: 0;
  color: var(--muted);
  max-width: 110px;
  font-size: 12px;
  height: 24px;
}
.notebook-link {
  font-size: 12px;
  color: var(--muted);
  padding: 0;
  text-align: left;
  line-height: 20px;
}
.notebook-link:hover {
  text-decoration: underline;
  color: var(--primary);
}
.notebook-footer > p {
  font-size: 12px;
  line-height: 20px;
}
.notebook-footer > div:last-child {
  display: flex;
  gap: 12px;
  margin-top: 6px;
}
.compute-runtime {
  font-size: 12px !important;
  margin: 24px 4px 6px !important;
}
.compute-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 0 4px;
}
.compute-stats > div {
  padding: 8px 12px;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.compute-stats small {
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
.compute-stats span {
  font-size: 14px;
}
.compute-stats i {
  height: 4px;
  border-radius: 2px;
  background: var(--border);
}
.compute-description {
  border-top: 1px solid var(--border);
  padding-top: 16px;
  margin-top: 12px !important;
  color: var(--text) !important;
}
.kernel-section {
  margin-top: 30px;
  padding: 0 4px;
}
.kernel-section h3 {
  font-size: 14px;
  line-height: 20px;
}
.kernel-section small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  line-height: 20px;
}
.kernel-section .notebook-link {
  display: block;
  margin-top: 12px;
}
@media (max-width: 1250px) {
  .task-inspector {
    width: min(var(--inspector-width), 320px);
  }
  .has-inspector .task-properties {
    display: none;
  }
  .has-inspector > .task-splitter {
    display: none;
  }
  .has-inspector .transcript-content,
  .has-inspector .pinned-composer {
    padding-left: 20px;
    padding-right: 20px;
  }
}
.embedded-task {
  position: relative;
  flex: 1;
  display: flex;
  min-height: 0;
  background: var(--surface);
}
.study-content {
  max-width: 780px;
  padding: 22px 20px 40px;
}
.study-title {
  font-size: 25px;
  line-height: 32px;
  font-weight: 600;
  margin-bottom: 12px;
  overflow-wrap: anywhere;
}
.study-question {
  font-size: 14.5px;
  margin-bottom: 14px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.stage-box {
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 12px 14px;
  margin-bottom: 10px;
  font-size: 13px;
}
.stage-box h4 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  margin-bottom: 6px;
}
.collapsed-stage > button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  color: var(--muted);
}
.record-line {
  display: flex;
  gap: 10px;
  padding: 3px 0;
  overflow-wrap: anywhere;
}
.record-line > span:nth-child(2) {
  flex: 1;
  min-width: 0;
  white-space: pre-wrap;
  line-height: 1.7;
}
.record-line .key {
  flex: none;
  color: var(--muted);
  min-width: 20px;
}
.record-line.superseded {
  color: var(--muted);
}
.superseded-mark {
  color: #8a6a1c;
  font-size: 12px;
  white-space: nowrap;
}
.record-line .source {
  color: var(--primary);
  margin-left: auto;
  white-space: nowrap;
}
.gate-reason {
  white-space: pre-wrap;
  line-height: 1.7;
  background: var(--input);
  border-left: 3px solid var(--control-border);
  padding: 6px 10px;
  margin: 6px 0 8px;
  overflow-wrap: anywhere;
}
.gate-line {
  display: flex;
  gap: 9px;
  align-items: baseline;
  padding: 4px 0;
  border-top: 1px solid var(--border);
  overflow-wrap: anywhere;
}
.gate-check {
  display: grid;
  grid-template-columns: 14px minmax(120px, 170px) minmax(0, 1fr) auto;
  line-height: 1.65;
}
.gate-line .name {
  min-width: 0;
  font-weight: 500;
}
.gate-line .why {
  color: var(--muted);
  min-width: 0;
}
.gate-line .more {
  margin-left: auto;
  font-size: 12px;
  white-space: nowrap;
}
.gate-details {
  margin: 2px 0 6px 22px;
  padding: 8px 10px;
  background: var(--input);
  border-radius: 6px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.ok {
  color: #3c7a4a;
}
.bad {
  color: #a33a44;
}
.att {
  color: #8a6a1c;
}
.gate-pass {
  background: #e6f1e8;
  color: #3c7a4a;
}
.gate-fail {
  background: #fbf0d9;
  color: #8a6a1c;
}
.who {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13.5px;
  margin: 8px 0 6px 14px;
}
.who.mine {
  justify-content: flex-end;
}
.who .tm {
  color: var(--muted);
  font-size: 12.5px;
}
.bubble {
  width: fit-content;
  max-width: calc(100% - 14px);
  line-height: 1.7;
  background: var(--input);
  border-radius: 12px;
  padding: 10px 13px;
  font-size: 13.5px;
  margin: 0 0 14px 14px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.bubble.mine {
  margin-left: auto;
  margin-right: 0;
  background: var(--fill-active);
}
.event-line {
  display: flex;
  gap: 10px;
  align-items: center;
  font-size: 12.5px;
  color: var(--muted);
  margin: 4px 0 10px 14px;
}
.event-line .dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--control-border);
  flex: none;
}
.study-meta {
  padding: 16px;
  font-size: 13px;
}
.study-meta > small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  margin-top: 12px;
}
.study-meta .v {
  margin-top: 3px;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.study-meta .v input {
  width: 90px;
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 3px 6px;
}
.study-meta .link {
  color: var(--primary);
}
.budget-bar {
  height: 5px;
  background: var(--border);
  border-radius: 3px;
  margin-top: 5px;
}
.budget-bar i {
  display: block;
  height: 5px;
  background: var(--primary);
  border-radius: 3px;
}

.status-hypotheses {
  color: #ab54f4;
}
.status-experimentation {
  color: #c69c25;
}
.status-validation {
  color: #278241;
}
.status-blocked {
  color: #d5213d;
}
.status-reporting,
.status-done {
  color: #0672c5;
}
.action-anchor {
  position: relative;
  margin-right: auto;
}
.action-mode {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 2px 8px;
  font-size: 12.5px;
  background: var(--surface);
}
.action-menu {
  position: absolute;
  bottom: calc(100% + 6px);
  left: 0;
  width: 230px;
  background: var(--fill-menu);
  border-radius: 8px;
  box-shadow: 0 10px 30px #0003;
  padding: 4px;
  z-index: 20;
}
.action-menu button {
  display: flex;
  width: 100%;
  padding: 6px 9px;
  border-radius: 5px;
  font-size: 13px;
  text-align: left;
}
.action-menu button:hover,
.action-menu button:focus-visible {
  background: var(--hover);
}
.action-menu small {
  margin-left: auto;
  color: var(--muted);
  font-size: 11.5px;
}
.action-menu .danger {
  color: var(--error-text);
}
.study-meta .ending-chip {
  max-width: 100%;
  white-space: normal;
  overflow-wrap: anywhere;
}
</style>

<style scoped>
.task-properties {
  flex-basis: var(--properties-width) !important;
  width: var(--properties-width) !important;
}
.study-meta {
  padding: 0 12px 24px;
}
.study-meta .properties-title {
  height: 56px;
  padding: 0 4px;
}
.properties-title h2 {
  font-size: 18px;
  font-weight: 600;
}
.property-row {
  display: flex;
  align-items: center;
  gap: 5px;
  min-height: 34px;
  font-size: 13px;
}
.property-row > small,
.property-row > label {
  width: 64px;
  flex: none;
  color: var(--muted);
  font-size: 12px;
}
.property-row .v {
  margin: 0;
  min-width: 0;
}
.property-row select {
  border: 0;
  background: transparent;
  min-width: 0;
  flex: 1;
  font-size: 12px;
  padding: 5px 0;
  border-radius: 4px;
}
.property-avatar {
  width: 18px;
  height: 18px;
  font-size: 10px;
}
.property-row select:hover {
  background: var(--hover);
}
.execution-log {
  margin-top: 32px;
}
.details-heading {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 13px;
  margin: 26px 0 16px;
}
.details-body {
  display: grid;
  grid-template-columns: 75px minmax(0, 1fr);
  gap: 14px 6px;
  align-items: center;
}
.details-body > small {
  color: var(--muted);
  font-size: 12px;
}
.details-body .v {
  font-size: 12px;
  margin: 0;
}
.details-body .budget-bar {
  grid-column: 2;
}
.details-body .v input {
  width: 70px;
}
.task-splitter:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: -1px;
}
@media (max-width: 850px) {
  .task-properties,
  .task-splitter {
    display: none;
  }
}
</style>
