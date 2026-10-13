<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue';
import { SquareTerminal, Globe, Trash2, UserRoundPlus, Rocket, Webhook } from '@lucide/vue';
import TimeZonePicker from './settings/TimeZonePicker.vue';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
import { service, state, refresh, workspaceName } from '../services/workspace';
const props = defineProps<{ kind: string; preset?: string }>();
const emit = defineEmits<{ close: [] }>();
const autopilotExpanded = ref(false);
const editorForm = ref<HTMLFormElement>(),
  memberMenu = ref(false),
  memberQuery = ref('');
const memberMatches = computed(() =>
  (state.data?.agents ?? []).filter(
    (x) => x.id !== agentId.value && x.name.toLowerCase().includes(memberQuery.value.toLowerCase()),
  ),
);
const kind = ref(props.kind),
  name = ref(props.preset ?? ''),
  description = ref(''),
  instructions = ref(''),
  agentId = ref(''),
  members = ref<string[]>([]),
  transport = ref('STDIO'),
  format = ref('Visual editor'),
  command = ref(''),
  url = ref(''),
  argumentsList = ref<string[]>([]),
  env = ref<{ key: string; value: string }[]>([]),
  json = ref(''),
  error = ref(''),
  busy = ref(false),
  output = ref('Create task'),
  projectId = ref(''),
  os = ref('macOS / Linux');
const memberSummary = computed(() =>
  members.value.length
    ? members.value
        .map((id) => state.data?.agents.find((agent) => agent.id === id)?.name ?? id)
        .join(', ')
    : 'Add agents or workspace members',
);
const headers = ref<{ key: string; value: string }[]>([]),
  extraConfig = ref<Record<string, unknown>>({});
function connectorConfig() {
  return {
    ...extraConfig.value,
    ...(transport.value === 'STDIO'
      ? {
          type: 'stdio',
          command: command.value,
          ...(argumentsList.value.length ? { args: argumentsList.value } : {}),
          ...(env.value.length
            ? { env: Object.fromEntries(env.value.map((x) => [x.key, x.value])) }
            : {}),
        }
      : {
          type: 'http',
          url: url.value,
          ...(headers.value.length
            ? { headers: Object.fromEntries(headers.value.map((x) => [x.key, x.value])) }
            : {}),
        }),
  };
}
function readConnectorJson() {
  const value: unknown = JSON.parse(json.value);
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Configuration must be a JSON object.');
  const config = value as Record<string, unknown>;
  if (!['stdio', 'http'].includes(String(config.type)))
    throw new Error('Transport type must be stdio or http.');
  if (config.type === 'stdio' && typeof config.command !== 'string')
    throw new Error('A STDIO configuration needs a command string.');
  if (config.type === 'http' && typeof config.url !== 'string')
    throw new Error('An HTTP configuration needs a URL string.');
  if (
    config.args !== undefined &&
    (!Array.isArray(config.args) || config.args.some((x) => typeof x !== 'string'))
  )
    throw new Error('Arguments must be an array of strings.');
  for (const key of ['env', 'headers']) {
    const entry = config[key];
    if (
      entry !== undefined &&
      (!entry ||
        typeof entry !== 'object' ||
        Array.isArray(entry) ||
        Object.values(entry).some((x) => typeof x !== 'string'))
    )
      throw new Error('Headers and environment values must be string maps.');
  }
  return config;
}
function changeFormat(next: string) {
  error.value = '';
  if (next === format.value) return;
  if (next === 'JSON') {
    json.value = JSON.stringify(connectorConfig(), null, 2);
    format.value = next;
    return;
  }
  try {
    const config = readConnectorJson();
    transport.value = config.type === 'stdio' ? 'STDIO' : 'Streamable HTTP';
    command.value = typeof config.command === 'string' ? config.command : '';
    url.value = typeof config.url === 'string' ? config.url : '';
    argumentsList.value = (config.args as string[] | undefined) ?? [];
    env.value = Object.entries((config.env as Record<string, string> | undefined) ?? {}).map(
      ([key, value]) => ({ key, value }),
    );
    headers.value = Object.entries(
      (config.headers as Record<string, string> | undefined) ?? {},
    ).map(([key, value]) => ({ key, value }));
    extraConfig.value = Object.fromEntries(
      Object.entries(config).filter(
        ([key]) => !['type', 'command', 'url', 'args', 'env', 'headers'].includes(key),
      ),
    );
    format.value = next;
  } catch (e) {
    error.value =
      e instanceof SyntaxError ? 'Configuration is not valid JSON.' : (e as Error).message;
  }
}
const triggerKind = ref<'schedule' | 'webhook'>('schedule'),
  timeMode = ref<'time' | 'interval'>('time'),
  atTime = ref('09:00'),
  interval = ref(1),
  intervalUnit = ref<'minutes' | 'hours'>('hours'),
  windowStart = ref('00:00'),
  windowEnd = ref('23:00'),
  dayMode = ref<'every' | 'week' | 'month'>('every'),
  weekdays = ref<number[]>([1]),
  monthDay = ref(1),
  timezone = ref(Intl.DateTimeFormat().resolvedOptions().timeZone),
  cron = ref(''),
  editingCron = ref(false),
  eventKey = ref(''),
  eventValue = ref(''),
  eventFilters = ref<{ key: string; value: string }[]>([]),
  subscriberIds = ref<string[]>([]),
  subscriberMenu = ref(false);
const dayNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const scheduleSummary = computed(() =>
  triggerKind.value === 'webhook'
    ? 'Webhook'
    : cron.value
      ? `Cron ${cron.value} (${timezone.value})`
      : `${timeMode.value === 'time' ? 'At ' + atTime.value : 'Every ' + interval.value + ' ' + intervalUnit.value + ' between ' + windowStart.value + ' and ' + windowEnd.value} · ${dayMode.value === 'every' ? 'Every day' : dayMode.value === 'week' ? weekdays.value.map((x) => dayNames[x]).join(', ') : 'Day ' + monthDay.value + ' of the month'} · ${timezone.value}`,
);
const displaySchedule = computed(() => {
  if (cron.value) return cron.value;
  const days =
    dayMode.value === 'every'
      ? 'Every day'
      : dayMode.value === 'week'
        ? weekdays.value.map((x) => dayNames[x]).join(', ')
        : `Day ${monthDay.value}`;
  const timing =
    timeMode.value === 'time'
      ? `At ${atTime.value}`
      : intervalUnit.value === 'hours'
        ? interval.value === 1
          ? 'Every hour at :00'
          : `Every ${interval.value} hours`
        : `Every ${interval.value} minutes`;
  return `${timing} · ${days}${timeMode.value === 'interval' && (windowStart.value !== '00:00' || windowEnd.value !== '23:00') ? ' · ' + windowStart.value + '–' + windowEnd.value : ''}`;
});
const triggerSpec = computed(() =>
  triggerKind.value === 'webhook'
    ? { kind: 'webhook' as const, eventFilters: eventFilters.value }
    : {
        kind: 'schedule' as const,
        timeMode: timeMode.value,
        time: atTime.value,
        interval: interval.value,
        unit: intervalUnit.value,
        windowStart: windowStart.value,
        windowEnd: windowEnd.value,
        dayMode: dayMode.value,
        weekdays: weekdays.value,
        monthDay: monthDay.value,
        timezone: timezone.value,
        cron: cron.value,
      },
);
function toggleDay(day: number) {
  weekdays.value = weekdays.value.includes(day)
    ? weekdays.value.filter((x) => x !== day)
    : [...weekdays.value, day].sort();
}
function addEventFilter() {
  if (!eventKey.value.trim() || !eventValue.value.trim()) return;
  eventFilters.value.push({ key: eventKey.value.trim(), value: eventValue.value.trim() });
  eventKey.value = '';
  eventValue.value = '';
}
const selectedFiles = ref<File[]>([]),
  folderInput = ref<HTMLInputElement>(),
  archiveInput = ref<HTMLInputElement>(),
  importRuntime = ref(''),
  runtimeSearch = ref('');
function pickFiles(event: Event) {
  selectedFiles.value = [...((event.target as HTMLInputElement).files ?? [])];
  error.value = '';
}
const importHelp: Record<string, string> = {
  'import-skill': 'Fetch a published skill by URL.',
  'import-local': 'Pick a folder with SKILL.md, or a .skill / .zip archive.',
  'import-runtime': 'Promote a skill installed on your local runtime.',
};
const titles: Record<string, string> = {
  team: 'Create Team',
  connector: 'Add connector',
  skills: 'New skill',
  skill: 'Create manually',
  'import-skill': 'Import from URL',
  'import-local': 'Import from local',
  'import-runtime': 'Copy from runtime',
  autopilot: 'New autopilot',
  machine: 'Add a computer',
  daemon: 'Daemon',
};
watch(
  kind,
  async () => {
    memberMenu.value = false;
    await nextTick();
    editorForm.value?.querySelector<HTMLInputElement>('[autofocus]')?.focus();
  },
  { immediate: true },
);
const title = computed(() => titles[kind.value] ?? kind.value);
async function save() {
  busy.value = true;
  error.value = '';
  try {
    switch (kind.value) {
      case 'team':
        await service.value.createResource('teams', {
          name: name.value,
          description: description.value,
          leaderAgentId: agentId.value || null,
          agentIds: [...new Set([agentId.value, ...members.value].filter(Boolean))],
        });
        break;
      case 'skill':
        await service.value.createResource('skills', {
          name: name.value,
          description: description.value,
          instructions: instructions.value,
        });
        break;
      case 'autopilot':
        if (triggerKind.value === 'schedule' && dayMode.value === 'week' && !weekdays.value.length)
          throw new Error('Choose at least one day of the week.');
        await service.value.createResource('autopilots', {
          name: name.value,
          instructions: instructions.value,
          schedule: scheduleSummary.value,
          triggerSpec: triggerSpec.value,
          subscriberIds: subscriberIds.value,
          agentId: agentId.value || null,
          outputMode: output.value === 'Run only' ? 'run' : 'task',
          projectId: projectId.value || null,
        });
        break;
      case 'connector':
        if (format.value === 'JSON') readConnectorJson();
        throw new Error(
          'Connector transport configuration requires the backend secret store. Nothing has been saved.',
        );
      default:
        throw new Error(
          'Importing skills requires a connected import service. Nothing has been saved.',
        );
    }
    await refresh();
    emit('close');
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <Modal
    :title="title"
    :class="[
      'resource-editor',
      'editor-' + kind,
      { 'autopilot-expanded': kind === 'autopilot' && autopilotExpanded },
    ]"
    @close="emit('close')"
  >
    <form ref="editorForm" @submit.prevent="save">
      <header>
        <button
          v-if="['skill', 'import-skill', 'import-local', 'import-runtime'].includes(kind)"
          type="button"
          class="icon-button"
          aria-label="Back"
          @click="kind = 'skills'"
        >
          <Icon name="back" />
        </button>
        <template v-if="kind === 'autopilot'"
          ><span class="autopilot-header-icon"><Rocket :size="13" :stroke-width="1.5" /></span
          ><span>New autopilot</span><span class="autopilot-header-muted">·</span
          ><span class="autopilot-header-muted">Recurring AI work</span
          ><Icon name="right" :size="12" /><span class="autopilot-header-muted">{{
            workspaceName
          }}</span
          ><button
            type="button"
            class="icon-button push-right"
            :aria-label="
              autopilotExpanded ? 'Collapse autopilot editor' : 'Expand autopilot editor'
            "
            @click="autopilotExpanded = !autopilotExpanded"
          >
            <Icon name="expand" :size="15" /></button
        ></template>
        <span v-else
          >{{ title
          }}<small v-if="kind === 'skills'">Choose how to add a skill to this workspace.</small>
          <small v-if="kind === 'skill'">Start from a blank SKILL.md.</small>
          <small v-if="importHelp[kind]">{{ importHelp[kind] }}</small>
        </span>
        <button
          type="button"
          :class="['icon-button', { 'push-right': kind !== 'autopilot' }]"
          aria-label="Close"
          @click="emit('close')"
        >
          <Icon name="close" />
        </button>
      </header>
      <div v-if="kind === 'skills'" class="skill-choices">
        <button type="button" @click="kind = 'skill'">
          <span class="square">
            <Icon name="plus" />
          </span>
          <span>Create manually<small>Start from a blank SKILL.md.</small> </span>
          <Icon name="right" :size="12" class="push-right" />
        </button>
        <button type="button" @click="kind = 'import-local'">
          <span class="square">
            <Icon name="folder" />
          </span>
          <span
            >Import from local<small
              >Pick a folder with SKILL.md, or a .skill / .zip archive.</small
            >
          </span>
          <Icon name="right" :size="12" class="push-right" />
        </button>
        <button type="button" @click="kind = 'import-skill'">
          <span class="square">
            <Icon name="download" />
          </span>
          <span>Import from URL<small>Fetch a published skill by URL.</small> </span>
          <Icon name="right" :size="12" class="push-right" />
        </button>
        <button type="button" @click="kind = 'import-runtime'">
          <span class="square">
            <Icon name="archive" />
          </span>
          <span
            >Copy from runtime<small>Promote a skill installed on your local runtime.</small>
          </span>
          <Icon name="right" :size="12" class="push-right" />
        </button>
      </div>
      <div v-else-if="kind === 'team'" class="team-body editor-body">
        <div class="team-identity">
          <Icon name="users" :size="32" />
          <div>
            <label
              >Name<input v-model="name" required autofocus placeholder="e.g. Frontend Team" />
            </label>
            <label
              >Description<input
                v-model="description"
                maxlength="255"
                placeholder="Describe what this team is responsible for..."
              />
              <small class="counter">{{ description.length }} / 255</small>
            </label>
          </div>
        </div>
        <label
          >Leader Agent<small
            >The leader receives all tasks assigned to this team and coordinates the team.</small
          >
          <div class="team-select">
            <UserRoundPlus :size="16" :stroke-width="1.5" /><select
              v-model="agentId"
              aria-label="Leader agent"
            >
              <option value="">Select a leader agent</option>
              <option v-for="agent in state.data?.agents" :key="agent.id" :value="agent.id">
                {{ agent.name }}
              </option></select
            ><Icon name="down" :size="14" />
          </div>
        </label>
        <div class="team-members-field">
          <label
            ><span>Additional Members <span class="optional">(optional)</span></span>
            <small>The leader can delegate sub-tasks to these members.</small> </label
          ><button
            type="button"
            class="team-member-trigger"
            :aria-expanded="memberMenu"
            @click="memberMenu = !memberMenu"
          >
            <UserRoundPlus :size="16" :stroke-width="1.5" /><span>{{ memberSummary }}</span
            ><Icon name="down" :size="14" />
          </button>
          <div
            v-if="memberMenu"
            class="team-member-menu"
            @keydown.esc.stop.prevent="memberMenu = false"
          >
            <input
              v-model="memberQuery"
              placeholder="Search agents..."
              aria-label="Search team members"
            /><label v-for="agent in memberMatches" :key="agent.id"
              ><input v-model="members" type="checkbox" :value="agent.id" />{{ agent.name }}</label
            >
            <p v-if="!memberMatches.length">
              {{ state.data?.agents.length ? 'No matching agents' : 'No agents available' }}
            </p>
            <small>Workspace-member assignment requires a team-members adapter.</small>
          </div>
        </div>
      </div>
      <div v-else-if="kind === 'skill' || kind === 'import-skill'" class="editor-body">
        <label v-if="kind === 'skill'"
          >Name<input v-model="name" required autofocus placeholder="e.g. review-helper" />
          <small>Must be unique within the workspace.</small>
        </label>
        <label v-else
          >Skill URL<input
            v-model="url"
            required
            autofocus
            type="url"
            placeholder="https://clawhub.ai/owner/skill"
          />
        </label>
        <label v-if="kind === 'skill'"
          ><span class="skill-description-label"><Icon name="edit" :size="12" />Description</span
          ><textarea
            v-model="description"
            placeholder="One sentence on when to assign this skill to an agent."
          />
        </label>
        <div v-if="kind === 'import-skill'" class="source-formats">
          <small>Source URL formats</small>
          <div>
            <button
              v-for="source in [
                { name: 'ClawHub', url: 'https://clawhub.ai/owner/skill' },
                { name: 'Skills.sh', url: 'https://skills.sh/owner/skill' },
                { name: 'GitHub', url: 'https://github.com/owner/repo' },
              ]"
              :key="source.name"
              type="button"
              @click="url = source.url"
            >
              {{ source.name }}<code>{{ source.url.replace('https://', '') }}</code>
            </button>
          </div>
        </div>
      </div>
      <div v-else-if="kind === 'import-local'" class="editor-body local-import">
        <div>
          <button type="button" class="button compact" @click="folderInput?.click()">
            <Icon name="folder" :size="14" />Choose folder
          </button>
          <button type="button" class="button compact" @click="archiveInput?.click()">
            <Icon name="file" :size="14" />Choose .skill / .zip
          </button>
        </div>
        <input ref="folderInput" type="file" webkitdirectory multiple hidden @change="pickFiles" />
        <input ref="archiveInput" type="file" accept=".skill,.zip" hidden @change="pickFiles" />
        <small v-if="selectedFiles.length"
          >{{ selectedFiles.length }} files selected. Import requires a connected import
          adapter.</small
        >
      </div>
      <div v-else-if="kind === 'import-runtime'" class="editor-body runtime-import">
        <label
          >Runtime<select v-model="importRuntime">
            <option value="">Choose runtime</option>
            <option v-for="runtime in state.data?.runtimes" :key="runtime.id" :value="runtime.id">
              {{ runtime.name }}
            </option>
          </select>
        </label>
        <div v-if="importRuntime" class="runtime-import-status">
          <Icon name="archive" :size="14" />
          <span>{{ state.data?.runtimes.find((x) => x.id === importRuntime)?.name }}</span>
          <span class="push-right">{{
            state.data?.runtimes.find((x) => x.id === importRuntime)?.status
          }}</span>
        </div>
        <label class="runtime-import-search">
          <Icon name="search" :size="14" />
          <input
            v-model="runtimeSearch"
            placeholder="Search local skills"
            aria-label="Search local skills"
          />
        </label>
        <label class="runtime-select-all"> <input type="checkbox" disabled />Select all</label>
        <p class="runtime-import-empty">
          {{
            state.data?.runtimes.length
              ? 'Runtime skills require a connected skill-inventory adapter.'
              : 'Connect a runtime to browse its installed skills.'
          }}
        </p>
      </div>
      <div v-else-if="kind === 'connector'" class="editor-body connector-body">
        <label
          >Connector name<input v-model="name" required autofocus pattern="[A-Za-z0-9_-]+" />
          <small
            >Used to identify this connector at runtime. Use letters, numbers, hyphens, and
            underscores.</small
          >
        </label>
        <label>Configuration</label>
        <div class="segmented">
          <button
            v-for="f in ['Visual editor', 'JSON']"
            :key="f"
            type="button"
            :class="{ active: format === f }"
            @click="changeFormat(f)"
          >
            {{ f }}
          </button>
        </div>
        <template v-if="format === 'Visual editor'">
          <label>Transport</label>
          <div class="transport-options">
            <button
              v-for="t in ['STDIO', 'Streamable HTTP']"
              :key="t"
              type="button"
              :class="{ active: transport === t }"
              @click="transport = t"
            >
              <component
                :is="t === 'STDIO' ? SquareTerminal : Globe"
                :size="22"
                :stroke-width="1.7"
              />
              <span
                >{{ t
                }}<small>{{
                  t === 'STDIO'
                    ? 'Start a local command-line process'
                    : 'Connect to a remote HTTP endpoint'
                }}</small>
              </span>
            </button>
          </div>
          <template v-if="transport === 'STDIO'">
            <label>Command<input v-model="command" placeholder="npx" /> </label>
            <div class="inline-label">
              <span>Startup arguments<small>Passed to the command in this order.</small> </span>
              <button type="button" @click="argumentsList.push('')">
                <Icon name="plus" :size="14" />Add argument
              </button>
            </div>
            <div v-for="(_, i) in argumentsList" :key="i" class="connector-row">
              <input v-model="argumentsList[i]" :aria-label="`Argument ${i + 1}`" />
              <button
                type="button"
                class="icon-button"
                :aria-label="`Remove argument ${i + 1}`"
                @click="argumentsList.splice(i, 1)"
              >
                <Icon name="close" :size="14" />
              </button>
            </div>
            <div class="inline-label">
              <span
                >Environment variables<small>Injected only when this connector starts.</small>
              </span>
              <button type="button" @click="env.push({ key: '', value: '' })">
                <Icon name="plus" :size="14" />Add environment variable
              </button>
            </div>
            <div v-for="(entry, i) in env" :key="i" class="connector-row">
              <input v-model="entry.key" :aria-label="`Variable ${i + 1} name`" />
              <input
                v-model="entry.value"
                :aria-label="`Variable ${i + 1} value`"
                type="password"
              />
              <button
                type="button"
                class="icon-button"
                :aria-label="`Remove variable ${i + 1}`"
                @click="env.splice(i, 1)"
              >
                <Icon name="close" :size="14" />
              </button>
            </div>
          </template>
          <template v-else>
            <label
              >Connector URL<input
                v-model="url"
                type="url"
                placeholder="https://mcp.example.com/mcp"
              />
            </label>
            <div class="inline-label">
              <span>HTTP headers<small>Sent with every HTTP request.</small> </span>
              <button type="button" @click="headers.push({ key: '', value: '' })">
                <Icon name="plus" :size="14" />Add header
              </button>
            </div>
            <div v-for="(header, i) in headers" :key="i" class="connector-row">
              <input v-model="header.key" :aria-label="`Header ${i + 1} name`" />
              <input v-model="header.value" :aria-label="`Header ${i + 1} value`" />
              <button
                type="button"
                class="icon-button"
                :aria-label="`Remove header ${i + 1}`"
                @click="headers.splice(i, 1)"
              >
                <Trash2 :size="14" :stroke-width="1.5" />
              </button>
            </div>
          </template>
        </template>
        <label v-else class="json-section">
          <small>Edit the full connector configuration as JSON.</small>Configuration JSON<textarea
            v-model="json"
            class="json-editor"
            aria-label="Connector JSON"
            spellcheck="false"
          />
        </label>
      </div>
      <div v-else-if="kind === 'autopilot'" class="autopilot-editor">
        <div class="autopilot-runbook">
          <input v-model="name" required autofocus placeholder="Autopilot name" />
          <label>RUNBOOK <span>Read by the agent on every run</span> </label>
          <textarea
            v-model="instructions"
            required
            placeholder="# Goal&#10;What should the agent accomplish?&#10;&#10;# Context&#10;Who is this for? Any constraints?&#10;&#10;# Steps&#10;1. ...&#10;2. ..."
          />
        </div>
        <aside>
          <label
            ><span>ASSIGNEE<span class="required-mark">*</span></span>
            <div class="autopilot-assignee">
              <Rocket :size="16" :stroke-width="1.5" /><select
                v-model="agentId"
                aria-label="Autopilot assignee"
              >
                <option value="">Select agent or team</option>
                <option v-for="agent in state.data?.agents" :key="agent.id" :value="agent.id">
                  {{ agent.name }}
                </option></select
              ><Icon name="down" :size="12" />
            </div>
          </label>
          <label>OUTPUT MODE</label>
          <button
            v-for="mode in ['Create task', 'Run only']"
            :key="mode"
            type="button"
            :class="['output-mode', { active: output === mode }]"
            @click="output = mode"
          >
            <span class="output-radio" :class="{ selected: output === mode }"
              ><Icon v-if="output === mode" name="check" :size="10"
            /></span>
            <span
              >{{ mode
              }}<small>{{
                mode === 'Create task'
                  ? 'Each run creates a tracked task'
                  : 'Silent run, no task created'
              }}</small>
            </span>
          </button>
          <label class="autopilot-project"
            >PROJECT<small
              >Also sets where runs execute — the project's repository or local directory</small
            >
            <div class="autopilot-project-select">
              <Icon name="folder" :size="15" /><select
                v-model="projectId"
                aria-label="Autopilot project"
              >
                <option value="">No project</option>
                <option v-for="p in state.data?.projects" :key="p.id" :value="p.id">
                  {{ p.name }}
                </option></select
              ><Icon name="down" :size="12" />
            </div>
          </label>
          <label
            >SUBSCRIBERS<small>Auto-subscribed to every task this autopilot creates</small>
            <button
              type="button"
              class="subscriber-button"
              @click="subscriberMenu = !subscriberMenu"
            >
              + Add subscriber
            </button>
          </label>
          <div v-if="subscriberMenu" class="subscriber-picker">
            <label v-for="member in state.data?.colleagues" :key="member.id">
              <input v-model="subscriberIds" type="checkbox" :value="member.id" />{{
                member.name
              }}</label
            >
            <small v-if="!state.data?.colleagues.length">No workspace members available.</small>
          </div>
          <small v-if="subscriberIds.length">{{ subscriberIds.length }} subscribers selected</small>
          <label>TRIGGER</label>
          <div class="segmented trigger-tabs">
            <button
              type="button"
              :class="{ active: triggerKind === 'schedule' }"
              @click="triggerKind = 'schedule'"
            >
              <Icon name="clock" :size="14" />Schedule
            </button>
            <button
              type="button"
              :class="{ active: triggerKind === 'webhook' }"
              @click="triggerKind = 'webhook'"
            >
              <Webhook :size="14" :stroke-width="1.5" />Webhook
            </button>
          </div>
          <template v-if="triggerKind === 'schedule'">
            <label>SCHEDULE</label>
            <div class="schedule-fields">
              <label>Time</label>
              <div class="segmented">
                <button
                  type="button"
                  :class="{ active: timeMode === 'time' }"
                  @click="timeMode = 'time'"
                >
                  At a time
                </button>
                <button
                  type="button"
                  :class="{ active: timeMode === 'interval' }"
                  @click="timeMode = 'interval'"
                >
                  At an interval
                </button>
              </div>
              <input
                v-if="timeMode === 'time'"
                v-model="atTime"
                type="time"
                required
                aria-label="Scheduled time"
              />
              <div v-else class="interval-fields">
                <label class="interval-value"
                  >Every
                  <input
                    v-model.number="interval"
                    type="number"
                    min="1"
                    :max="intervalUnit === 'hours' ? 23 : 59"
                    aria-label="Interval"
                  />
                  <select v-model="intervalUnit" aria-label="Interval unit">
                    <option value="hours">hours</option>
                    <option value="minutes">minutes</option>
                  </select>
                </label>
                <label class="interval-window">
                  <Icon name="clock" :size="12" />
                  <input v-model="windowStart" type="time" aria-label="Window start" /> ~
                  <input v-model="windowEnd" type="time" aria-label="Window end" />
                </label>
              </div>
              <label
                >Days<select v-model="dayMode" aria-label="Schedule days">
                  <option value="every">Every day</option>
                  <option value="week">Days of week</option>
                  <option value="month">Day of month</option>
                </select>
              </label>
              <div v-if="dayMode === 'week'" class="weekday-picker">
                <button
                  v-for="(day, i) in dayNames"
                  :key="day"
                  type="button"
                  :class="{ active: weekdays.includes(i) }"
                  :aria-pressed="weekdays.includes(i)"
                  @click="toggleDay(i)"
                >
                  {{ day }}
                </button>
              </div>
              <input
                v-if="dayMode === 'month'"
                v-model.number="monthDay"
                type="number"
                min="1"
                max="31"
                aria-label="Day of month"
              />
              <label
                >Timezone<TimeZonePicker v-model="timezone" :automatic="false" compact
              /></label>
              <div class="schedule-summary">
                <Icon name="clock" :size="12" />
                <span>{{ displaySchedule }}</span>
              </div>
              <button type="button" class="cron-toggle" @click="editingCron = !editingCron">
                <Icon name="edit" :size="12" />{{ cron || 'Custom cron expression' }}
              </button>
              <input
                v-if="editingCron"
                v-model="cron"
                placeholder="0 9 * * *"
                aria-label="Cron expression"
              />
              <div class="next-runs">
                <span>Next runs</span>
                <small>Calculated by the scheduling adapter when connected.</small>
              </div>
            </div>
          </template>
          <template v-else>
            <label>WEBHOOK</label>
            <div class="webhook-explanation">
              A connected control plane must provision the webhook URL. Saving this local draft does
              not create an endpoint.
            </div>
            <label class="event-filter-heading">
              <Icon name="filter" :size="12" />EVENT FILTERS</label
            >
            <div class="event-filter-fields">
              <input
                v-model="eventKey"
                placeholder="e.g. workflow_run"
                aria-label="Event filter field"
              />
              <input
                v-model="eventValue"
                placeholder="completed, failed"
                aria-label="Event filter values"
              />
              <button
                type="button"
                :disabled="!eventKey.trim() || !eventValue.trim()"
                @click="addEventFilter"
              >
                <Icon name="plus" :size="12" />Add
              </button>
            </div>
            <div v-for="(filter, i) in eventFilters" :key="i" class="event-filter-row">
              <span>{{ filter.key }}: {{ filter.value }}</span>
              <button
                type="button"
                :aria-label="'Remove event filter ' + (i + 1)"
                @click="eventFilters.splice(i, 1)"
              >
                <Icon name="close" :size="12" />
              </button>
            </div>
            <small>Only process webhooks matching these events. Leave empty to accept all.</small>
          </template>
        </aside>
      </div>
      <div v-else class="editor-body machine-body">
        <p>Connect a computer to make its runtimes available to your agents.</p>
        <label>1. Install the Lykeion CLI</label>
        <div class="segmented">
          <button
            v-for="platform in ['macOS / Linux', 'Windows']"
            :key="platform"
            type="button"
            :class="{ active: os === platform }"
            @click="os = platform"
          >
            {{ platform }}
          </button>
        </div>
        <div class="command-placeholder">
          Installation instructions will be supplied by your connected control plane.
        </div>
        <label>2. Start the daemon</label>
        <div class="command-placeholder">
          Pairing is unavailable until a daemon provider is configured.
        </div>
        <small>No connection has been started.</small>
        <button
          type="button"
          class="button"
          @click="
            emit('close');
            $router.push('/settings/connection');
          "
        >
          Connection settings
        </button>
      </div>
      <p v-if="error" class="editor-error" role="alert">{{ error }}</p>
      <footer v-if="kind !== 'skills'">
        <small v-if="kind === 'autopilot'" class="footer-note"
          ><Icon name="zap" :size="14" />{{
            service.mode === 'local'
              ? 'Saved as an inactive draft.'
              : 'Execution follows your control plane policy.'
          }}</small
        >
        <button type="button" class="button compact push-right" @click="emit('close')">
          Cancel
        </button>
        <button
          v-if="!['machine', 'daemon'].includes(kind)"
          class="button primary compact"
          :disabled="
            busy ||
            (!name.trim() && !kind.startsWith('import-')) ||
            (kind === 'import-skill' && !url.trim()) ||
            (kind === 'import-local' && !selectedFiles.length) ||
            kind === 'import-runtime'
          "
        >
          {{
            busy
              ? 'Saving…'
              : kind === 'team'
                ? 'Create Team'
                : kind === 'skill'
                  ? 'Create skill'
                  : kind === 'connector'
                    ? 'Add'
                    : kind === 'autopilot'
                      ? 'Create autopilot'
                      : kind === 'import-runtime'
                        ? 'Import to Workspace'
                        : 'Import'
          }}
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.resource-editor {
  background: #e1e1e6;
}
.resource-editor form {
  display: flex;
  flex-direction: column;
  max-height: 85vh;
}
.resource-editor header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 18px 20px;
  border-bottom: 1px solid #ffffff40;
  font-size: 16px;
  min-height: 50px;
}
.resource-editor header small {
  display: block;
  font-size: 12px;
  margin-top: 4px;
  color: var(--muted);
}
.editor-body {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  overflow: auto;
}
.resource-editor label {
  display: flex;
  flex-direction: column;
  gap: 7px;
  font-size: 13px;
  color: var(--muted);
}
.resource-editor input,
.resource-editor textarea {
  border: 1px solid #ffffff38;
  border-radius: 7px;
  padding: 7px 10px;
  background: transparent;
  width: 100%;
  outline-offset: 1px;
}
.resource-editor select {
  background: var(--surface);
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 7px;
  width: 100%;
}
.resource-editor small {
  font-size: 12px;
  color: var(--muted);
  line-height: 16px;
}
.resource-editor footer {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 12px 20px;
  border-top: 1px solid #ffffff40;
  min-height: 54px;
}
.editor-team {
  height: 85vh;
}
.editor-team form {
  height: 100%;
}
.editor-team footer {
  margin-top: auto;
  background: var(--surface);
}
.team-identity {
  display: flex;
  gap: 32px;
  padding: 0 0 0 16px;
}
.team-identity > .icon {
  margin-top: 20px;
}
.team-identity > div {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.counter {
  text-align: right;
}
.team-body > label > small {
  margin-top: -4px;
}
.team-body select[multiple] {
  height: 42px;
}
.editor-skills,
.editor-skill,
.editor-import-skill {
  width: 450px;
}
.editor-skill textarea {
  height: 64px;
  resize: vertical;
}
.skill-choices {
  padding: 20px;
  display: grid;
  gap: 8px;
}
.skill-choices > button {
  display: flex;
  align-items: center;
  gap: 12px;
  text-align: left;
  padding: 16px;
  border-radius: 8px;
  background: #edecf1;
  font-size: 14px;
}
.skill-choices small {
  display: block;
  margin-top: 4px;
}
.square {
  width: 36px;
  height: 36px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  background: #e1e1e6;
}
.connector-body {
  padding: 32px;
  gap: 24px;
}
.connector-body > label {
  font-size: 14px;
  color: var(--text);
}
.connector-body > label small {
  margin-top: 0;
}
.segmented {
  display: flex;
  gap: 4px;
}
.segmented > button {
  flex: 1;
  border-radius: 5px;
  height: 28px;
}
.segmented > button.active {
  background: white;
  box-shadow: 0 1px 3px #0002;
}
.transport-options {
  display: flex;
  gap: 12px;
  margin-top: -12px;
}
.transport-options > button {
  border: 1px solid #ffffff38;
  display: flex;
  gap: 16px;
  align-items: center;
  text-align: left;
  border-radius: 8px;
  padding: 14px;
  flex: 1;
  font-size: 14px;
}
.transport-options > button.active {
  border-color: var(--primary);
  background: #d7d9e2;
}
.transport-options small,
.inline-label small {
  display: block;
  margin-top: 3px;
}
.inline-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 14px;
}
.inline-label > button {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
}
.environment-row {
  display: flex;
  gap: 10px;
}
.json-editor {
  min-height: 300px;
  font-family: monospace;
}
.editor-autopilot {
  width: 1024px;
  max-width: calc(100vw - 48px);
}
.editor-autopilot header {
  font-size: 12px;
}
.autopilot-editor {
  height: min(620px, 65vh);
  display: grid;
  grid-template-columns: minmax(400px, 1.7fr) minmax(300px, 1fr);
  overflow: hidden;
}
.autopilot-runbook {
  padding: 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.autopilot-runbook > input {
  padding: 0;
  font-size: 24px;
  font-weight: 600;
  border: 0;
}
.autopilot-runbook > label {
  font-size: 11px;
  display: flex;
  flex-direction: row;
  gap: 8px;
  letter-spacing: 0.5px;
}
.autopilot-runbook > label > span {
  font-size: 12px;
  letter-spacing: 0;
}
.autopilot-runbook textarea {
  resize: none;
  flex: 1;
  background: white;
  padding: 16px;
  line-height: 23px;
}
.autopilot-editor aside {
  padding: 20px;
  border-left: 1px solid #ffffff40;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.autopilot-editor aside > label {
  font-size: 11px;
  letter-spacing: 1px;
  margin-top: 8px;
}
.autopilot-editor aside > label:first-child {
  margin-top: 0;
}
.autopilot-editor aside select,
.autopilot-editor aside input {
  letter-spacing: 0;
  font-size: 14px;
}
.autopilot-editor aside small {
  letter-spacing: 0;
  font-size: 11px;
}
.output-mode {
  display: flex;
  gap: 10px;
  background: white;
  border-radius: 7px;
  text-align: left;
  padding: 10px;
  font-size: 14px;
  border: 1px solid transparent;
}
.output-mode.active {
  border-color: var(--primary);
  background: #d7d9e2;
}
.output-mode small {
  display: block;
  margin-top: 3px;
}
.editor-autopilot footer {
  background: var(--surface);
}
.editor-error {
  padding: 12px 20px;
  color: #9b3030;
  font-size: 13px;
}
.editor-machine,
.editor-daemon {
  width: 512px;
}
.machine-body {
  padding: 24px;
}
.machine-body p {
  font-size: 12px;
  color: var(--muted);
  line-height: 17px;
}
.machine-body > .segmented {
  align-self: flex-start;
}
.command-placeholder {
  font-family: monospace;
  font-size: 13px;
  padding: 12px;
  border-radius: 6px;
  border: 1px solid #ffffff40;
  line-height: 20px;
}
.machine-body > .button {
  align-self: flex-start;
}

.editor-connector > form > header {
  padding: 20px 32px;
  min-height: 66px;
}
.editor-connector footer {
  padding-right: 32px;
}
.connector-body {
  gap: 28px;
}
.connector-body > .segmented {
  margin-top: -12px;
  margin-bottom: 4px;
}
.connector-body > .transport-options {
  margin-top: -14px;
}
.connector-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: -16px;
}
.connector-row > input {
  min-width: 0;
  height: 32px;
  flex: 1;
}
.connector-row > .icon-button {
  flex: none;
  width: 32px;
}
.connector-body input {
  height: 32px;
}
.json-section {
  gap: 4px !important;
}
.json-section > small {
  margin-bottom: -4px;
}
.json-editor {
  height: 256px;
  min-height: 256px;
  resize: vertical;
  font-size: 13px;
  line-height: 20px;
}
.editor-connector .transport-options > button {
  min-height: 64px;
}
.editor-connector .editor-error {
  padding: 0 32px 18px;
}

.trigger-tabs > button {
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}
.autopilot-editor aside > .trigger-tabs {
  margin-bottom: 8px;
}
.schedule-fields {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.schedule-fields > label {
  font-size: 12px !important;
  letter-spacing: 0 !important;
  color: var(--muted);
}
.schedule-fields .segmented > button {
  font-size: 12px;
  height: 26px;
}
.schedule-fields select {
  height: 32px;
  padding: 5px 8px;
  background: transparent;
}
.schedule-fields > input {
  height: 32px;
}
.interval-fields {
  display: flex;
  gap: 8px;
}
.interval-fields > label {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 4px;
  border: 1px solid #ffffff38;
  border-radius: 7px;
  height: 32px;
  padding: 0 7px;
  font-size: 13px;
  color: var(--text);
}
.interval-value {
  flex: 1;
  min-width: 0;
}
.interval-value input {
  width: 30px !important;
  padding: 0 !important;
  border: 0 !important;
  text-align: center;
  -moz-appearance: textfield;
}
.interval-value input::-webkit-inner-spin-button {
  appearance: none;
}
.interval-value select {
  border: 0;
  width: auto;
  font-size: 13px !important;
  padding: 0 !important;
}
.interval-window {
  flex: 1.2;
  min-width: 0;
}
.interval-window input {
  padding: 0 !important;
  border: 0 !important;
  min-width: 0;
  width: 60px !important;
  font-size: 12px !important;
}
.interval-window input::-webkit-calendar-picker-indicator {
  display: none;
}
.weekday-picker {
  display: flex;
  gap: 3px;
}
.weekday-picker > button {
  flex: 1;
  height: 26px;
  font-size: 11px;
  color: var(--muted);
  border-radius: 5px;
}
.weekday-picker > button.active {
  background: var(--text);
  color: var(--surface);
}
.schedule-summary {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  margin-top: 16px;
  line-height: 18px;
}
.schedule-summary > .icon {
  margin-top: 3px;
}
.cron-toggle {
  align-self: flex-start;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--muted);
  font-family: monospace;
  padding: 0 8px;
}
.next-runs {
  border-top: 1px solid #fff3;
  padding: 10px 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 12px;
}
.webhook-explanation {
  padding: 12px;
  background: white;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
  color: var(--muted);
  line-height: 20px;
}
.autopilot-editor aside > .event-filter-heading {
  flex-direction: row;
  align-items: center;
  gap: 4px;
  margin-top: 0;
}
.event-filter-fields {
  display: flex;
  gap: 8px;
}
.event-filter-fields input {
  min-width: 0;
  padding: 7px 8px !important;
  background: white;
  font-size: 12px !important;
}
.event-filter-fields input:first-child {
  flex: 1.2;
  font-family: monospace;
}
.event-filter-fields input:nth-child(2) {
  flex: 1;
}
.event-filter-fields button {
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
  border: 1px solid #fff3;
  border-radius: 5px;
  font-size: 12px;
}
.event-filter-row {
  display: flex;
  justify-content: space-between;
  gap: 6px;
  border: 1px solid #fff3;
  border-radius: 5px;
  font-size: 12px;
  padding: 8px;
  overflow-wrap: anywhere;
}
.subscriber-button {
  align-self: flex-start;
  letter-spacing: 0;
  font-size: 12px;
  color: var(--muted);
}
.subscriber-picker {
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: var(--surface);
  border-radius: 6px;
  padding: 12px;
}
.subscriber-picker label {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  letter-spacing: 0;
}
.subscriber-picker input {
  width: auto;
}

.editor-import-local {
  width: 450px;
}
.editor-import-runtime {
  width: 676px;
}
.source-formats {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.source-formats > div {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}
.source-formats button {
  text-align: left;
  border: 1px solid #ffffff38;
  border-radius: 5px;
  padding: 10px 12px;
  font-size: 12px;
  min-width: 0;
}
.source-formats code {
  display: block;
  font-size: 11px;
  color: var(--primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-top: 4px;
  text-decoration: underline;
}
.local-import > div {
  display: flex;
  gap: 8px;
}
.local-import .button {
  flex: 1;
}
.local-import input[hidden] {
  display: none;
}
.runtime-import select {
  background: transparent;
  height: 32px;
  padding: 4px 10px;
}
.runtime-import-status {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid #fff3;
  border-radius: 6px;
  padding: 8px 10px;
  color: var(--muted);
  font-size: 12px;
  margin-top: -10px;
}
.runtime-import .runtime-import-search {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  border: 1px solid #fff3;
  border-radius: 6px;
  padding: 0 10px;
  margin-top: 6px;
}
.runtime-import-search input {
  border: 0;
  padding-left: 0;
}
.runtime-import .runtime-select-all {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  margin-top: -6px;
}
.runtime-select-all input {
  width: auto;
}
.runtime-import-empty {
  font-size: 13px;
  color: var(--muted);
  text-align: center;
  line-height: 20px;
  padding: 70px 20px;
}
.editor-import-runtime footer:before {
  content: 'Select a skill to continue.';
  font-size: 12px;
  color: var(--muted);
}

.editor-team header {
  height: 50px;
  min-height: 50px;
  padding: 10px 20px;
}
.editor-team header .icon-button {
  width: 24px;
  height: 24px;
}
.editor-team .team-body {
  gap: 16px;
}
.team-body label {
  gap: 4px;
  font-size: 12px;
}
.team-body input {
  height: 32px;
  font-size: 14px;
}
.team-identity {
  gap: 32px;
  padding-left: 16px;
}
.team-identity > div {
  gap: 10px;
}
.team-identity > .icon {
  margin-top: 17px;
  color: var(--muted);
}
.team-body .counter {
  margin-top: 0;
}
.team-body > label > small {
  margin-top: 0;
}
.team-select {
  position: relative;
}
.team-select > svg:first-child {
  position: absolute;
  left: 13px;
  top: 13px;
  pointer-events: none;
}
.team-select > .icon:last-child {
  position: absolute;
  right: 13px;
  top: 14px;
  pointer-events: none;
}
.team-select select {
  height: 42px;
  appearance: none;
  padding: 9px 40px;
  font-size: 14px;
  color: var(--text);
}
.team-members-field {
  position: relative;
}
.team-members-field label {
  gap: 2px;
}
.team-members-field .optional {
  margin-left: 5px;
}
.team-member-trigger {
  margin-top: 7px;
  height: 42px;
  width: 100%;
  display: flex;
  align-items: center;
  gap: 12px;
  background: white;
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 0 12px;
  font-size: 14px;
  color: var(--muted);
}
.team-member-trigger > span {
  flex: 1;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.team-member-menu {
  position: absolute;
  z-index: 5;
  left: 0;
  right: 0;
  top: 100%;
  padding: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 7px;
  box-shadow: 0 6px 20px #0002;
  max-height: 230px;
  overflow: auto;
}
.team-member-menu label {
  flex-direction: row;
  align-items: center;
  gap: 8px;
  padding: 8px;
}
.team-member-menu input[type='checkbox'] {
  width: 14px;
  height: 14px;
}
.team-member-menu p {
  padding: 12px;
  font-size: 12px;
  color: var(--muted);
}
.team-member-menu > small {
  display: block;
  padding: 8px;
  font-size: 11px;
}
.editor-team footer {
  min-height: 58px;
}
.editor-team footer > .button:not(.primary),
.editor-skill footer > .button:not(.primary) {
  background: transparent;
  border-color: transparent;
}
.editor-team footer > .button {
  font-size: 14px;
  font-weight: 400;
  height: 30px;
}
.editor-skill header {
  padding: 16px 20px;
  min-height: 70px;
  height: 70px;
  align-items: flex-start;
  gap: 12px;
}
.editor-skill header .icon-button {
  width: 14px;
  height: 20px;
  padding: 0;
}
.editor-skill header > span {
  line-height: 20px;
}
.editor-skill header small {
  margin-top: 2px;
}
.editor-skill .editor-body {
  gap: 16px;
  padding: 16px 20px;
}
.editor-skill label {
  font-size: 12px;
  gap: 6px;
}
.editor-skill input {
  height: 32px;
  font-size: 14px;
}
.editor-skill textarea {
  resize: none;
  font-size: 14px;
  line-height: 20px;
}
.skill-description-label {
  display: flex;
  align-items: center;
  gap: 8px;
}
.editor-skill footer .button {
  font-size: 14px;
  font-weight: 400;
}
.editor-skill footer {
  min-height: 54px;
}
.editor-connector > form > header .icon-button {
  width: 24px;
  height: 24px;
}
.editor-connector .transport-options > button {
  padding-left: 21px;
  gap: 17px;
}
.editor-connector .transport-options small {
  font-size: 12px;
}
.resource-editor input:focus-visible {
  outline: 2px solid #4c87af;
  box-shadow: 0 0 0 4px #4c87af50;
  outline-offset: 0;
}
.editor-autopilot {
  width: 1026px;
}
.editor-autopilot form > header {
  height: 48px;
  min-height: 48px;
  padding: 10px 20px;
  gap: 8px;
  flex: none;
  font-size: 12px;
}
.editor-autopilot header .icon-button {
  height: 24px;
  width: 24px;
}
.autopilot-header-icon {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  background: #d1dfeb;
  color: var(--primary);
  border-radius: 4px;
}
.autopilot-header-muted {
  color: var(--muted);
}
.autopilot-editor {
  grid-template-columns: minmax(0, 644px) minmax(0, 1fr);
  height: min(620px, 65vh);
}
.autopilot-runbook {
  padding: 20px 24px 24px;
  gap: 12px;
}
.autopilot-runbook > input {
  font-size: 24px;
  line-height: 29px;
  height: 29px;
  font-weight: 600;
  outline: 0 !important;
  box-shadow: none !important;
  border: 0 !important;
  border-radius: 0;
}
.autopilot-runbook > input::placeholder {
  color: var(--muted);
  opacity: 1;
}
.autopilot-runbook > label {
  line-height: 16px;
  gap: 8px;
  margin-bottom: -3px;
}
.autopilot-runbook textarea {
  font-size: 14px;
  line-height: 23px;
  padding: 12px 16px;
  border-radius: 8px;
}
.autopilot-editor aside {
  padding: 20px 30px 24px 20px;
  gap: 8px;
  scrollbar-width: thin;
}
.autopilot-editor aside > label {
  font-size: 11px;
  line-height: 16px;
  letter-spacing: 0.8px;
  margin-top: 12px;
  gap: 8px;
}
.autopilot-editor aside > label:first-child {
  margin-top: 0;
}
.required-mark {
  color: #d43b50;
  margin-left: 2px;
}
.autopilot-assignee,
.autopilot-project-select {
  position: relative;
  display: flex;
  align-items: center;
  background: white;
  border-radius: 7px;
  height: 44px;
  flex: none;
}
.autopilot-assignee > svg:first-child {
  position: absolute;
  left: 14px;
  color: var(--muted);
  background: var(--input);
  border-radius: 50%;
  padding: 6px;
  width: 28px;
  height: 28px;
  pointer-events: none;
}
.autopilot-assignee > select {
  appearance: none;
  height: 44px;
  padding: 8px 36px 8px 50px;
  min-width: 0;
  border: 0;
}
.autopilot-assignee > .icon:last-child,
.autopilot-project-select > .icon:last-child {
  position: absolute;
  right: 12px;
  pointer-events: none;
}
.output-mode {
  height: 54px;
  min-height: 54px;
  flex: none;
  padding: 9px 12px;
  gap: 10px;
}
.output-mode + .output-mode {
  margin-top: -2px;
}
.output-mode small {
  margin-top: 0;
  font-size: 12px !important;
  line-height: 18px;
}
.output-radio {
  margin-top: 1px;
  width: 16px;
  height: 16px;
  border: 1px solid #c6c8ca;
  border-radius: 50%;
  display: grid;
  place-items: center;
  flex: none;
}
.output-radio.selected {
  background: var(--primary);
  color: white;
  border-color: var(--primary);
}
.autopilot-project-select {
  align-self: flex-start;
  height: 38px;
  max-width: 100%;
  width: max-content;
}
.autopilot-project-select > .icon:first-child {
  position: absolute;
  left: 12px;
  pointer-events: none;
}
.autopilot-project-select > select {
  appearance: none;
  height: 38px;
  padding: 7px 34px 7px 42px;
  width: auto;
  max-width: 100%;
  border: 0;
}
.autopilot-editor aside small {
  line-height: 15px;
}
.subscriber-button {
  padding: 3px 8px;
  border: 1px dashed #ffffff30;
  border-radius: 4px;
}
.trigger-tabs {
  padding: 3px;
  gap: 4px;
}
.trigger-tabs > button {
  height: 32px;
  font-size: 14px;
}
.schedule-fields {
  gap: 12px;
}
.schedule-fields .segmented {
  padding: 3px;
}
.schedule-fields > label {
  gap: 7px;
  line-height: 18px;
}
.schedule-fields .segmented > button {
  height: 24px;
}
.editor-autopilot footer {
  height: 52px;
  min-height: 52px;
  padding: 12px 20px;
}
.editor-autopilot footer .button {
  font-size: 14px;
  font-weight: 400;
  height: 28px;
}
.footer-note {
  display: flex;
  align-items: center;
  gap: 6px;
}
.footer-note > .icon {
  color: #dc9d24;
}
.autopilot-expanded {
  width: min(1400px, calc(100vw - 64px));
}
.autopilot-expanded .autopilot-editor {
  grid-template-columns: minmax(0, 1fr) 380px;
  height: calc(85vh - 100px);
}
.autopilot-editor aside > .trigger-tabs {
  margin-bottom: 0;
}
.autopilot-editor aside > .trigger-tabs + label {
  margin-top: 4px;
}
.interval-value:focus-within {
  outline: 2px solid #4c87af;
  box-shadow: 0 0 0 4px #4c87af30;
}
.interval-value input:focus-visible {
  outline: 0;
  box-shadow: none;
}
</style>
