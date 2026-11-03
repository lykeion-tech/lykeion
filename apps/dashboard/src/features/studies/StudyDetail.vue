<script setup lang="ts">
import SelectPicker from '../../ui/SelectPicker.vue';
import {
  computed,
  defineAsyncComponent,
  defineComponent,
  h,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { Square } from '@lucide/vue';
import SurfacePane from '../../ui/SurfacePane.vue';
import StudyOutputs from './StudyOutputs.vue';
import StudyReview from './StudyReview.vue';
import StudyNotebook from './StudyNotebook.vue';
import StudyDescriptionControls from './StudyDescriptionControls.vue';
import AgentRunCard from '../runs/AgentRunCard.vue';
import TeamName from '../teams/TeamName.vue';
import ExecutionLog from '../runs/ExecutionLog.vue';
import ColumnRating from './ColumnRating.vue';
import { api } from '../../api/client';
import { useWorkflows } from '../../api/workflows';
import { formatSize } from '../../lib/attachments';
import { studyDisplayTitle } from '../../lib/studies';
import { agentDisplayName } from '../../lib/agent-name';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import {
  useAgents,
  useResearches,
  useCard,
  useCardEvents,
  useCardRatings,
  useCardReview,
  useCards,
  useComments,
  useKindRecords,
  useMe,
  useMembers,
  useRuns,
  useRuntimes,
  useWorkspace,
} from '../../api/queries';
import type { Card } from '../../api/types';
import {
  blockedActions,
  blockedMessage,
  columnName,
  endLabel,
  stateLabel,
  studyNumber,
  validationLines,
  transcript,
} from '../../lib/studies';
import { feedLine, reviewingLine } from '../../lib/review';
import { notify } from '../../state/notice';
import { usePins } from '../../state/pins';
import { readText, writeText } from '../../storage';
import { deliveryLine } from './comment-delivery';

const PlainDescription = defineComponent({
  props: { content: { type: String, default: '' } },
  setup: (props) => () => h('p', { class: 'study-description-text' }, props.content),
});
const RecordedMarkdown = defineAsyncComponent({
  loader: () => import('../../ui/RecordedMarkdown.vue'),
  loadingComponent: PlainDescription,
  errorComponent: PlainDescription,
  delay: 0,
});

const props = defineProps<{ id: string; embedded?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const card = useCard(() => props.id);
const cards = useCards();
const comments = useComments(() => props.id);
const events = useCardEvents(() => props.id);
const ratings = useCardRatings(() => props.id);
const review = useCardReview(() => props.id);
const useStatuses = useKindRecords(() => props.id, 'validation_status');
const statusLines = computed(() => validationLines(useStatuses.data.value ?? []));
const openRatings = computed(() => (ratings.data.value ?? []).filter((r) => !r.dismissed));
const runs = useRuns(() => props.id);
const members = useMembers();
const agents = useAgents();
const workflows = useWorkflows();
const researches = useResearches();
const studyElement = ref<HTMLElement>();
const studyWidth = ref(0);
const inspectorMinimum = 240;
const studyMinimum = 400;
const embeddedConstrained = computed(
  () => props.embedded && studyWidth.value < inspectorMinimum + studyMinimum,
);
let studyObserver: ResizeObserver | undefined;
let reviewPoll: ReturnType<typeof setInterval> | undefined;
onMounted(() => {
  reviewPoll = setInterval(() => {
    const current = card.data.value;
    const roundRuns = (runs.data.value ?? []).filter(
      (run) => run.board_column === current?.board_column && run.round === current?.round,
    );
    if (
      current?.state === 'working' &&
      current.assignee_type !== 'agent' &&
      roundRuns.length &&
      roundRuns.every((run) => !['queued', 'dispatched', 'running'].includes(run.status)) &&
      !(review.data.value ?? []).some(
        (column) =>
          column.column === current.board_column &&
          column.attempts.some((attempt) => attempt.round === current.round),
      )
    ) {
      void card.refetch();
    }
  }, 5000);
  if (!studyElement.value) return;
  studyWidth.value = studyElement.value.getBoundingClientRect().width;
  studyObserver = new ResizeObserver(([entry]) => {
    if (entry) studyWidth.value = entry.contentRect.width;
  });
  studyObserver.observe(studyElement.value);
});
onUnmounted(() => {
  studyObserver?.disconnect();
  clearInterval(reviewPoll);
});
const activeSurface = ref<string | null>(null);
const paneFocused = ref(false);
const notebookRun = ref<string | null>(null);
const notebookScrollRequest = ref(0);
const studySurfaces = [
  { id: 'execution', label: 'Timeline', icon: 'chart' },
  { id: 'review', label: 'Review', icon: 'shield' },
  { id: 'notebook', label: 'Notebook', icon: 'code' },
  { id: 'files', label: 'Files', icon: 'file' },
  { id: 'properties', label: 'Properties', icon: 'settings' },
];
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
const resourceReads = computed(() =>
  [
    { label: 'Comments', query: comments },
    { label: 'Events', query: events },
    { label: 'Runs', query: runs },
    { label: 'Review', query: review },
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
function jevOutcomeClass(text: string) {
  const outcome = text.slice(text.lastIndexOf(' · ') + 3);
  if (outcome.startsWith('good to go')) return 'jev-pass';
  if (outcome === 'needs another try' || outcome === 'sent back to Experimentation')
    return 'jev-redo';
  if (outcome === 'nothing to review yet') return 'jev-empty';
  return 'jev-waiting';
}
const entries = computed(() => {
  const columns = review.data.value ?? [];
  const entries = transcript(
    (comments.data.value ?? []).filter(
      (comment) =>
        !comment.reply_run_id || !agentRuns.value.some((run) => run.id === comment.reply_run_id),
    ),
    agentRuns.value,
    (events.data.value ?? []).filter((e) => e.type !== 'card.created'),
    columns.flatMap((column) =>
      column.attempts.map((attempt) => ({
        at: attempt.at,
        text: feedLine(attempt),
        column: attempt.column,
      })),
    ),
  );
  const current = c.value;
  if (!current || !review.isSuccess.value || !runs.isSuccess.value) return entries;
  const line = reviewingLine(current, agentRuns.value, columns);
  if (!line) {
    if (!columns.some((column) => column.attempts.length)) {
      entries.push({
        kind: 'jev',
        at: current.created_at,
        text: `Jev · ${columnName(current.board_column)} · nothing to review yet`,
        column: current.board_column,
      });
    }
    return entries;
  }
  const newest = agentRuns.value
    .filter((run) => run.board_column === current.board_column && run.round === current.round)
    .at(-1);
  if (newest) {
    // A completed run can lack completed_at in older data.
    entries.push({
      kind: 'jev',
      at: newest.completed_at ?? newest.started_at ?? newest.created_at,
      text: line,
      column: current.board_column,
    });
  }
  return entries;
});
const agentRuns = computed(() =>
  [...(runs.data.value ?? [])].sort(
    (a, b) => Date.parse(a.created_at) - Date.parse(b.created_at) || a.id.localeCompare(b.id),
  ),
);
// Who a comment's receipt names: the agent that worked the role.
const roleNames = computed(
  () =>
    new Map(
      agentRuns.value
        .filter((r) => r.agent_name)
        .map((r) => [r.role, agentDisplayName(r.agent_name!)]),
    ),
);
const working = computed(() => !!c.value && !c.value.ending && c.value.state === 'working');
const waiting = computed(() => agentRuns.value.find((r) => r.waiting)?.waiting ?? '');
const showMeta = ref(readText('lykeion.study-meta') !== 'false');
watch(showMeta, (v) => writeText('lykeion.study-meta', String(v)));
function openNotebookAt(run: string) {
  notebookRun.value = run;
  notebookScrollRequest.value++;
  showMeta.value = true;
  activeSurface.value = 'notebook';
}
defineExpose({ openNotebookAt });
const comment = ref('');
const busy = ref(false);
const error = ref('');
const canStop = computed(
  () =>
    working.value &&
    agentRuns.value.some((run) => run.status === 'running' || run.status === 'dispatched'),
);
const showStop = computed(() => canStop.value && !comment.value.trim());
const blockedBar = computed(() => (c.value ? blockedMessage(c.value) : ''));
const blocked = computed(() => (c.value ? blockedActions(c.value, me.data.value?.id) : []));
const assignedTeams = computed(() => [
  ...new Set(Object.values(c.value?.teams ?? {}).map((t) => t.name)),
]);
const assigneeName = computed(() => {
  const x = c.value;
  if (x?.assignee_type === 'team') {
    return 'Team';
  }
  if (!x?.assignee_id) return 'Unassigned';
  if (x.assignee_type === 'member') return name(x.assignee_id);
  return agentDisplayName(
    agents.data.value?.find((a) => a.id === x.assignee_id)?.name ?? 'Unavailable agent',
  );
});
// An agent working with no Team has nobody auditing it.
const worksAlone = computed(
  () => c.value?.assignee_type === 'agent' && !Object.keys(c.value.teams ?? {}).length,
);
let draftRevision = 0;
watch(
  comment,
  () => {
    draftRevision++;
  },
  { flush: 'sync' },
);
let generation = 0;
watch(
  [workspaceId, () => props.id],
  () => {
    generation++;
    activeSurface.value = null;
    notebookRun.value = null;
    paneFocused.value = false;
    propertyBusy.value = false;
    comment.value = '';
    busy.value = false;
    error.value = '';
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
function runtime(runtimeId: string | null) {
  return runtimes.data.value?.find((x) => x.id === runtimeId);
}
const time = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
// Each continuation belongs to its captured visit.
async function send() {
  const text = comment.value.trim();
  if (busy.value || !text || !c.value) return;
  const scope = mutationScope();
  const ownDraftRevision = draftRevision;
  const base = `/api/cards/${scope.id}`;
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `${base}/comments`, { body: text });
    if (!scope.current()) return;
    if (ownDraftRevision === draftRevision) {
      comment.value = '';
    }
    await client.invalidateQueries({ queryKey: ['ws', scope.workspace, 'card', scope.id] });
    if (scope.current()) await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) busy.value = false;
  }
}
async function submitComposer() {
  if (!showStop.value) return send();
  if (
    busy.value ||
    !confirm(`Stop ${number.value}? This ends the study and it cannot be reopened.`)
  )
    return;
  const scope = mutationScope();
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/cards/${scope.id}/stop`, { reason: 'Stopped by user' });
    if (!scope.current()) return;
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
    class="study-detail"
    :class="[
      embedded ? 'embedded-study' : 'desktop-study',
      { 'embedded-constrained': embeddedConstrained },
    ]"
    aria-label="Study"
  >
    <div class="task-main" :hidden="showMeta && paneFocused">
      <header class="study-heading">
        <button
          v-if="!embedded"
          class="icon-button back-study"
          aria-label="Back to studies"
          @click="emit('close')"
        >
          <Icon name="back" /></button
        ><span>{{ number }} {{ studyDisplayTitle(c?.title ?? '') }}</span>
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
            v-if="!showMeta"
            class="icon-button study-pane-toggle"
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
          <h1 class="study-title">{{ studyDisplayTitle(c.title) }}</h1>
          <p v-if="waiting" class="study-waiting" role="status">{{ waiting }}</p>
          <section class="study-description" aria-label="Study description">
            <RecordedMarkdown class="lyk-reading" :content="c.question" />
            <StudyDescriptionControls
              :key="id"
              :card-id="id"
              :attachments="c.attachments ?? []"
              @download="download"
            />
          </section>
          <h2 class="activity-heading">Activity</h2>
          <ul
            v-if="statusLines.length"
            class="validation-statuses type-body"
            aria-label="Validation"
          >
            <li v-for="(line, i) in statusLines" :key="i">{{ line }}</li>
          </ul>
          <ColumnRating v-for="r in openRatings" :key="r.column" :card-id="id" :offer="r" />
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
                : entry.kind === 'jev'
                  ? `${entry.column}:${entry.at}:${entry.text}`
                  : entry.kind === 'run'
                    ? entry.run.id
                    : entry.comment.id)
            "
          >
            <div v-if="entry.kind === 'comment'" class="activity-comment">
              <div class="who" :class="{ mine: entry.comment.author_id === me.data.value?.id }">
                <span class="avatar">{{ name(entry.comment.author_id)[0] }}</span
                >{{ name(entry.comment.author_id) }}<span class="tm">{{ time(entry.at) }}</span>
              </div>
              <p class="bubble" :class="{ mine: entry.comment.author_id === me.data.value?.id }">
                {{ entry.comment.body }}
              </p>
              <p
                v-if="
                  deliveryLine(entry.comment.delivery, {
                    working,
                    who: roleNames.get(entry.comment.delivery?.role ?? ''),
                  })
                "
                class="delivery type-caption"
                :class="{ mine: entry.comment.author_id === me.data.value?.id }"
              >
                {{
                  deliveryLine(entry.comment.delivery, {
                    working,
                    who: roleNames.get(entry.comment.delivery?.role ?? ''),
                  })
                }}
              </p>
            </div>
            <div v-else-if="entry.kind === 'event'" class="event-line">
              <span class="dot" />{{ entry.text }} · {{ time(entry.at) }}
            </div>
            <AgentRunCard
              v-else-if="entry.kind === 'run'"
              class="activity-card"
              :run="entry.run"
              :study="studyDisplayTitle(c.title)"
              :position="agentRuns.findIndex((run) => run.id === entry.run.id) + 1"
              :runtime="runtime(entry.run.runtime_id)"
              :study-id="id"
              :author-initial="me.data.value?.id ? name(me.data.value.id)[0] : '?'"
              :replies="
                (comments.data.value ?? []).filter(
                  (comment) => comment.reply_run_id === entry.run.id,
                )
              "
              @notebook="openNotebookAt"
            />
            <div v-else-if="entry.kind === 'jev'" class="jev-card">
              <span class="avatar jev-avatar" aria-hidden="true">J</span>
              <span class="jev-text">
                {{ entry.text.slice(0, entry.text.lastIndexOf(' · ')) }} ·
                <span :class="jevOutcomeClass(entry.text)">{{
                  entry.text.slice(entry.text.lastIndexOf(' · ') + 3)
                }}</span>
              </span>
              <button
                class="jev-review-link"
                @click="
                  showMeta = true;
                  activeSurface = 'review';
                "
              >
                Open Review <span aria-hidden="true">→</span>
              </button>
            </div>
          </template>
        </div>
      </div>
      <div v-if="c && !c.ending" class="pinned-composer">
        <p v-if="blocked.length" class="block-bar">{{ blockedBar }}</p>
        <form class="study-comment" @submit.prevent="submitComposer">
          <textarea
            v-model="comment"
            placeholder="Leave a comment…"
            aria-label="Comment"
            @keydown.meta.enter.prevent="send"
            @keydown.ctrl.enter.prevent="send"
          />
          <footer>
            <button
              class="round-send"
              :class="{ 'stop-control': showStop }"
              type="submit"
              :disabled="busy || (!comment.trim() && !canStop)"
              :aria-label="showStop ? 'Stop' : 'Send'"
              :title="showStop ? 'Stop' : 'Send comment'"
            >
              <Square
                v-if="showStop"
                :size="12"
                fill="currentColor"
                :stroke-width="0"
                aria-hidden="true"
              />
              <Icon v-else name="up" :size="18" />
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
    <SurfacePane
      v-if="c"
      v-model:open="showMeta"
      v-model:active="activeSurface"
      v-model:focused="paneFocused"
      :surfaces="studySurfaces"
      :default-width="240"
      :restore-width="false"
      hide-title
      :container-width="studyWidth || undefined"
      :width-key="
        embedded ? 'lykeion.embedded-study-inspector-width' : 'lykeion.study-inspector-width'
      "
    >
      <template #default="{ active }">
        <div v-if="active === 'properties'" class="study-meta surface-content">
          <h2>Properties</h2>
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
            ><span class="v"
              ><template v-if="c.assignee_type === 'team' && assignedTeams.length"
                >Team:
                <template v-for="(team, i) in assignedTeams" :key="team"
                  ><span v-if="i">, </span><TeamName :name="team" /></template></template
              ><template v-else>{{ assigneeName }}</template></span
            ><span v-if="worksAlone" class="chip">No auditor</span>
          </div>
          <div v-if="c.workflow" class="property-row">
            <small>Workflow</small>
            <span class="v">
              <RouterLink
                v-if="workflows.data.value?.workflows.some((w) => w.slug === c?.workflow)"
                :to="`/workflows/${c.workflow}`"
                >{{ workflows.data.value.workflows.find((w) => w.slug === c?.workflow)!.name
                }}<template v-if="c.workflow_release">
                  · {{ c.workflow_release }}</template
                ></RouterLink
              >
              <template v-else-if="workflows.data.value"
                >This workflow is no longer available</template
              >
            </span>
          </div>
          <div class="property-row">
            <label for="study-research">Project</label
            ><span class="property-icon"><Icon name="folder" :size="13" /></span
            ><SelectPicker
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
                </option>
              </select></SelectPicker
            ><RouterLink
              v-if="c.research_id"
              :to="'/projects/' + c.research_id"
              aria-label="Open Project"
              ><Icon name="right" :size="12"
            /></RouterLink>
          </div>
          <div class="study-machine type-caption">
            <span
              >Runs on:
              {{ c.machine_label || (c.machine ? 'Unavailable machine' : 'My machines') }}</span
            >
            <p v-if="c.machine_notice" role="status">{{ c.machine_notice }}</p>
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
          <div class="details-body">
            <small>Owner</small>
            <div class="v metadata-owner">
              <span class="avatar">{{ name(c.owner_id)[0] }}</span
              >{{ name(c.owner_id) }}
            </div>
            <small>Start date</small>
            <div class="v">
              {{
                c.start_date
                  ? new Date(c.start_date).toLocaleDateString(undefined, { timeZone: 'UTC' })
                  : 'None'
              }}
            </div>
            <small>Due date</small>
            <div class="v">
              {{ c.deadline ? new Date(c.deadline).toLocaleDateString() : 'None' }}
            </div>
            <template v-if="c.attachments.length">
              <small>Attached files</small>
              <ul class="v attached-files" aria-label="Attached files">
                <li v-for="a in c.attachments" :key="a.id">
                  <button type="button" class="text-button" @click="download(a)">
                    {{ a.name }}
                  </button>
                  <small>{{ formatSize(a.size_bytes) }}</small>
                </li>
              </ul>
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
        </div>
        <div v-else-if="active === 'execution'" class="study-meta surface-content">
          <ExecutionLog :runs="runs.data.value ?? []" />
        </div>
        <StudyReview v-else-if="active === 'review'" :card-id="id" />
        <StudyNotebook
          v-else-if="active === 'notebook'"
          :card-id="id"
          :run-id="notebookRun"
          :scroll-request="notebookScrollRequest"
        />
        <StudyOutputs v-else-if="active === 'files'" :card-id="id" />
      </template>
    </SurfacePane>
  </section>
</template>
<style scoped>
.study-detail :deep(.surface-header) {
  height: 49px;
  border-bottom: 0;
  padding-right: 18px;
}
.study-detail :deep(.surface-header .surface-icon[aria-label='Close pane']) {
  transform: translateY(-0.5px);
}
.study-meta.surface-content {
  padding: 24px 20px 24px;
}
.surface-content > h2 {
  font-size: 16px;
  margin: 0 0 12px;
}
.task-main[hidden] {
  display: none;
}
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
  background: var(--fill-menu);
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

.desktop-study {
  inset: 0;
  position: absolute;
  background: var(--surface);
  display: flex;
  z-index: 12;
  overflow: hidden;
}
.study-preview {
  left: auto;
  width: min(1180px, calc(100% - 48px));
  border-left: 1px solid var(--border);
  box-shadow: -16px 0 40px #00000010;
}
.study-properties {
  flex: 0 0 var(--properties-width);
  width: var(--properties-width);
  overflow: auto;
  padding: 0 8px 24px;
  font-size: 14px;
  scrollbar-color: var(--scrollbar-thumb) transparent;
}
.study-properties > header {
  height: 60px;
  padding: 0 12px;
  display: flex;
  align-items: center;
  gap: 4px;
}
.study-properties > header h2 {
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
.study-metadata > p {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-left: 0;
}
.study-metadata p > span {
  min-width: 72px;
  color: var(--muted);
}
.study-splitter {
  flex: none;
  width: 1px;
  background: var(--border);
  cursor: col-resize;
  position: relative;
  touch-action: none;
}
.study-splitter::before {
  content: '';
  position: absolute;
  inset: 0 -3px;
  z-index: 2;
}
.study-splitter:hover {
  background: var(--primary);
}
.task-main {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.study-heading {
  height: 49px;
  border-bottom: 1px solid var(--border);
  flex: none;
  padding: 0 20px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
}
.study-heading > span {
  white-space: nowrap;
  text-overflow: ellipsis;
  overflow: hidden;
}
.study-heading > .push-right {
  display: flex;
}
.study-heading .back-study {
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
.study-description-text {
  padding: 0;
  text-align: left;
  white-space: pre-wrap;
  line-height: 23px;
  display: block;
  color: var(--muted);
}
.study-title-input {
  width: 100%;
  font-size: 24px;
  font-weight: 600;
  border: 0;
  background: var(--input);
  padding: 6px;
  border-radius: 5px;
  margin-bottom: 12px;
}
.study-description-input {
  width: 100%;
  min-height: 100px;
  resize: none;
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
.study-comment {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 8px 12px;
  min-height: 72px;
  margin-top: 24px;
}
.study-comment textarea {
  height: 26px;
  display: block;
  border: 0;
  background: none;
  outline: none;
  width: 100%;
  min-height: 26px;
  max-height: 180px;
  resize: none;
  line-height: 22px;
  font-size: 14px;
  padding: 0;
}
.study-comment footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  min-height: 24px;
  gap: 12px;
}
.study-comment footer > small {
  margin-right: auto;
  color: var(--muted);
  font-size: 11px;
}
.study-comment .round-send {
  width: 26px;
  height: 26px;
  padding: 0;
}
.study-comment .round-send.stop-control {
  background: var(--primary-text);
  color: #000;
}
.pinned-composer {
  width: 100%;
  max-width: 1002px;
  margin: 0 auto;
  padding: 0 16px 24px;
  flex: none;
}
.pinned-composer .study-comment {
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
.study-claims {
  margin-top: 30px;
}
.study-claims h2 {
  font-size: 16px;
}
.study-claims article {
  border-top: 1px solid var(--border);
  padding: 16px 0;
  line-height: 23px;
}
.study-claims small {
  color: var(--muted);
  overflow-wrap: anywhere;
}
.detail-loading {
  padding: 24px;
  color: var(--muted);
  font-size: 14px;
}
@media (max-width: 1150px) {
  .study-properties {
    flex-basis: min(var(--properties-width), 280px);
    width: min(var(--properties-width), 280px);
  }
  .transcript-content,
  .pinned-composer {
    padding-left: 24px;
    padding-right: 24px;
  }
  .study-preview .study-properties {
    flex-basis: 240px;
    width: 240px;
  }
  .study-heading {
    padding: 0 16px;
  }
}
.has-inspector .transcript-content,
.has-inspector .pinned-composer {
  padding-left: 30px;
  padding-right: 30px;
}
.inspector-focused > .study-properties,
.inspector-focused > .study-splitter,
.inspector-focused > .task-main {
  display: none;
}
.study-inspector {
  width: var(--inspector-width);
  flex: none;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--surface);
  overflow: hidden;
}
.inspector-focused .study-inspector {
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
  .study-inspector {
    width: min(var(--inspector-width), 320px);
  }
  .has-inspector .study-properties {
    display: none;
  }
  .has-inspector > .study-splitter {
    display: none;
  }
  .has-inspector .transcript-content,
  .has-inspector .pinned-composer {
    padding-left: 20px;
    padding-right: 20px;
  }
}
.embedded-constrained > .study-properties,
.embedded-constrained > .study-splitter {
  display: none;
}
.embedded-study {
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
  margin-bottom: 20px;
  overflow-wrap: anywhere;
}
.study-waiting {
  margin: -12px 0 20px;
  font-size: 13px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.study-description {
  color: var(--text);
}
.study-description :deep(.markdown-body ul) {
  padding-inline-start: 20px;
}
.activity-heading {
  margin: 32px 0 16px;
  padding-top: 32px;
  border-top: 1px solid var(--border);
  font-size: 16px;
  line-height: 24px;
  font-weight: 600;
}
.activity-card,
.jev-card {
  margin: 12px 0;
}
.jev-card {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid var(--card-border);
  border-radius: 8px;
  background: #ffffff16;
  font-size: 14px;
}
.jev-avatar {
  color: #b5841b;
  background: #b5841b30;
  font-weight: 600;
}
.jev-text {
  min-width: 0;
  flex: 1;
}
.jev-review-link {
  flex: none;
  color: var(--primary);
}
.jev-review-link:hover {
  text-decoration: underline;
}
.jev-waiting {
  color: #c69c25;
}
.jev-empty {
  color: var(--muted);
}
.jev-redo {
  color: #ef4444;
}
.jev-pass {
  color: #22a65a;
}
.jev-review-link:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 4px;
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
.superseded-mark {
  color: #8a6a1c;
  font-size: 12px;
  white-space: nowrap;
}
.activity-comment {
  margin: 24px 0;
}
.who {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13.5px;
  margin: 0 0 6px 14px;
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
  margin: 0 0 0 14px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.bubble.mine {
  margin-left: auto;
  margin-right: 0;
  background: var(--fill-active);
}
.delivery {
  color: var(--muted);
  margin: 8px 0 0 14px;
}
.delivery.mine {
  text-align: right;
  margin-left: 0;
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
.study-meta .ending-chip {
  max-width: 100%;
  white-space: normal;
  overflow-wrap: anywhere;
}
</style>

<style scoped>
.study-properties {
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
.study-properties :deep(.execution-log) {
  margin-top: 26px;
}
.details-body {
  display: grid;
  grid-template-columns: 75px minmax(0, 1fr);
  gap: 14px 6px;
  align-items: center;
  margin-top: 20px;
}
.study-pane-toggle:hover {
  background: color-mix(in srgb, var(--text) 12%, transparent);
}
.details-body > small {
  color: var(--muted);
  font-size: 12px;
}
.details-body .v {
  font-size: 12px;
  margin: 0;
}
.details-body .attached-files,
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
.study-splitter:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: -1px;
}
@media (max-width: 850px) {
  .study-properties,
  .study-splitter {
    display: none;
  }
}
</style>

<style scoped>
.study-machine {
  padding: 16px 0 0;
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
