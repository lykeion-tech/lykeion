<script setup lang="ts">
import SelectPicker from '../../ui/SelectPicker.vue';
import {
  computed,
  defineAsyncComponent,
  defineComponent,
  h,
  nextTick,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { Square } from '@lucide/vue';
import Icon from '../../ui/Icon.vue';
import StudyPullRequests from './StudyPullRequests.vue';
import SurfacePane from '../../ui/SurfacePane.vue';
import StudyOutputs from './StudyOutputs.vue';
import StudyFileThumbnail from './StudyFileThumbnail.vue';
import StudyReview from './StudyReview.vue';
import StudyNotebook from './StudyNotebook.vue';
import StudyDescriptionControls from './StudyDescriptionControls.vue';
import StudyParticipants from './StudyParticipants.vue';
import CommentFiles from './CommentFiles.vue';
import CommentFilePicker from './CommentFilePicker.vue';
import { uploadAttachment } from '../../api/queries';
import { useAttachments } from '../../lib/attachments';
import { registerDraftVisit } from '../../state/resource-draft';
import AgentRunCard from '../runs/AgentRunCard.vue';
import TeamName from '../teams/TeamName.vue';
import ExecutionLog from '../runs/ExecutionLog.vue';
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
  useCardReview,
  useCards,
  useComments,
  useMe,
  useKindRecords,
  useMembers,
  useNotebookRecords,
  useRuns,
  useRuntimes,
  useWorkspace,
} from '../../api/queries';
import type { Card, Column } from '../../api/types';
import {
  blockedActions,
  blockedMessage,
  columnName,
  endLabel,
  stateLabel,
  stopReason,
  stoppedReason,
  studyNumber,
  studyActivity,
  groupStudyActivity,
  runActivityAt,
  transcript,
} from '../../lib/studies';
import { reviewTimeline } from '../../lib/review';
import { notify } from '../../state/notice';
import { usePins } from '../../state/pins';
import { composerAvailability, deliveryLine } from './comment-delivery';
const typesafeIcon = `${import.meta.env.BASE_URL}brand/typesafe.png`;

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

const props = defineProps<{ id: string; embedded?: boolean; citation?: string }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const card = useCard(() => props.id);
const cards = useCards();
const comments = useComments(() => props.id);
const events = useCardEvents(() => props.id);
const activityRecords = useNotebookRecords(() => props.id);
const outputFiles = useKindRecords(() => props.id, 'output');
const generatedFiles = computed(() => {
  if (!outputFiles.isSuccess.value) return [];
  const seen = new Set<string>();
  const counts = new Map<string, number>();
  const files = (outputFiles.data.value ?? []).flatMap((file) => {
    if (!file.key || seen.has(file.key)) return [];
    seen.add(file.key);
    const name = typeof file.body.name === 'string' ? file.body.name : file.key;
    counts.set(name, (counts.get(name) ?? 0) + 1);
    return [{ ...file, name }];
  });
  return files.map((file) => {
    if (counts.get(file.name) === 1) return { ...file, provenance: '' };
    const position = agentRuns.value.findIndex((run) => run.id === file.agent_run_id) + 1;
    const stage = columnName(file.board_column);
    const provenance = `${stage} · ${position ? `Run ${position}` : `Round ${file.round}`} · ${file.key}`;
    return { ...file, provenance };
  });
});
const review = useCardReview(() => props.id);
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
const activityNow = ref(Date.now());
let reviewPoll: ReturnType<typeof setInterval> | undefined;
onMounted(() => {
  reviewPoll = setInterval(() => {
    activityNow.value = Date.now();
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
const reviewRequest = ref<{ column: Column } | null>(null);
const paneFocused = ref(false);
const studyOutputs = ref<InstanceType<typeof StudyOutputs>>();
const paneSurface = computed({
  get: () => activeSurface.value,
  set: (surface: string | null) => {
    if (surface === null && activeSurface.value === 'files' && studyOutputs.value?.hasPreview) {
      studyOutputs.value.back();
      return;
    }
    activeSurface.value = surface;
  },
});
const notebookRun = ref<string | null>(null);
const notebookScrollRequest = ref(0);
const studySurfaces = [
  { id: 'execution', label: 'Timeline', icon: 'chart' },
  { id: 'review', label: 'Review', icon: 'shield' },
  { id: 'notebook', label: 'Notebook', icon: 'code' },
  { id: 'files', label: 'Files', icon: 'file' },
  { id: 'pull-requests', label: 'Pull requests', icon: 'link' },
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
const agentsWorking = computed(
  () =>
    c.value?.state === 'working' &&
    !!cards.data.value?.find((item) => item.id === c.value!.id)?.agent_running,
);
const propertyStateLabel = computed(() => {
  const current = c.value;
  if (!current) return '';
  if (current.state === 'working' && !agentsWorking.value) return '';
  return stateLabel(current);
});
// While agents work on an empty composer, Send becomes Stop; typing brings
// Send back so a comment can still steer them.
const showStop = computed(
  () => agentsWorking.value && !comment.value.trim() && !commentFiles.items.value.length,
);
const services = computed(() => c.value?.services ?? []);
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
    { label: 'Activity', query: activityRecords },
    { label: 'Generated files', query: outputFiles },
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
  return 'jev-waiting';
}
const entries = computed(() => {
  const columns = review.data.value ?? [];
  const entries = transcript(
    (comments.data.value ?? []).filter(
      (comment) =>
        !comment.reply_run_id ||
        !agentRuns.value.some((run) => run.id === comment.reply_run_id && runActivityAt(run)),
    ),
    agentRuns.value,
    [],
    reviewTimeline(events.data.value ?? [], columns, c.value, activityNow.value),
  );
  entries.push(
    ...studyActivity(events.data.value ?? [], activityRecords.data.value ?? [], (id) => {
      const member = members.data.value?.find((item) => item.user_id === id);
      const run = agentRuns.value.find((item) => item.id === id);
      return member?.name || member?.email || run?.agent_name || '';
    }),
  );
  entries.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  return entries;
});
const groupedEntries = computed(() => groupStudyActivity(entries.value));
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
const composerUnavailable = computed(() =>
  composerAvailability(
    c.value?.state ?? '',
    agentRuns.value.filter(
      (run) => run.board_column === c.value?.board_column && run.round === c.value?.round,
    ),
    runtimes.data.value ?? [],
  ),
);
const composerHint = computed(() =>
  c.value?.state === 'done' || !!c.value?.ending ? undefined : composerUnavailable.value,
);
const showMeta = ref(false);
let attachmentRequest = 0;
async function previewAttachment(attachment: { id: string }) {
  const visit = generation,
    request = ++attachmentRequest;
  showMeta.value = true;
  activeSurface.value = 'files';
  await nextTick();
  if (visit === generation && request === attachmentRequest)
    studyOutputs.value?.openAttachment(attachment.id);
}
async function previewOutput(key: string) {
  const visit = generation,
    request = ++attachmentRequest;
  showMeta.value = true;
  activeSurface.value = 'files';
  await nextTick();
  if (visit === generation && request === attachmentRequest) studyOutputs.value?.openOutput(key);
}
function openNotebookAt(run: string) {
  notebookRun.value = run;
  notebookScrollRequest.value++;
  showMeta.value = true;
  activeSurface.value = 'notebook';
}
defineExpose({ openNotebookAt });
const comment = ref('');
const commentFiles = useAttachments(uploadAttachment);
registerDraftVisit({
  dirty: () => !!comment.value.trim() || commentFiles.items.value.length > 0,
  discard: () => {
    comment.value = '';
    commentFiles.clear();
  },
  identity: () => `${workspaceId.value}:${props.id}:comment`,
});
const busy = ref(false);
const error = ref('');
const cancellingComment = ref('');
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
    showMeta.value = false;
    activeSurface.value = null;
    reviewRequest.value = null;
    notebookRun.value = null;
    paneFocused.value = false;
    propertyBusy.value = false;
    comment.value = '';
    commentFiles.clear();
    cancellingComment.value = '';
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
  if (
    busy.value ||
    (!text && !commentFiles.ids.value.length) ||
    commentFiles.uploading.value ||
    commentFiles.failed.value ||
    !c.value
  )
    return;
  const attachmentIds = [...commentFiles.ids.value];
  const scope = mutationScope();
  const ownDraftRevision = draftRevision;
  const base = `/api/cards/${scope.id}`;
  busy.value = true;
  error.value = '';
  try {
    await api(
      'POST',
      `${base}/comments`,
      { body: text, ...(attachmentIds.length ? { attachment_ids: attachmentIds } : {}) },
      undefined,
      { workspace: scope.workspace },
    );
    if (!scope.current()) return;
    commentFiles.clear();
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
// Stopping ends the study for good, so it asks first.
async function stop() {
  if (busy.value || !c.value) return;
  if (!confirm(`Stop ${number.value}? Ended studies are never reopened.`)) return;
  const scope = mutationScope();
  const reason = stopReason(comment.value);
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/cards/${scope.id}/stop`, { reason }, undefined, {
      workspace: scope.workspace,
    });
    if (!scope.current()) return;
    await client.invalidateQueries({ queryKey: ['ws', scope.workspace, 'card', scope.id] });
    if (scope.current()) await client.invalidateQueries({ queryKey: keys.cards(scope.workspace) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) busy.value = false;
  }
}
async function cancelComment(id: string) {
  if (cancellingComment.value) return;
  const scope = mutationScope();
  cancellingComment.value = id;
  error.value = '';
  try {
    await api('DELETE', `/api/cards/${scope.id}/comments/${id}`, undefined, undefined, {
      workspace: scope.workspace,
    });
    await client.invalidateQueries({ queryKey: keys.comments(scope.workspace, scope.id) });
  } catch (e) {
    if (scope.current()) error.value = (e as Error).message;
  } finally {
    if (scope.current()) cancellingComment.value = '';
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
    <div class="task-main">
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
            v-if="!(showMeta && paneFocused)"
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
          <section class="study-description" aria-label="Study description">
            <RecordedMarkdown class="lyk-reading" :content="c.question" />
            <StudyDescriptionControls :key="id" :card-id="id" />
          </section>
          <div class="activity-heading">
            <h2>Activity</h2>
            <StudyParticipants
              :runs="agentRuns"
              :agents="agents.data.value ?? []"
              :runtimes="runtimes.data.value ?? []"
              :teams="c.teams ?? {}"
              :owner="members.data.value?.find((member) => member.user_id === c?.owner_id)"
            />
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
          <template
            v-for="entry in groupedEntries"
            :key="
              entry.kind +
              (entry.kind === 'activity-group'
                ? entry.items[0]?.id
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
              <p
                v-if="entry.comment.body"
                class="bubble"
                :class="{ mine: entry.comment.author_id === me.data.value?.id }"
              >
                {{ entry.comment.body }}
              </p>
              <CommentFiles :comment="entry.comment" :study-id="id" />
              <p
                v-if="
                  (!entry.comment.delivery || entry.comment.cancelled) &&
                  deliveryLine(entry.comment.delivery, {
                    working,
                    unavailable: c.state === 'done' || c.ending ? undefined : composerUnavailable,
                    cancelled: entry.comment.cancelled,
                    reply: !!entry.comment.reply_run_id,
                    files: !!entry.comment.attachments?.length,
                    who: roleNames.get(entry.comment.delivery?.role ?? ''),
                  })
                "
                class="delivery type-caption"
                :class="{ mine: entry.comment.author_id === me.data.value?.id }"
              >
                {{
                  deliveryLine(entry.comment.delivery, {
                    working,
                    unavailable: c.state === 'done' || c.ending ? undefined : composerUnavailable,
                    cancelled: entry.comment.cancelled,
                    reply: !!entry.comment.reply_run_id,
                    files: !!entry.comment.attachments?.length,
                    who: roleNames.get(entry.comment.delivery?.role ?? ''),
                  })
                }}
              </p>
            </div>
            <details v-else-if="entry.kind === 'activity-group'" class="activity-group">
              <summary>
                {{ entry.items.length }} {{ entry.items.length === 1 ? 'activity' : 'activities' }}
              </summary>
              <div v-for="item in entry.items" :key="item.id" class="event-line">
                {{ item.text }} · {{ time(item.at) }}
              </div>
            </details>
            <AgentRunCard
              v-else-if="entry.kind === 'run'"
              class="activity-card"
              :run="entry.run"
              :agent="
                (agents.data.value ?? []).find((agent) => agent.id === entry.run.agent_id) ?? null
              "
              :study="studyDisplayTitle(c.title)"
              :position="agentRuns.findIndex((run) => run.id === entry.run.id) + 1"
              :runtime="runtime(entry.run.runtime_id)"
              :study-id="id"
              :author-initial="me.data.value?.id ? name(me.data.value.id)[0] : '?'"
              :author-id="me.data.value?.id"
              :cancelling-comment="cancellingComment"
              :replies="
                (comments.data.value ?? []).filter(
                  (comment) => comment.reply_run_id === entry.run.id,
                )
              "
              @cancel-comment="cancelComment"
              @open-output="previewOutput"
            />
            <div v-else-if="entry.kind === 'jev'" class="jev-card">
              <img class="avatar jev-avatar" :src="typesafeIcon" alt="" />
              <span class="jev-text">
                {{ entry.text.slice(0, entry.text.lastIndexOf(' · ')) }} ·
                <button
                  class="jev-review-link"
                  :class="jevOutcomeClass(entry.text)"
                  @click="
                    reviewRequest = { column: entry.column };
                    showMeta = true;
                    activeSurface = 'review';
                  "
                >
                  {{ entry.text.slice(entry.text.lastIndexOf(' · ') + 3) }}
                </button>
              </span>
            </div>
          </template>
          <RouterLink v-if="c.has_report" class="report-link" :to="'/decisions/report/' + id"
            >Go to the report</RouterLink
          >
          <section
            v-if="generatedFiles.length"
            class="generated-files"
            aria-label="Generated files"
          >
            <hr class="generated-files-divider" />
            <h3 class="generated-files-heading">FILES GENERATED</h3>
            <div class="generated-files-grid">
              <button
                v-for="file in generatedFiles"
                :key="file.id"
                type="button"
                class="generated-file"
                @click="previewOutput(file.key)"
              >
                <StudyFileThumbnail
                  :name="file.name"
                  :size="
                    typeof file.body.size_bytes === 'number' ? file.body.size_bytes : undefined
                  "
                  :url="`/api/cards/${encodeURIComponent(id)}/outputs/${encodeURIComponent(file.key)}`"
                />
                <span>{{ file.name }}</span>
                <small v-if="file.provenance" :title="file.provenance">{{ file.provenance }}</small>
              </button>
            </div>
          </section>
        </div>
      </div>
      <div v-if="c && !c.ending" class="composer-scroll-alignment">
        <div class="pinned-composer">
          <p v-if="blocked.length" class="block-bar">{{ blockedBar }}</p>
          <p v-if="composerHint" id="study-composer-hint" class="type-caption" role="status">
            {{ composerHint }}
          </p>
          <form class="study-comment" @submit.prevent="send">
            <textarea
              v-model="comment"
              placeholder="Leave a comment…"
              aria-label="Comment"
              :aria-describedby="composerHint ? 'study-composer-hint' : undefined"
              @keydown.meta.enter.prevent="send"
              @keydown.ctrl.enter.prevent="send"
            />
            <footer>
              <CommentFilePicker
                :items="commentFiles.items.value"
                :error="commentFiles.error.value"
                :disabled="busy"
                @pick="commentFiles.add"
                @remove="commentFiles.remove"
              />
              <button
                v-if="showStop"
                type="button"
                class="round-send"
                aria-label="Stop study"
                :disabled="busy"
                @click="stop"
              >
                <Square :size="10" fill="currentColor" />
              </button>
              <button
                v-else
                class="round-send"
                type="submit"
                :disabled="
                  busy ||
                  commentFiles.uploading.value ||
                  commentFiles.failed.value ||
                  (!comment.trim() && !commentFiles.ids.value.length)
                "
                aria-label="Send"
                title="Send comment"
              >
                <Icon name="up" :size="18" />
              </button>
            </footer>
          </form>
        </div>
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
      floating
      v-model:open="showMeta"
      v-model:active="paneSurface"
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
              }}<span v-if="propertyStateLabel" class="chip" :class="'chip-' + c.state">{{
                propertyStateLabel
              }}</span
              ><span v-if="c.ending" class="chip ending-chip" :class="'chip-' + c.ending">{{
                endLabel(c)
              }}</span>
            </div>
          </div>
          <p v-if="stoppedReason(c)" class="ending-reason">{{ stoppedReason(c) }}</p>
          <div class="property-row property-assignee">
            <small>Assignee</small>
            <div class="v">
              <span v-if="c.assignee_type === 'team'" class="property-icon">
                <Icon name="team" :size="13" />
              </span>
              <span v-else class="avatar property-avatar property-icon" aria-hidden="true">{{
                c.assignee_id ? assigneeName[0] : '?'
              }}</span>
              <ul v-if="c.assignee_type === 'team' && assignedTeams.length" class="property-list">
                <li v-for="(team, index) in assignedTeams" :key="team">
                  <span v-if="index === 0">Team: </span><TeamName :name="team" />
                </li>
              </ul>
              <span v-else>{{ assigneeName }}</span>
            </div>
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
          <div class="property-row property-project">
            <label for="study-research">Project</label>
            <div class="v">
              <span class="property-icon"><Icon name="folder" :size="13" /></span>
              <SelectPicker>
                <select
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
                </select>
              </SelectPicker>
              <RouterLink
                v-if="c.research_id"
                :to="'/projects/' + c.research_id"
                aria-label="Open Project"
                ><Icon name="right" :size="12"
              /></RouterLink>
            </div>
          </div>
          <div class="property-row">
            <small>Runs on</small>
            <div class="property-machine">
              {{ c.machine_label || (c.machine ? 'Unavailable machine' : 'My machines') }}
              <p v-if="c.machine_notice" role="status">{{ c.machine_notice }}</p>
            </div>
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
              <ul class="property-list attached-files" aria-label="Attached files">
                <li v-for="a in c.attachments" :key="a.id">
                  <button type="button" class="text-button" @click="previewAttachment(a)">
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
        <StudyPullRequests
          v-else-if="active === 'pull-requests'"
          :key="`${workspaceId}:${id}`"
          :card-id="id"
        />
        <StudyReview v-else-if="active === 'review'" :card-id="id" :request="reviewRequest" />
        <StudyNotebook
          v-else-if="active === 'notebook'"
          :card-id="id"
          :run-id="notebookRun"
          :scroll-request="notebookScrollRequest"
        />
        <StudyOutputs
          v-else-if="active === 'files'"
          ref="studyOutputs"
          :card-id="id"
          :attachments="c.attachments"
          :runs="agentRuns"
        />
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
  padding: 16px 20px 24px;
  --property-label-width: 68px;
  --property-column-gap: 10px;
  line-height: 1.4;
}
.study-detail :deep(.surface-focused .study-meta.surface-content) {
  --property-label-width: 88px;
}
.study-detail :deep(.surface-body:has(> .study-meta.surface-content)) {
  scrollbar-width: none;
}
.surface-content > h2 {
  font-size: 16px;
  margin: 0 0 12px;
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
  font-size: 13px;
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
  font-size: 13px;
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
.report-link {
  display: inline-block;
  margin: 8px 0;
  font-size: 14px;
  color: var(--primary-text);
}
.report-link:hover {
  text-decoration: underline;
}
.transcript-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  scrollbar-gutter: stable;
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
  font-size: 13px;
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
  font-size: 13px;
  color: var(--muted);
  white-space: nowrap;
}
.transcript-entry p {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.creation-entry {
  font-size: 13px;
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
  font-size: 13px;
}
.study-meta .ending-reason {
  margin: 0;
  color: var(--muted);
  font-size: 13px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.study-comment .round-send {
  width: 26px;
  height: 26px;
  padding: 0;
}
.composer-scroll-alignment {
  flex: none;
  overflow-y: auto;
  scrollbar-gutter: stable;
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
  font-size: 13px;
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
  font-size: 13px;
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
  font-size: 13px;
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
  font-size: 13px;
  height: 24px;
}
.notebook-link {
  font-size: 13px;
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
  font-size: 13px;
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
  font-size: 13px;
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
  font-size: 13px;
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
.study-description {
  color: var(--text);
}
.study-description :deep(.markdown-body ul) {
  padding-inline-start: 20px;
}
.activity-heading {
  display: flex;
  align-items: center;
  margin: 32px 0 0;
  padding-top: 32px;
  border-top: 1px solid var(--border);
  font-size: 16px;
  line-height: 24px;
  font-weight: 600;
}
.activity-heading h2 {
  margin: 0;
  font: inherit;
}
.activity-card {
  margin: 0;
}
.activity-card + .activity-card {
  margin-top: 12px;
}
.jev-card {
  margin: 40px 0;
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
  object-fit: cover;
}
.jev-text {
  min-width: 0;
  flex: 1;
}
.jev-review-link {
  text-decoration: underline;
  cursor: pointer;
}
.jev-waiting {
  color: #c69c25;
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
  font-size: 13px;
  margin: 8px 0;
  flex-wrap: wrap;
}
.study-evidence > summary {
  color: var(--muted);
  cursor: pointer;
  font-size: 13px;
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
.activity-group {
  color: var(--muted);
  font-size: 12.5px;
  margin: 0;
}
.jev-card:has(+ .activity-group) {
  margin-bottom: 0;
}
.activity-group + .activity-card,
.activity-group + .jev-card {
  margin-top: 0;
}
.generated-files {
  margin-top: 24px;
}
.generated-files-divider {
  border: 0;
  border-top: 1px solid var(--border);
  margin: 0 0 12px;
}
.generated-files-heading {
  font-size: 11px;
  color: var(--muted);
  letter-spacing: 0.04em;
  margin: 0 0 8px;
}
.generated-files-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
}
.generated-file {
  text-align: left;
  min-width: 0;
  overflow: hidden;
  padding: 0;
  border: 1px solid var(--card-border);
  border-radius: 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.generated-file > span {
  display: block;
  padding: 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.generated-file > small {
  display: block;
  padding: 0 8px 8px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.activity-group > summary {
  display: list-item;
  width: fit-content;
  cursor: pointer;
  padding: 10px 0 10px 14px;
}
.event-line {
  display: flex;
  gap: 10px;
  align-items: center;
  font-size: 12.5px;
  color: var(--muted);
  margin: 4px 0 10px 14px;
}
.study-meta {
  padding: 16px;
  font-size: 13px;
}
.study-meta > small {
  display: block;
  color: var(--muted);
  font-size: 13px;
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
  display: grid;
  grid-template-columns: var(--property-label-width) minmax(0, 1fr);
  align-items: start;
  column-gap: var(--property-column-gap);
  padding-block: 6px;
  font-size: 13px;
}
.property-row > small,
.property-row > label {
  color: var(--muted);
  font-size: 13px;
}
.property-row .v {
  margin: 0;
  overflow-wrap: anywhere;
}
.property-list {
  display: grid;
  gap: 4px;
  list-style: none;
  margin: 0;
  min-width: 0;
  padding: 0;
  overflow-wrap: anywhere;
}
.property-project {
  align-items: center;
}
.property-assignee .v,
.property-project .v {
  flex-wrap: nowrap;
}
.property-assignee .v {
  align-items: flex-start;
}
.property-row select {
  border: 0;
  background: transparent;
  min-width: 0;
  flex: 1;
  font-size: 13px;
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
  grid-template-columns: var(--property-label-width) minmax(0, 1fr);
  gap: 12px var(--property-column-gap);
  align-items: start;
  margin-top: 6px;
}
.study-pane-toggle:hover {
  background: color-mix(in srgb, var(--text) 12%, transparent);
}
.details-body > small {
  color: var(--muted);
  font-size: 13px;
}
.details-body .v {
  font-size: 13px;
  margin: 0;
}
.details-body .services-note {
  grid-column: 1 / -1;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.attached-files li {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 6px;
}
.attached-files small {
  color: var(--muted);
}
.attached-files .text-button {
  font-size: 13px;
  padding: 0;
  min-width: 0;
  text-align: left;
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
.property-machine p {
  margin: 4px 0 0;
  color: var(--error-text);
}
</style>
