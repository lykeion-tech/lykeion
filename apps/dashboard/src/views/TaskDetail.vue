<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import MachinePicker from '../components/MachinePicker.vue';
import AgentRunCard from '../components/AgentRunCard.vue';
import AgentRunDialog from '../components/AgentRunDialog.vue';
import ExecutionLog from '../components/ExecutionLog.vue';
import ColumnRating from '../components/ColumnRating.vue';
import { usePaneResize } from '../lib/resize';
import { api } from '../api/client';
import { formatSize } from '../lib/attachments';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import {
  useAgents,
  useResearches,
  useCard,
  useCardEvents,
  useCardRatings,
  useCards,
  useComments,
  useMe,
  useMembers,
  useRecords,
  useRuns,
  useRuntimes,
  useWorkspace,
} from '../api/queries';
import type { Card, Preregistration } from '../api/types';
import {
  actionLabel,
  blockedActions,
  type ComposerAction,
  columnName,
  endLabel,
  flowKinds,
  flowView,
  levelText,
  redoLabel,
  recordLine,
  verdictLabels,
  stages,
  stateLabel,
  studyNumber,
  studyOverview,
  transcript,
} from '../lib/studies';
import { notify } from '../state/notice';
import { usePins } from '../state/pins';
import { readText, writeText } from '../storage';

const props = defineProps<{ id: string; embedded?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const card = useCard(() => props.id);
const cards = useCards();
const comments = useComments(() => props.id);
const events = useCardEvents(() => props.id);
const ratings = useCardRatings(() => props.id);
const openRatings = computed(() => (ratings.data.value ?? []).filter((r) => !r.dismissed));
const runs = useRuns(() => props.id);
const members = useMembers();
const agents = useAgents();
const researches = useResearches();
const studyElement = ref<HTMLElement>();
const studyWidth = ref(0);
const inspectorMinimum = 240;
const studyMinimum = 400;
const embeddedConstrained = computed(
  () => props.embedded && studyWidth.value < inspectorMinimum + studyMinimum,
);
const inspector = usePaneResize(
  props.embedded ? 'lykeion.embedded-study-inspector-width' : 'lykeion.study-inspector-width',
  props.embedded ? 260 : 364,
  inspectorMinimum,
  480,
  -1,
  () =>
    props.embedded
      ? Math.max(0, studyWidth.value - studyMinimum)
      : Math.max(
          160,
          window.innerWidth - (Number(readText('lykeion.sidebar-width')) || 224) - studyMinimum,
        ),
);
let studyObserver: ResizeObserver | undefined;
onMounted(() => {
  if (!props.embedded || !studyElement.value) return;
  studyWidth.value = studyElement.value.getBoundingClientRect().width;
  studyObserver = new ResizeObserver(([entry]) => {
    if (entry) studyWidth.value = entry.contentRect.width;
  });
  studyObserver.observe(studyElement.value);
});
onUnmounted(() => studyObserver?.disconnect());
const detailsOpen = ref(true);
const propertyBusy = ref(false);
async function saveProperty(body: Record<string, unknown>) {
  const scope = mutationScope();
  propertyBusy.value = true;
  error.value = '';
  try {
    const saved = await api<Card>('PATCH', `/api/cards/${scope.id}/properties`, body);
    if (!scope.current()) return;
    // The PATCH answer lacks the Teams' names, so keep them.
    client.setQueryData<Card>(keys.card(scope.workspace, scope.id), (old) => ({
      ...old,
      ...saved,
    }));
    await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
    await client.invalidateQueries({ queryKey: keys.researches(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) propertyBusy.value = false;
  }
}

const runtimes = useRuntimes();
const workspace = useWorkspace();
const me = useMe();
const { pins, togglePin } = usePins();

const c = computed(() => card.data.value);
// Each pre-registered result with its binding decision, if it has one yet.
const preregistered = computed(() =>
  (c.value?.predicates ?? []).map((p) => ({
    ...p,
    decision: c.value?.preregistration?.find((d) => d.name === p.name),
  })),
);
const outcomeText = (d?: Preregistration) =>
  !d
    ? 'Not decided yet'
    : d.outcome === 'unconfirmed'
      ? `not confirmed yet: ${d.reason}`
      : d.outcome;
const valuesIn = (values: Record<string, number> | null, run: string) =>
  `${Object.entries(values ?? {})
    .map(([k, v]) => `${k} = ${v}`)
    .join(', ')} in run ${run}`;
const services = computed(() => c.value?.services ?? []);
async function download(a: { id: string; name: string }) {
  try {
    const blob = await api<Blob>(
      'GET',
      `/api/cards/${props.id}/attachments/${a.id}`,
      undefined,
      undefined,
      { response: 'blob' },
    );
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = a.name;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (e) {
    notify(`${a.name}: ${(e as Error).message}`);
  }
}
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
const flow = computed(() => flowView(records.data.value ?? []));
const firstRound = computed(() =>
  Math.min(c.value?.round ?? 1, ...(records.data.value ?? []).map((r) => r.round)),
);
const hasFlow = computed(
  () =>
    !!(
      flow.value.scores.length ||
      flow.value.steps.length ||
      flow.value.audit ||
      flow.value.calls.length
    ),
);
const lineRecords = computed(() =>
  (records.data.value ?? []).filter((r) => !flowKinds.includes(r.kind)),
);
const team = computed(() => (c.value && stage.value ? c.value.teams?.[stage.value] : undefined));
const offLabel = computed(() =>
  Boolean(team.value && stage.value && c.value?.off_label?.includes(stage.value)),
);
const resourceReads = computed(() =>
  [
    { label: 'Comments', query: comments },
    { label: 'Events', query: events },
    { label: 'Runs', query: runs },
    ...(stage.value ? [{ label: 'Records', query: records }] : []),
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
// Runs are read as agent cards above; their transcripts open in a dialog.
const entries = computed(() =>
  transcript(
    comments.data.value ?? [],
    [],
    (events.data.value ?? []).filter((e) => e.type !== 'card.created'),
  ),
);
const overview = computed(() => studyOverview(records.data.value ?? [], c.value?.round ?? 1));
const agentRuns = computed(() =>
  [...(runs.data.value ?? [])].sort(
    (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || a.id.localeCompare(b.id),
  ),
);
const openRun = ref('');
const showMeta = ref(readText('lykeion.study-meta') !== 'false');
watch(showMeta, (v) => writeText('lykeion.study-meta', String(v)));
const comment = ref('');
const busy = ref(false);
const error = ref('');
const action = ref<ComposerAction>({ kind: 'comment' });
const picker = ref<'' | 'main'>('');
const placeholders: Record<ComposerAction['kind'], string> = {
  comment: 'Leave a comment… the stage roles read it next round.',
  stop: 'Why the study stops.',
};
const blocked = computed(() => (c.value ? blockedActions(c.value, me.data.value?.id) : []));
const assigneeName = computed(() => {
  const x = c.value;
  if (x?.assignee_type === 'team') {
    const names = [...new Set(Object.values(x.teams ?? {}).map((t) => t.name))];
    return names.length ? `Team: ${names.join(', ')}` : 'Team';
  }
  if (!x?.assignee_id) return 'Unassigned';
  if (x.assignee_type === 'member') return name(x.assignee_id);
  return agents.data.value?.find((a) => a.id === x.assignee_id)?.name ?? 'Unavailable agent';
});
// An agent working with no Team has nobody auditing it.
const worksAlone = computed(
  () => c.value?.assignee_type === 'agent' && !Object.keys(c.value.teams ?? {}).length,
);
let draftRevision = 0;
watch(
  [comment, action],
  () => {
    draftRevision++;
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
    comment.value = '';
    busy.value = false;
    error.value = '';
    action.value = { kind: 'comment' };
    picker.value = '';
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

const machineBusy = ref(false);
watch(
  [workspaceId, () => props.id],
  () => {
    machineBusy.value = false;
  },
  { flush: 'sync' },
);
async function pickMachine(value: string) {
  if (machineBusy.value) return;
  const scope = mutationScope();
  machineBusy.value = true;
  error.value = '';
  try {
    await api(
      'PATCH',
      `/api/cards/${encodeURIComponent(scope.id)}/machine`,
      { machine: value || null },
      undefined,
      { workspace: scope.workspace },
    );
    if (!scope.current()) return;
    await client.invalidateQueries({ queryKey: keys.card(scope.workspace, scope.id) });
    if (!scope.current()) return;
    await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) machineBusy.value = false;
  }
}

function name(userId: string) {
  const m = members.data.value?.find((x) => x.user_id === userId);
  return m ? m.name || m.email : 'Former member';
}
function runtime(runtimeId: string | null) {
  return runtimes.data.value?.find((x) => x.id === runtimeId);
}
const time = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
// Each continuation belongs to its captured visit.
async function send() {
  const text = comment.value.trim();
  const a = action.value;
  if (busy.value || !text || !c.value) return;
  if (a.kind === 'stop' && !confirm(`Stop ${number.value}? Ended studies are never reopened.`))
    return;
  const scope = mutationScope();
  const ownDraftRevision = draftRevision;
  const base = `/api/cards/${scope.id}`;
  busy.value = true;
  error.value = '';
  try {
    if (a.kind === 'comment') await api('POST', `${base}/comments`, { body: text });
    if (a.kind === 'stop') await api('POST', `${base}/stop`, { reason: text });
    if (!scope.current()) return;
    if (ownDraftRevision === draftRevision) {
      comment.value = '';
      action.value = { kind: 'comment' };
    }
    await client.invalidateQueries({ queryKey: ['ws', scope.workspace, 'card', scope.id] });
    if (scope.current()) await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) busy.value = false;
  }
}
function pin() {
  if (!togglePin(props.id)) notify('The pin could not be saved on this device.');
}
</script>
<template>
  <section
    ref="studyElement"
    class="task-detail"
    :class="[
      embedded ? 'embedded-task' : 'desktop-task',
      { 'embedded-constrained': embeddedConstrained },
    ]"
    :style="{ '--properties-width': inspector.width.value + 'px' }"
    aria-label="Study"
  >
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
      <div v-if="c" class="study-machine type-caption">
        <span
          >Runs on:
          {{ c.machine_label || (c.machine ? 'Unavailable machine' : 'My machines') }}</span
        >
        <p v-if="c.machine_notice" role="status">{{ c.machine_notice }}</p>
        <MachinePicker
          v-if="c.owner_id === me.data.value?.id"
          :model-value="c.machine || ''"
          :disabled="machineBusy"
          @update:model-value="pickMachine"
        />
      </div>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <div v-if="c" class="transcript-scroll">
        <div class="transcript-content study-content">
          <h1 class="study-title">{{ c.title }}</h1>
          <section v-if="stage" class="stage-box study-summary" aria-label="Overview">
            <h4>
              <span class="stage-dot" :class="'status-' + stage" />{{ columnName(stage) }} · round
              {{ c.round
              }}<span v-if="stateLabel(c)" class="chip" :class="'chip-' + c.state">{{
                stateLabel(c)
              }}</span
              ><span v-if="c.flag" class="chip" :class="'chip-' + c.flag">{{
                c.flag.replace(/_/g, ' ')
              }}</span
              ><span v-if="team" class="chip chip-team">{{ team.name }}</span
              ><span v-if="offLabel" class="chip chip-off-label"
                >Built for {{ columnName(team!.stage) }}</span
              >
            </h4>
            <p class="summary-text">{{ overview }}</p>
            <p v-if="flow.audit" class="summary-audit">
              <Icon name="check" :size="14" />
              Audit recorded<span v-if="flow.audit.findings.length">
                · {{ flow.audit.findings.length }}
                {{ flow.audit.findings.length === 1 ? 'finding' : 'findings' }} to review</span
              >
            </p>
            <details class="study-evidence">
              <summary>View evidence and activity</summary>

              <section v-if="flow.scores.length" class="flow-block" aria-label="Scores">
                <h5>
                  Scores<span class="chip" :class="'chip-verdict-' + flow.verdict">{{
                    verdictLabels[flow.verdict] ?? flow.verdict
                  }}</span>
                </h5>
                <div v-for="s in flow.scores" :key="s.criterion" class="score-row">
                  <span>{{ s.criterion }}</span
                  ><span class="level">{{ levelText(s.level) }} of 5</span
                  ><span
                    v-if="s.level <= 2 && redoLabel(c.redos, c.round, firstRound)"
                    class="chip"
                    >{{ redoLabel(c.redos, c.round, firstRound) }}</span
                  >
                </div>
              </section>
              <section v-if="flow.steps.length" class="flow-block" aria-label="Steps">
                <h5>Steps</h5>
                <div
                  v-for="st in flow.steps"
                  :key="st.key"
                  class="step-row"
                  :class="{ 'self-step': st.self }"
                >
                  {{ st.text }}<small v-if="st.detail">{{ st.detail }}</small>
                </div>
              </section>
              <section v-if="flow.audit" class="flow-block" aria-label="Audit">
                <h5>Audit</h5>
                <p>{{ flow.audit.text }}</p>
                <ul v-if="flow.audit.findings.length">
                  <li v-for="f in flow.audit.findings" :key="f">{{ f }}</li>
                </ul>
              </section>
              <section v-if="flow.calls.length" class="flow-block" aria-label="Calls">
                <h5>Calls</h5>
                <details v-for="k in flow.calls" :key="k.key" class="call-row">
                  <summary>{{ k.server }} · {{ k.tool }} · {{ time(k.at) }}</summary>
                  <pre>{{ k.answer }}</pre>
                </details>
              </section>
              <div
                v-for="r in lineRecords"
                :key="r.id"
                class="record-line"
                :class="{ superseded: r.superseded_at }"
              >
                <span class="key">{{ recordLine(r).key }}</span
                ><span>{{
                  typeof r.body.text === 'string' ? r.body.text : recordLine(r).text
                }}</span
                ><span v-if="recordLine(r).source" class="source">{{ recordLine(r).source }}</span
                ><span v-if="r.superseded_at" class="superseded-mark">superseded</span>
              </div>
              <p v-if="records.isSuccess.value && !lineRecords.length && !hasFlow" class="muted">
                No records yet.
              </p>
            </details>
          </section>
          <ColumnRating v-for="r in openRatings" :key="r.column" :card-id="id" :offer="r" />
          <section v-if="agentRuns.length" class="agent-cards" aria-label="Agents">
            <AgentRunCard
              v-for="(run, index) in agentRuns"
              :key="run.id"
              :run="run"
              :study="c.title"
              :position="index + 1"
              @open="openRun = run.id"
            />
          </section>
          <AgentRunDialog
            v-if="openRun && agentRuns.some((r) => r.id === openRun)"
            v-model="openRun"
            :runs="agentRuns"
            :runtime="runtime"
            :study-id="id"
            @close="openRun = ''"
          />
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
            <div v-else-if="entry.kind === 'event'" class="event-line">
              <span class="dot" />{{ entry.text }} · {{ time(entry.at) }}
            </div>
          </template>
        </div>
      </div>
      <div v-if="c && !c.ending" class="pinned-composer" @keydown.esc="picker = ''">
        <p v-if="blocked.length" class="block-bar">
          Scored low twice. Tell the Team what to fix to redo this column.
        </p>
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
                  v-if="!blocked.length"
                  type="button"
                  role="menuitem"
                  class="danger"
                  @click="pick({ kind: 'stop' })"
                >
                  Stop<small>ends the study</small>
                </button>
              </div>
            </div>
            <button
              class="round-send"
              type="submit"
              :disabled="busy || !comment.trim()"
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
    <aside v-if="showMeta && c" class="task-properties study-meta" aria-label="Study details">
      <header class="properties-title"><h2>Properties</h2></header>
      <div class="property-row">
        <small>Stage</small>
        <div class="v">
          <span class="property-icon"
            ><span class="stage-dot" :class="'status-' + c.board_column" /></span
          >{{ columnName(c.board_column)
          }}<span v-if="stateLabel(c)" class="chip" :class="'chip-' + c.state">{{
            stateLabel(c)
          }}</span
          ><span v-if="c.ending" class="chip ending-chip" :class="'chip-' + c.ending">{{
            endLabel(c)
          }}</span>
        </div>
      </div>
      <div class="property-row">
        <small>Assignee</small
        ><span class="avatar property-avatar property-icon">{{
          c.assignee_type === 'agent'
            ? (agents.data.value?.find((a) => a.id === c?.assignee_id)?.name[0] ?? 'A')
            : c.assignee_id
              ? name(c.assignee_id)[0]
              : '?'
        }}</span
        ><span class="v">{{ assigneeName }}</span
        ><span v-if="worksAlone" class="chip">No auditor</span>
      </div>
      <div class="property-row">
        <label for="study-research">Project</label
        ><span class="property-icon"><Icon name="folder" :size="13" /></span
        ><select
          id="study-research"
          aria-label="Project"
          :value="c.research_id ?? ''"
          :disabled="propertyBusy || researches.isPending.value"
          @change="
            saveProperty({ research_id: ($event.target as HTMLSelectElement).value || null })
          "
        >
          <option value="">No project</option>
          <option v-for="r in researches.data.value ?? []" :key="r.id" :value="r.id">
            {{ r.name }}
          </option></select
        ><RouterLink
          v-if="c.research_id"
          :to="'/projects/' + c.research_id"
          aria-label="Open Project"
          ><Icon name="right" :size="12"
        /></RouterLink>
      </div>
      <div
        v-for="read in [
          { query: members, label: 'members' },
          { query: agents, label: 'agents' },
          { query: researches, label: 'Projects' },
        ].filter((x) => x.query.error.value)"
        :key="read.label"
        class="inline-error"
      >
        {{ read.query.error.value?.message }}
        <button @click="read.query.refetch()">Retry {{ read.label }}</button>
      </div>
      <ExecutionLog :runs="runs.data.value ?? []" :title="number + ' · ' + c.title" />
      <h3 class="details-heading">
        <button :aria-expanded="detailsOpen" @click="detailsOpen = !detailsOpen">
          Details <Icon :name="detailsOpen ? 'down' : 'right'" :size="11" />
        </button>
      </h3>
      <div v-if="detailsOpen" class="details-body">
        <small>Owner</small>
        <div class="v metadata-owner">
          <span class="avatar">{{ name(c.owner_id)[0] }}</span
          >{{ name(c.owner_id) }}
        </div>
        <small>Deadline</small>
        <div class="v">{{ c.deadline ? new Date(c.deadline).toLocaleDateString() : 'None' }}</div>
        <template v-if="c.attachments.length">
          <small>Attached files</small>
          <ul class="v attached-files" aria-label="Attached files">
            <li v-for="a in c.attachments" :key="a.id">
              <button type="button" class="text-button" @click="download(a)">{{ a.name }}</button>
              <small>{{ formatSize(a.size_bytes) }}</small>
            </li>
          </ul>
        </template>
        <template v-if="preregistered.length">
          <small>Pre-registered results</small>
          <dl class="v study-variables" aria-label="Pre-registered results">
            <div v-for="p in preregistered" :key="p.name">
              <dt>{{ p.name }}</dt>
              <dd>{{ p.text }}</dd>
              <dd>{{ outcomeText(p.decision) }}</dd>
              <dd v-if="p.decision?.run">{{ valuesIn(p.decision.values, p.decision.run) }}</dd>
              <dd v-if="p.decision?.others?.length">
                Other runs wrote:
                {{ p.decision.others.map((o) => valuesIn(o.values, o.run)).join('; ') }}
              </dd>
            </div>
          </dl>
        </template>
        <p v-if="services.length" class="v services-note">
          This study has hidden services: {{ services.map((s) => s.name).join(', ') }}. Hidden
          services can't be fully isolated on every machine; don't use them to keep secrets.
        </p>
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
  scrollbar-color: var(--scrollbar-thumb) transparent;
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
  scrollbar-color: var(--scrollbar-thumb) transparent;
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
.block-bar {
  margin: 0;
  padding: 8px 12px;
  background: var(--error-bg);
  border: 1px solid var(--error-border);
  border-radius: 8px;
  font-size: 12px;
  line-height: 18px;
  color: var(--error-text);
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
  color: var(--primary-text);
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
.embedded-constrained > .task-properties,
.embedded-constrained > .task-splitter {
  display: none;
}
.embedded-task {
  min-width: 0;
  position: relative;
  flex: 1;
  display: flex;
  min-height: 0;
  background: var(--surface);
}
.study-content,
.pinned-composer {
  max-width: 860px;
  padding-inline: 20px;
}
.study-content {
  padding: 22px 20px 40px;
}
.study-title {
  font-size: 25px;
  line-height: 32px;
  font-weight: 600;
  margin-bottom: 12px;
  overflow-wrap: anywhere;
}
.agent-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr));
  gap: 12px;
  margin: 24px 0;
}
.stage-box {
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 12px 14px;
  margin-bottom: 10px;
  font-size: 13px;
}
.summary-text {
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  margin: 10px 0;
}
.summary-audit {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--muted);
  font-size: 12px;
  margin: 8px 0;
  flex-wrap: wrap;
}
.study-evidence > summary {
  color: var(--muted);
  cursor: pointer;
  font-size: 12px;
  padding-top: 6px;
}
.flow-block {
  margin: 8px 0;
}
.flow-block h5 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 4px;
}
.score-row {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 3px 0;
}
.score-row > span:first-child {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}
.step-row {
  padding: 3px 0;
  overflow-wrap: anywhere;
}
.step-row small {
  display: block;
  color: var(--muted);
}
.self-step {
  font-style: italic;
}
.call-row pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.stage-box h4 {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  margin-bottom: 6px;
  flex-wrap: wrap;
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
  color: var(--primary-text);
  margin-left: auto;
  white-space: nowrap;
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
  color: var(--primary-text);
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
.property-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  flex: 0 0 18px;
}
.property-icon .stage-dot {
  margin: 0;
}
.property-avatar {
  width: 18px;
  height: 18px;
  font-size: 10px;
}
.property-row select:hover {
  background: var(--hover);
}
/* ExecutionLog has two root nodes, so its section takes no scope attribute. */
.task-properties :deep(.execution-log) {
  margin-top: 26px;
}
.details-heading {
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
  margin: 26px 0 16px;
}
.details-heading button {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 0;
  font: inherit;
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
.details-body .attached-files {
  list-style: none;
  padding: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}
.study-variables > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-bottom: 6px;
  overflow-wrap: anywhere;
}
.study-variables dt {
  font-weight: 500;
}
.study-variables dd {
  margin: 0;
  white-space: pre-wrap;
}
.details-body .services-note {
  grid-column: 1 / -1;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.attached-files small {
  margin-left: 6px;
  color: var(--muted);
}
.attached-files .text-button {
  font-size: 12px;
  padding: 0;
  text-decoration: underline;
}
.details-body .v input {
  width: 70px;
}
.task-splitter:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: -1px;
}
@media (max-width: 850px) {
  .task-properties,
  .task-splitter {
    display: none;
  }
}
</style>

<style scoped>
.study-machine {
  padding: 8px 20px;
  border-bottom: 1px solid var(--border);
  color: var(--muted);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
}
.study-machine p {
  flex-basis: 100%;
  margin: 0;
  color: var(--error-text);
}
</style>
