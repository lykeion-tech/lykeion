<script lang="ts">
import { ref } from 'vue';
// A webhook's URL is shown only once, right after it is created; the server keeps only its hash.
const freshWebhooks = ref<Record<string, string>>({});
</script>
<script setup lang="ts">
import SelectPicker from '../../ui/SelectPicker.vue';
import { computed, reactive, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api } from '../../api/client';
import { authenticationDataGeneration } from '../../api/auth-loss';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import {
  useAgents,
  useAutoresearch,
  useAutoresearches,
  useAutoresearchRuns,
  useMe,
} from '../../api/queries';
import type { Autoresearch } from '../../api/types';
import { columnName, type Picked, type Range } from '../../lib/studies';
import Modal from '../../ui/Modal.vue';
import AutoresearchRunDialog from './AutoresearchRunDialog.vue';
import Icon from '../../ui/Icon.vue';
import AssignPicker from '../../ui/AssignPicker.vue';
import TimeZonePicker from '../../ui/TimeZonePicker.vue';
import {
  scheduleCron,
  scheduleError,
  scheduleLabel,
  toggleWeekday,
  weekdayAbbreviations,
  type Schedule,
} from './schedule';
import { notify } from '../../state/notice';

const props = defineProps<{ id?: string }>();
const list = useAutoresearches(),
  detail = useAutoresearch(() => props.id ?? ''),
  runs = useAutoresearchRuns(() => props.id ?? ''),
  me = useMe();
const client = useQueryClient(),
  router = useRouter();

const templates = [
  {
    name: 'Literature watch',
    icon: 'book',
    text: 'Track new papers, datasets, and evidence for your research',
  },
  {
    name: 'Hypothesis review',
    icon: 'sparkles',
    text: 'Check predictions, falsifiability, and competing explanations',
  },
  {
    name: 'Experiment monitor',
    icon: 'flask',
    text: 'Review experiment results, failures, and next steps',
  },
  {
    name: 'Weekly research report',
    icon: 'chart',
    text: 'Summarize evidence, experiments, and research…',
  },
  {
    name: 'Reproducibility audit',
    icon: 'shield',
    text: 'Check methods, data provenance, and experiment…',
  },
  {
    name: 'Manuscript evidence check',
    icon: 'file',
    text: 'Verify claims, citations, and figures against the evidence',
  },
];
const agents = useAgents();
const modes = [
  { id: 'create_card', name: 'Create a study', text: 'Each run puts a new study on the board' },
  { id: 'run_only', name: 'Run only', text: 'Each run reports back, no study created' },
] as const;

const selectedRunId = ref('');
const editor = ref(false),
  name = ref(''),
  runbook = ref(''),
  assignee = ref<Picked | null>(null),
  range = ref<Range>({ from: 'grounding', to: 'validation' }),
  picker = ref<InstanceType<typeof AssignPicker>>(),
  mode = ref<Autoresearch['execution_mode']>('create_card'),
  trigger = ref<'schedule' | 'webhook'>('schedule'),
  editingCron = ref(false),
  busy = ref(false),
  error = ref('');
const schedule = reactive<Schedule>(blankSchedule());
function blankSchedule(): Schedule {
  return {
    kind: 'schedule',
    timeMode: 'time',
    time: '09:00',
    interval: 1,
    unit: 'hours',
    windowStart: '00:00',
    windowEnd: '23:00',
    dayMode: 'every',
    weekdays: [1],
    monthDay: 1,
    timezone: me.data.value?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
    cron: '',
  };
}
function open(preset = '') {
  name.value = preset;
  runbook.value = '';
  assignee.value = null;
  range.value = { from: 'grounding', to: 'validation' };
  mode.value = 'create_card';
  trigger.value = 'schedule';
  editingCron.value = false;
  Object.assign(schedule, blankSchedule());
  error.value = '';
  editor.value = true;
}
watch(
  [workspaceId, () => props.id],
  () => {
    editor.value = false;
    selectedRunId.value = '';
  },
  { flush: 'sync' },
);

const invalid = computed(
  () =>
    busy.value ||
    !name.value.trim() ||
    !runbook.value.trim() ||
    !assignee.value ||
    picker.value?.unstaged ||
    (trigger.value === 'schedule' && !!scheduleError(schedule)),
);

// A single Team keeps its own stage, so the server takes no range for it; a
// Team set always runs its range.
const scope = (a: Picked) =>
  a.type === 'team' && Object.keys(a.teams).length === 1 && !a.agent_id && !picker.value?.set
    ? {}
    : { scope: range.value };

async function create() {
  if (invalid.value) return;
  const ws = workspaceId.value;
  const generation = authenticationDataGeneration();
  busy.value = true;
  error.value = '';
  try {
    const ap = await api<Autoresearch>('POST', '/api/autoresearch', {
      title: name.value.trim(),
      description: runbook.value,
      execution_mode: mode.value,
      assignee: assignee.value,
      ...scope(assignee.value!),
    });
    // ponytail: two requests; a refused trigger leaves the autoresearch without one, said in a notice.
    try {
      const made = await api<{ url?: string }>(
        'POST',
        `/api/autoresearch/${ap.id}/triggers`,
        trigger.value === 'schedule'
          ? { kind: 'schedule', cron: scheduleCron(schedule), tz: schedule.timezone }
          : { kind: 'webhook' },
      );
      if (made.url) freshWebhooks.value[ap.id] = new URL(made.url, location.origin).href;
    } catch (e) {
      if (
        (e instanceof Error && e.name === 'AbortError') ||
        generation !== authenticationDataGeneration()
      )
        return;
      notify(`Autoresearch created, but its trigger was refused: ${(e as Error).message}`);
    }
    editor.value = false;
    await client.invalidateQueries({ queryKey: keys.autoresearches(ws) });
    if (generation !== authenticationDataGeneration()) return;
    await router.push('/autoresearch/' + ap.id);
  } catch (e) {
    if (
      (e instanceof Error && e.name === 'AbortError') ||
      generation !== authenticationDataGeneration()
    )
      return;
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}

async function act(path: string, method = 'POST') {
  const ws = workspaceId.value,
    id = props.id!;
  try {
    await api(method, `/api/autoresearch/${id}${path}`);
    await client.invalidateQueries({ queryKey: keys.autoresearches(ws) });
  } catch (e) {
    notify((e as Error).message);
  }
}
async function remove() {
  const ap = detail.data.value;
  if (!ap || !confirm(`Delete “${ap.title}”? Its triggers stop and its runs are removed.`)) return;
  try {
    await api('DELETE', `/api/autoresearch/${ap.id}`);
    delete freshWebhooks.value[ap.id];
    await client.invalidateQueries({ queryKey: keys.autoresearches(workspaceId.value) });
    await router.push('/autoresearch');
  } catch (e) {
    notify((e as Error).message);
  }
}
// Who works each run, and over which stages.
function assigneeLine(a: Autoresearch) {
  const range =
    a.scope_from === a.scope_to
      ? columnName(a.scope_from)
      : `${columnName(a.scope_from)} → ${columnName(a.scope_to)}`;
  if (a.assignee_type === 'team')
    return `${a.scope_from === a.scope_to ? 'a Team' : 'a Team set'} · ${range}`;
  const agent = agents.data.value?.find((x) => x.id === a.assignee_id)?.name ?? 'an agent';
  return `${agent} · ${range}`;
}
const when = (t: string | null) => (t ? new Date(t).toLocaleString() : '—');
const failure = computed(() => (props.id ? detail.error.value : list.error.value));
</script>
<template>
  <section class="directory-page">
    <template v-if="id"
      ><RouterLink to="/autoresearch" class="text-button">← Autoresearch</RouterLink>
      <div v-if="detail.data.value" class="research-detail">
        <header>
          <h1>{{ detail.data.value.title }}</h1>
          <span class="push-right" />
          <button
            v-if="detail.data.value.status === 'active'"
            class="button"
            @click="act('/pause')"
          >
            Pause
          </button>
          <button v-else class="button" @click="act('/resume')">Resume</button>
          <button
            class="button"
            :disabled="detail.data.value.status !== 'active'"
            @click="act('/run').then(() => runs.refetch())"
          >
            <Icon name="play" :size="14" />Run now
          </button>
          <button class="button" @click="remove">Delete</button>
        </header>
        <p class="muted">
          {{ modes.find((m) => m.id === detail.data.value!.execution_mode)?.name }} ·
          {{ detail.data.value.status === 'active' ? 'Active' : 'Paused' }}
          <template v-if="detail.data.value.pause_reason">
            · {{ detail.data.value.pause_reason }}</template
          >
        </p>
        <p class="muted">Assigned to {{ assigneeLine(detail.data.value) }}</p>
        <h2>Runbook</h2>
        <p class="description runbook">{{ detail.data.value.description }}</p>
        <h2>Triggers</h2>
        <label v-if="freshWebhooks[detail.data.value.id]" class="webhook-url"
          >Copy this URL now; it is not shown again.
          <input
            :value="freshWebhooks[detail.data.value.id]"
            readonly
            aria-label="Webhook URL"
            @focus="($event.target as HTMLInputElement).select()"
        /></label>
        <div class="research-studies">
          <div v-for="t in detail.data.value.triggers" :key="t.id" class="trigger-row">
            <Icon :name="t.kind === 'schedule' ? 'clock' : 'link'" /><span
              >{{ t.kind === 'schedule' ? t.cron : `Webhook (${t.provider})`
              }}<small
                >{{
                  t.kind === 'schedule'
                    ? `${t.tz} · next run ${when(t.next_run_at)}`
                    : 'Fires on each POST to its URL'
                }}{{ t.enabled ? '' : ' · disabled' }}</small
              ></span
            >
          </div>
          <p v-if="!detail.data.value.triggers.length" class="muted">
            No triggers. Run it by hand.
          </p>
        </div>
        <h2>Runs</h2>
        <div class="research-studies">
          <component
            :is="r.card_id ? 'RouterLink' : 'button'"
            v-for="r in runs.data.value ?? []"
            :key="r.id"
            :to="r.card_id ? '/studies/' + r.card_id : undefined"
            :type="r.card_id ? undefined : 'button'"
            class="trigger-row run-row"
            @click="!r.card_id && (selectedRunId = r.id)"
            ><Icon name="zap" /><span
              >{{ r.status.charAt(0).toUpperCase() + r.status.slice(1)
              }}<small
                >{{ r.source }} · {{ when(r.planned_at)
                }}{{ r.reason ? ' · ' + r.reason : '' }}</small
              ></span
            ><span class="run-destination">{{
              r.card_id ? 'Open study' : r.report ? 'Open report' : 'View run'
            }}</span
            ><Icon name="right" :size="14" />
          </component>
          <p v-if="runs.error.value" class="inline-error" role="alert">
            {{ runs.error.value.message }} <button @click="runs.refetch()">Retry</button>
          </p>
          <p v-if="runs.isSuccess.value && !runs.data.value?.length" class="muted">No runs yet.</p>
        </div>
      </div></template
    ><template v-else
      ><Teleport to="#page-header-actions">
        <button class="button primary" @click="open()">
          <Icon name="plus" :size="14" />New autoresearch
        </button>
      </Teleport>
      <div class="directory-list">
        <RouterLink v-for="a in list.data.value ?? []" :key="a.id" :to="'/autoresearch/' + a.id"
          ><Icon name="zap" /><span
            >{{ a.title
            }}<small
              >{{ modes.find((m) => m.id === a.execution_mode)?.name }} ·
              {{ a.status === 'active' ? 'Active' : 'Paused' }}</small
            ></span
          ><Icon name="right"
        /></RouterLink>
      </div>
      <div v-if="list.isSuccess.value && !list.data.value?.length" class="autoresearch-content">
        <Icon name="zap" :size="40" />
        <p>No autoresearch yet</p>
        <small
          >Schedule recurring work for your agents. Pick a template or start from scratch.</small
        >
        <div class="template-grid">
          <button v-for="t in templates" :key="t.name" @click="open(t.name)">
            <Icon :name="t.icon" :size="20" /><span
              >{{ t.name }}<small>{{ t.text }}</small></span
            >
          </button>
        </div>
        <button class="button primary" @click="open()">
          <Icon name="plus" :size="14" />Start from scratch
        </button>
      </div></template
    >
    <p v-if="failure" class="inline-error" role="alert">
      {{ failure.message }}
      <button @click="id ? detail.refetch() : list.refetch()">Retry</button>
    </p>
    <p v-if="id ? detail.isPending.value : list.isPending.value" role="status">
      Loading Autoresearch…
    </p>
    <AutoresearchRunDialog
      v-if="selectedRunId"
      :key="workspaceId + ':' + selectedRunId"
      :run-id="selectedRunId"
      @close="selectedRunId = ''"
    />
    <Modal v-if="editor" class="hide-scrollbar" title="New autoresearch" @close="editor = false"
      ><form class="directory-form autoresearch-form" @submit.prevent="create">
        <h2>New autoresearch</h2>
        <label>Name<input v-model="name" required aria-label="Autoresearch name" /></label>
        <label
          >Runbook <small>Read by the agent on every run</small
          ><textarea
            v-model="runbook"
            required
            aria-label="Runbook"
            placeholder="# Goal&#10;What should the agent accomplish?&#10;&#10;# Steps&#10;1. ..."
          />
        </label>
        <span class="field-label">Assign <small>Works every run</small></span>
        <div class="property-line">
          <AssignPicker ref="picker" v-model="assignee" v-model:range="range" agents menu-below />
        </div>
        <span class="field-label">Output</span>
        <div class="choice-row">
          <button
            v-for="m in modes"
            :key="m.id"
            type="button"
            :class="['choice', { active: mode === m.id }]"
            :aria-pressed="mode === m.id"
            @click="mode = m.id"
          >
            {{ m.name }}<small>{{ m.text }}</small>
          </button>
        </div>
        <span class="field-label">Trigger</span>
        <div class="choice-row">
          <button
            type="button"
            :class="['choice', { active: trigger === 'schedule' }]"
            :aria-pressed="trigger === 'schedule'"
            @click="trigger = 'schedule'"
          >
            Schedule
          </button>
          <button
            type="button"
            :class="['choice', { active: trigger === 'webhook' }]"
            :aria-pressed="trigger === 'webhook'"
            @click="trigger = 'webhook'"
          >
            Webhook
          </button>
        </div>
        <template v-if="trigger === 'schedule'">
          <div class="choice-row">
            <button
              type="button"
              :class="['choice', { active: schedule.timeMode === 'time' }]"
              @click="schedule.timeMode = 'time'"
            >
              At a time
            </button>
            <button
              type="button"
              :class="['choice', { active: schedule.timeMode === 'interval' }]"
              @click="schedule.timeMode = 'interval'"
            >
              At an interval
            </button>
          </div>
          <input
            v-if="schedule.timeMode === 'time'"
            v-model="schedule.time"
            type="time"
            required
            aria-label="Scheduled time"
          />
          <div v-else class="inline-fields">
            Every
            <input
              v-model.number="schedule.interval"
              type="number"
              min="1"
              :max="schedule.unit === 'hours' ? 23 : 59"
              aria-label="Interval"
            />
            <SelectPicker
              ><select v-model="schedule.unit" aria-label="Interval unit">
                <option value="hours">hours</option>
                <option value="minutes">minutes</option>
              </select></SelectPicker
            >
            between
            <input
              v-model="schedule.windowStart"
              type="time"
              step="3600"
              aria-label="Window start"
            />
            and
            <input v-model="schedule.windowEnd" type="time" step="3600" aria-label="Window end" />
          </div>
          <label
            >Days<SelectPicker
              ><select v-model="schedule.dayMode" aria-label="Schedule days">
                <option value="every">Every day</option>
                <option value="week">Days of week</option>
                <option value="month">Day of month</option>
              </select></SelectPicker
            ></label
          >
          <div v-if="schedule.dayMode === 'week'" class="choice-row">
            <button
              v-for="(day, i) in weekdayAbbreviations"
              :key="day"
              type="button"
              :class="['choice', 'day', { active: schedule.weekdays.includes(i) }]"
              :aria-pressed="schedule.weekdays.includes(i)"
              @click="schedule.weekdays = toggleWeekday(schedule.weekdays, i)"
            >
              {{ day }}
            </button>
          </div>
          <input
            v-if="schedule.dayMode === 'month'"
            v-model.number="schedule.monthDay"
            type="number"
            min="1"
            max="31"
            aria-label="Day of month"
          />
          <label
            >Timezone<TimeZonePicker v-model="schedule.timezone" :automatic="false" compact
          /></label>
          <p class="muted">
            <Icon name="clock" :size="12" /> {{ scheduleLabel(schedule) }} ·
            <code>{{ scheduleCron(schedule) }}</code>
          </p>
          <button type="button" class="text-button" @click="editingCron = !editingCron">
            {{ editingCron ? 'Use the fields above' : 'Custom cron expression' }}
          </button>
          <input
            v-if="editingCron"
            v-model="schedule.cron"
            placeholder="0 9 * * *"
            aria-label="Cron expression"
          />
          <p v-if="scheduleError(schedule)" class="inline-error">{{ scheduleError(schedule) }}</p>
        </template>
        <p v-else class="muted">
          Lykeion makes a URL for this autoresearch. Each POST to it starts a run.
        </p>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <footer>
          <button type="button" class="button" @click="editor = false">Cancel</button
          ><button class="button primary" :disabled="invalid">Create autoresearch</button>
        </footer>
      </form></Modal
    >
  </section>
</template>
<style src="../../ui/directory.css" />
<style scoped>
.autoresearch-content {
  display: flex;
  align-items: center;
  flex-direction: column;
  padding: 64px 20px 24px;
  color: var(--muted);
}
.autoresearch-content > p {
  margin-top: 14px;
}
.autoresearch-content > small {
  margin-top: 6px;
  font-size: 13px;
}
.template-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(180px, 248px));
  gap: 12px;
  margin: 24px 0 16px;
}
.template-grid button {
  border: 1px solid var(--border);
  border-radius: 8px;
  text-align: left;
  padding: 14px 12px;
  display: flex;
  gap: 12px;
  min-height: 80px;
  color: var(--text);
  font-size: 14px;
}
.template-grid button:hover {
  background: var(--input);
}
.template-grid button > .icon {
  color: var(--muted);
  margin-top: 1px;
}
.template-grid small,
.choice small {
  display: block;
  font-size: 13px;
  color: var(--muted);
  line-height: 18px;
  margin-top: 3px;
}
.research-detail > header {
  gap: 8px;
}
.runbook {
  white-space: pre-wrap;
}
.trigger-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 8px;
}
.run-row {
  width: 100%;
  text-align: left;
  font-size: 13px;
}
.run-row:hover {
  background: var(--input);
}
.run-destination {
  margin-left: auto;
  white-space: nowrap;
  padding: 4px 7px;
  border-radius: 5px;
  font-size: 13px;
  color: var(--muted);
  background: var(--input);
}
.trigger-row small {
  display: block;
  color: var(--muted);
}
.webhook-url {
  display: grid;
  gap: 6px;
  margin-bottom: 10px;
  font-size: 13px;
}
.webhook-url input {
  font-family: var(--font-mono, monospace);
}
.field-label {
  font-size: 13px;
  font-weight: 500;
}
.choice-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.choice {
  flex: 1;
  text-align: left;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 13px;
}
.choice.day {
  flex: 0 0 auto;
  text-align: center;
}
.choice.active {
  border-color: var(--text);
  background: var(--input);
}
.inline-fields {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 13px;
}
.inline-fields input[type='number'] {
  width: 64px;
}
.inline-fields select,
.inline-fields input[type='time'] {
  width: auto;
  min-width: 0;
}
@media (max-width: 800px) {
  .template-grid {
    grid-template-columns: 1fr;
  }
}
</style>
