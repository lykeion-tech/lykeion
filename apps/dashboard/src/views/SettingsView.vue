<script setup lang="ts">
import { themes } from '../theme';
import { computed, reactive, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { state, service, apiUrl, connect, refresh, notify } from '../services/workspace';
import Icon from '../components/Icon.vue';
import TokenCreationForm from '../components/settings/TokenCreationForm.vue';
import CompactSelect from '../components/settings/CompactSelect.vue';
import Modal from '../components/Modal.vue';
import ColorPicker from '../components/settings/ColorPicker.vue';
import { readJSON } from '../storage';
import { defaultCreateFields, usePreferences } from '../state/preferences';
import { useProfile } from '../state/profile';
import { useSettingsDrafts, type LabelDraft, type PropertyDraft } from '../state/settings-drafts';
import { failedSettingsSaves, saveSetting } from '../state/settings-saves';
import TimeZonePicker from '../components/settings/TimeZonePicker.vue';
import { colorPresets } from '../lib/color';
import { matchesQuery } from '../lib/search';
import {
  copyCreateFields as fieldsCopy,
  createFieldAvailable as fieldAvailable,
  labelNameTaken,
  propertyOptions,
  repositoryUrl,
  validCreateFields as validFields,
} from '../lib/settings';
const router = useRouter();
const props = defineProps<{ section: string }>();
const section = computed(() => props.section);
const query = ref('');
const error = ref('');
const busy = ref(false);
const name = ref(state.data?.workspace.name ?? 'Lykeion');
const context = ref(state.data?.workspace.researchContext ?? '');
const prefix = ref(state.data?.workspace.taskPrefix ?? 'LYK');
const endpoint = ref(apiUrl.value);
const profile = useProfile();
const careerStage = ref(profile.careerStage);
const savedExtras = readJSON<unknown>('lykeion.workspace-appearance', null);
const workspaceExtras = reactive<Record<string, { description: string; logo: string }>>(
  savedExtras && typeof savedExtras === 'object' ? { ...savedExtras } : {},
);
const workspaceKey = computed(() => state.data?.workspace.id || 'local');
const extras = computed(() => workspaceExtras[workspaceKey.value]!);
watch(workspaceExtras, (value) => saveSetting('lykeion.workspace-appearance', value), {
  deep: true,
});
// Seed the current workspace's entry before anything reads it, so the computed stays pure.
watch(
  workspaceKey,
  (id) => {
    workspaceExtras[id] ||= { description: '', logo: '' };
  },
  { immediate: true, flush: 'sync' },
);
const prefixEditing = ref(false);
const logoInput = ref<HTMLInputElement>();
const createFieldNames = [
  'Status',
  'Priority',
  'Assignee',
  'Labels',
  'Project',
  'Due date',
  'Start date',
];
const prefs = usePreferences();
let fieldWorkspace = state.data?.workspace.id;
if (fieldWorkspace && !prefs.createFieldsByWorkspace[fieldWorkspace])
  prefs.createFieldsByWorkspace[fieldWorkspace] = fieldsCopy(prefs.createFields);
watch(
  () => state.data?.workspace.id,
  (id) => {
    if (!id || id === fieldWorkspace) return;
    if (fieldWorkspace)
      prefs.createFieldsByWorkspace[fieldWorkspace] = fieldsCopy(prefs.createFields);
    const next = prefs.createFieldsByWorkspace[id];
    prefs.createFields = fieldsCopy(validFields(next) ? next : defaultCreateFields);
    fieldWorkspace = id;
  },
);
function toggleField(field: string, mode: 'agent' | 'manual', event: Event) {
  const enabled = (event.target as HTMLInputElement).checked;
  prefs.createFields[mode] = enabled
    ? [...new Set([...prefs.createFields[mode], field])]
    : prefs.createFields[mode].filter((item) => item !== field);
  if (fieldWorkspace)
    prefs.createFieldsByWorkspace[fieldWorkspace] = fieldsCopy(prefs.createFields);
}
type WorkspaceDraft = { name: string; researchContext: string; taskPrefix: string };
const workspaceDrafts = new Map<string, WorkspaceDraft>();
let loadedWorkspaceId = state.data?.workspace.id;
let baseline: WorkspaceDraft = {
  name: name.value,
  researchContext: context.value,
  taskPrefix: prefix.value,
};
function draft(): WorkspaceDraft {
  return { name: name.value, researchContext: context.value, taskPrefix: prefix.value };
}
function hydrate(value: WorkspaceDraft) {
  name.value = value.name;
  context.value = value.researchContext;
  prefix.value = value.taskPrefix;
}
watch(
  () => state.data?.workspace,
  (workspace) => {
    if (!workspace) return;
    const changedId = loadedWorkspaceId !== workspace.id;
    const dirty = JSON.stringify(draft()) !== JSON.stringify(baseline);
    if (changedId && loadedWorkspaceId && dirty) workspaceDrafts.set(loadedWorkspaceId, draft());
    if (changedId || !dirty) hydrate(workspaceDrafts.get(workspace.id) || workspace);
    loadedWorkspaceId = workspace.id;
    baseline = {
      name: workspace.name,
      researchContext: workspace.researchContext,
      taskPrefix: workspace.taskPrefix,
    };
  },
  { deep: true },
);
watch(apiUrl, (value, old) => {
  if (endpoint.value === old) endpoint.value = value;
});
const localSettings = useSettingsDrafts();
const settingsDraft = computed(() => localSettings[workspaceKey.value]!);
function seedSettingsDraft(id: string) {
  localSettings[id] ||= {
    labels: [],
    properties: [],
    notifications: {
      assignments: true,
      status: true,
      comments: true,
      mentions: true,
      priority: true,
      agents: true,
      system: false,
    },
    repositories: [],
    code: { sidebar: true, autoLink: false, coAuthor: false },
  };
}
watch(workspaceKey, seedSettingsDraft, { immediate: true, flush: 'sync' });
const labelScope = ref<'Tasks' | 'Skills'>('Tasks');
const tableFilter = ref('');
const showArchived = ref(false);
const filteredLabels = computed(() =>
  settingsDraft.value.labels.filter(
    (item) =>
      item.scope === labelScope.value &&
      matchesQuery(item.name + ' ' + item.description, tableFilter.value),
  ),
);
const filteredProperties = computed(() =>
  settingsDraft.value.properties.filter(
    (item) => (showArchived.value || !item.archived) && matchesQuery(item.name, tableFilter.value),
  ),
);
const editor = ref<'label' | 'property' | 'repository' | null>(null);
const editingId = ref('');
const draftName = ref('');
const draftDescription = ref('');
const draftColor = ref('#3b82f6');
const draftIcon = ref('filters');
const draftType = ref('Select');
const draftOptions = ref('');
const draftOptionColors = ref<string[]>([]);
const draftUrl = ref('');
const editorError = ref('');
function openEditor(kind: 'label' | 'property' | 'repository', id = '') {
  editor.value = kind;
  editingId.value = id;
  editorError.value = '';
  const label = settingsDraft.value.labels.find((item) => item.id === id);
  const property = settingsDraft.value.properties.find((item) => item.id === id);
  draftName.value = label?.name || property?.name || '';
  draftDescription.value = label?.description || property?.description || '';
  draftColor.value = label?.color || '#3b82f6';
  draftIcon.value = property?.icon || 'filters';
  draftType.value = property?.type || 'Select';
  draftOptions.value = property?.options.join('\n') || '';
  draftOptionColors.value = property?.optionColors ? [...property.optionColors] : ['#3b82f6'];
  draftUrl.value = '';
}
function saveEditor() {
  editorError.value = '';
  if (editor.value === 'repository') {
    const url = repositoryUrl(draftUrl.value);
    if (!url) {
      editorError.value =
        'Enter an HTTPS repository URL without credentials, a query, or a fragment.';
      return;
    }
    if (settingsDraft.value.repositories.some((item) => item.url === url)) {
      editorError.value = 'This repository is already listed.';
      return;
    }
    settingsDraft.value.repositories.push({ id: crypto.randomUUID(), url });
  } else {
    if (!draftName.value.trim()) {
      editorError.value = 'Enter a name.';
      return;
    }
    const updatedAt = new Date().toISOString();
    if (editor.value === 'label') {
      if (
        labelNameTaken(
          settingsDraft.value.labels,
          draftName.value,
          labelScope.value,
          editingId.value,
        )
      ) {
        editorError.value = 'A label with this name already exists.';
        return;
      }
      const value: LabelDraft = {
        id: editingId.value || crypto.randomUUID(),
        name: draftName.value.trim(),
        description: draftDescription.value,
        color: draftColor.value,
        scope: labelScope.value,
        updatedAt,
      };
      const index = settingsDraft.value.labels.findIndex((item) => item.id === value.id);
      if (index < 0) settingsDraft.value.labels.push(value);
      else settingsDraft.value.labels[index] = value;
    } else {
      if (
        !editingId.value &&
        settingsDraft.value.properties.filter((item) => !item.archived).length >= 20
      ) {
        editorError.value = 'Archive a property before adding another.';
        return;
      }
      const value: PropertyDraft = {
        id: editingId.value || crypto.randomUUID(),
        name: draftName.value.trim(),
        description: draftDescription.value,
        icon: draftIcon.value,
        type: draftType.value,
        options: propertyOptions(draftType.value, draftOptions.value),
        optionColors: [...draftOptionColors.value],
        archived: false,
        updatedAt,
      };
      if (['Select', 'Multi-select'].includes(value.type) && !value.options.length) {
        editorError.value = 'Add at least one option.';
        return;
      }
      const index = settingsDraft.value.properties.findIndex((item) => item.id === value.id);
      if (index < 0) settingsDraft.value.properties.push(value);
      else settingsDraft.value.properties[index] = value;
    }
  }
  editor.value = null;
  notify('Saved on this device.');
}
const propertyTypes = [
  'Text',
  'Number',
  'Select',
  'Multi-select',
  'Date',
  'Checkbox',
  'URL',
  'Member',
  'Members',
];
const optionRows = computed(() => draftOptions.value.split('\n'));
function updateOption(index: number, event: Event) {
  const options = [...optionRows.value];
  options[index] = (event.target as HTMLInputElement).value;
  draftOptions.value = options.join('\n');
}
function removeOption(index: number) {
  const options = [...optionRows.value];
  if (options.length > 1) {
    options.splice(index, 1);
    draftOptionColors.value.splice(index, 1);
    draftOptions.value = options.join('\n');
  }
}
const notificationRows = [
  {
    id: 'assignments',
    title: 'Assignments',
    description: 'When you are assigned or unassigned from a task',
  },
  {
    id: 'status',
    title: 'Status changes',
    description: 'When a task you follow changes scientific stage or status',
  },
  { id: 'comments', title: 'Comments', description: 'New comments on tasks you follow' },
  {
    id: 'mentions',
    title: 'Mentions',
    description: 'When someone @mentions you, including @all and @team',
  },
  {
    id: 'priority',
    title: 'Priority & Due date',
    description: 'When priority or due date changes on tasks you follow',
  },
  { id: 'agents', title: 'Agent activity', description: 'When an agent run fails' },
];

const analyticsTab = ref('Usage');
const analyticsPeriod = ref('30');
const analyticsProject = ref('');
const analyticsMetric = ref('Tokens');
const analyticsDimension = ref('Daily');
const leaderboardMetric = ref('Tokens');
const avatarInput = ref<HTMLInputElement>();
const groups = computed(() =>
  [
    {
      title: 'Personal',
      suffix: profile.name || 'This device',
      items: [
        { id: 'profile', label: 'Profile', icon: 'users' },
        { id: 'preferences', label: 'Preferences', icon: 'filters' },
        { id: 'notifications', label: 'Notifications', icon: 'bell' },
        { id: 'analytics', label: 'Analytics', icon: 'chart' },
        { id: 'tokens', label: 'API Tokens', icon: 'link' },
      ],
    },
    {
      title: state.data?.workspace.name || 'Lykeion',
      suffix: 'Workspace',
      items: [
        { id: 'workspace', label: 'General', icon: 'settings' },
        { id: 'labels', label: 'Labels', icon: 'tag' },
        { id: 'properties', label: 'Properties', icon: 'list' },
      ],
    },
    {
      title: 'Connections & extensions',
      suffix: '',
      items: [
        { id: 'skills', label: 'Skills', icon: 'book' },
        { id: 'mcp', label: 'Connectors', icon: 'link' },
        { id: 'code', label: 'Code', icon: 'folder' },
        { id: 'connection', label: 'API connection', icon: 'network' },
      ],
    },
  ]
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => matchesQuery(item.label, query.value)),
    }))
    .filter((group) => group.items.length),
);
const sectionLabels: Record<string, string> = {
  profile: 'Profile',
  preferences: 'Preferences',
  appearance: 'Preferences',
  notifications: 'Notifications',
  analytics: 'Analytics',
  tokens: 'API Tokens',
  workspace: 'General',
  general: 'General',
  labels: 'Labels',
  properties: 'Properties',
  skills: 'Skills',
  mcp: 'Connectors',
  code: 'Code',
  connection: 'API connection',
};
const title = computed(() => sectionLabels[section.value] || 'Settings');
watch(failedSettingsSaves, () => {
  error.value = 'Your browser could not save these settings. Free storage and try again.';
});
async function avatarChanged(event: Event, workspaceLogo = false) {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 1024 * 1024) {
    error.value = 'Choose a PNG, JPEG or WebP smaller than 1 MB.';
    return;
  }
  const reader = new FileReader();
  reader.onload = () => {
    if (workspaceLogo) extras.value.logo = String(reader.result);
    else profile.avatar = String(reader.result);
  };
  reader.readAsDataURL(file);
}
async function save() {
  busy.value = true;
  error.value = '';
  try {
    await service.value.updateWorkspace({
      name: name.value,
      researchContext: context.value,
      taskPrefix: prefix.value,
    });
    await refresh();
    baseline = draft();
    if (loadedWorkspaceId) workspaceDrafts.delete(loadedWorkspaceId);
    notify('Workspace settings saved.');
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function applyConnection() {
  busy.value = true;
  error.value = '';
  try {
    await connect(endpoint.value);
    notify(endpoint.value ? 'Control plane connected.' : 'Local workspace opened.');
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function copySlug() {
  try {
    await navigator.clipboard.writeText(state.data?.workspace.slug || '');
    notify('Workspace slug copied.');
  } catch {
    error.value = 'Could not access your clipboard.';
  }
}
function open(id: string) {
  error.value = '';
  tableFilter.value = '';
  router.push({ path: '/settings', query: { tab: id } });
}
</script>
<template>
  <div class="desktop-settings">
    <nav class="settings-rail" aria-label="Settings">
      <h1>Settings</h1>
      <label class="settings-search"
        ><Icon name="search" :size="16" /><input
          v-model="query"
          type="search"
          placeholder="Search settings"
          aria-label="Search settings"
      /></label>
      <section
        v-for="(group, index) in groups"
        :key="group.title"
        :class="{ 'extension-group': index === 2 }"
      >
        <header>
          <span v-if="index < 2" class="tiny-avatar">{{ group.title.charAt(0) }}</span
          ><strong>{{ group.title }}</strong
          ><small>{{ group.suffix }}</small>
        </header>
        <button
          v-for="item in group.items"
          :key="item.id"
          :class="{ selected: section === item.id }"
          :aria-current="section === item.id ? 'page' : undefined"
          @click="open(item.id)"
        >
          <Icon :name="item.icon" />{{ item.label }}
        </button>
      </section>
      <p v-if="!groups.length" class="no-match">No matching settings.</p>
    </nav>
    <div class="settings-scroll">
      <section
        class="settings-body"
        :class="{
          wide: ['preferences', 'appearance', 'labels', 'properties'].includes(section),
          'analytics-layout': section === 'analytics',
          'table-layout': ['labels', 'properties', 'tokens'].includes(section),
          'notifications-layout': section === 'notifications',
          'code-layout': section === 'code',
        }"
      >
        <header class="settings-heading">
          <h2>{{ title }}</h2>
          <span v-if="section === 'profile'" class="scope-badge"
            ><Icon name="monitor" :size="11" />This device only</span
          ><span
            v-else-if="
              ['workspace', 'general', 'labels', 'properties', 'code', 'notifications'].includes(
                section,
              )
            "
            class="scope-badge"
            ><Icon name="users" :size="11" />{{ state.data?.workspace.name || 'Lykeion' }} · This
            device</span
          ><span v-if="section === 'analytics'" class="analytics-status"
            >No analytics service connected
            <button
              class="icon-button"
              aria-label="Refresh"
              @click="notify('Analytics require a connected control plane with usage reporting.')"
            >
              <Icon name="refresh" /></button
          ></span>
        </header>
        <template v-if="section === 'profile'">
          <h3>Personal information</h3>
          <div class="setting-box">
            <div class="setting-row avatar-row">
              <div>Avatar<small>Click to upload avatar</small></div>
              <input
                ref="avatarInput"
                hidden
                type="file"
                accept="image/png,image/jpeg,image/webp"
                @change="avatarChanged"
              /><button
                class="profile-avatar"
                aria-label="Change avatar"
                @click="avatarInput?.click()"
              >
                <img v-if="profile.avatar" :src="profile.avatar" alt="Your avatar" /><template
                  v-else
                  >{{ profile.name.charAt(0).toUpperCase() || 'L' }}</template
                >
              </button>
            </div>
            <label class="setting-row"
              ><span>Name</span
              ><input
                v-model="profile.name"
                aria-label="Name"
                placeholder="Your name"
                maxlength="120"
            /></label>
            <label class="setting-row about-row"
              ><span>About you<small>Saved on this device for your research profile.</small></span>
              <div class="about-control">
                <textarea
                  v-model="profile.about"
                  aria-label="About you"
                  maxlength="2000"
                  placeholder="e.g. Backend engineer (Go + Postgres). Prefer terse PRs and tests alongside the change."
                /><small>{{ profile.about.length }}/2000</small>
              </div></label
            >
          </div>
        </template>
        <template v-else-if="section === 'preferences' || section === 'appearance'">
          <h3>Language & region <span class="scope-badge">This device only</span></h3>
          <div class="setting-box">
            <label class="setting-row"
              ><span>Language</span
              ><select aria-label="Language">
                <option>English</option>
              </select></label
            ><label class="setting-row"
              ><span>Time zone<small>Used for dates and charts on this device.</small></span
              ><TimeZonePicker v-model="prefs.timeZone" v-model:automatic="prefs.timeZoneAutomatic"
            /></label>
          </div>
          <h3>
            Appearance
            <span class="scope-badge"><Icon name="monitor" :size="11" />This device only</span>
          </h3>
          <div class="theme-grid">
            <button
              v-for="theme in themes"
              :key="theme.id"
              class="theme-card"
              :class="{ chosen: prefs.theme === theme.id }"
              :aria-pressed="prefs.theme === theme.id"
              :aria-label="theme.name"
              :style="{
                '--swatch-bg': theme.surface,
                '--swatch-input': theme.input,
                '--swatch-accent': theme.accent,
                '--swatch-text': theme.text,
              }"
              @click="prefs.theme = theme.id"
            >
              <div class="theme-preview">
                <div><i></i><i></i><span>Primary</span><b></b></div>
              </div>
              <footer>
                {{ theme.name }}<small>{{ theme.description }}</small
                ><Icon v-if="prefs.theme === theme.id" name="check" :size="14" />
              </footer>
            </button>
          </div>
          <h3>
            Comments & chat
            <span class="scope-badge"><Icon name="monitor" :size="11" />This device only</span>
          </h3>
          <div class="setting-box">
            <label class="setting-row"
              ><span>Pin comment bar to bottom</span
              ><input
                v-model="prefs.pinComments"
                class="setting-switch"
                type="checkbox"
                role="switch"
                aria-label="Pin comment bar to bottom" /></label
            ><label class="setting-row"
              ><span
                >When replying to a running agent<small
                  >Sets the default only. You can still change it for any message before
                  sending.</small
                ></span
              ><CompactSelect
                v-model="prefs.replyMode"
                class="reply-mode-select"
                label="When replying to a running agent"
                :width="192"
                :options="[
                  { value: 'current', label: 'Add to current run' },
                  { value: 'after', label: 'Start after this run' },
                ]"
            /></label>
          </div>
          <h3>
            Opening tasks
            <span class="scope-badge"><Icon name="monitor" :size="11" />This device only</span>
          </h3>
          <div class="setting-box">
            <div class="setting-row">
              <span
                >Clicking a card or row opens<small>Shift+Click opens the other one.</small></span
              >
              <div class="segmented">
                <button :aria-pressed="prefs.taskOpen === 'page'" @click="prefs.taskOpen = 'page'">
                  Full page</button
                ><button
                  :aria-pressed="prefs.taskOpen === 'preview'"
                  @click="prefs.taskOpen = 'preview'"
                >
                  Side preview
                </button>
              </div>
            </div>
          </div>
          <h3>
            Fields shown when creating tasks
            <span class="scope-badge"
              ><Icon name="monitor" :size="11" />This device ·
              {{ state.data?.workspace.name || 'Lykeion' }}</span
            >
          </h3>
          <p class="fields-description">
            Choose which fields each create dialog keeps on its toolbar. Hidden fields stay
            available from the ⋯ menu and reappear while they hold a value.
          </p>
          <div class="setting-box">
            <table class="field-table">
              <thead>
                <tr>
                  <th>Field</th>
                  <th>Create with agent</th>
                  <th>Manual create</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="field in createFieldNames" :key="field">
                  <th scope="row">{{ field }}</th>
                  <td v-for="mode in ['agent', 'manual'] as const" :key="mode">
                    <input
                      v-if="fieldAvailable(field, mode)"
                      type="checkbox"
                      :aria-label="
                        field + ' · ' + (mode === 'agent' ? 'Create with agent' : 'Manual create')
                      "
                      :checked="prefs.createFields[mode].includes(field)"
                      @change="toggleField(field, mode, $event)"
                    /><span v-else aria-label="Not available when creating with an agent">—</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
        <template v-else-if="section === 'workspace' || section === 'general'">
          <h3>Your career stage</h3>
          <div class="setting-box career-box">
            <label
              >Your career stage<select v-model="careerStage" aria-label="Your career stage">
                <option value="">Choose a stage (optional)</option>
                <option>Undergraduate student</option>
                <option>Graduate student</option>
                <option>Doctoral researcher</option>
                <option>Postdoctoral researcher</option>
                <option>Faculty</option>
                <option>Industry researcher</option>
                <option>Independent researcher</option>
                <option>Other</option>
              </select></label
            ><button
              class="button primary"
              @click="
                profile.careerStage = careerStage;
                notify('Career stage saved on this device.');
              "
            >
              Save career stage
            </button>
          </div>
          <h3 class="subsequent-heading">Workspace details</h3>
          <form @submit.prevent="save">
            <div class="setting-box">
              <div class="setting-row avatar-row">
                <div>
                  Workspace logo<small>Click the logo to upload a new image · This device</small>
                </div>
                <input
                  ref="logoInput"
                  hidden
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  @change="avatarChanged($event, true)"
                /><button
                  type="button"
                  class="profile-avatar"
                  aria-label="Change workspace logo"
                  @click="logoInput?.click()"
                >
                  <img v-if="extras.logo" :src="extras.logo" alt="Workspace logo" /><template
                    v-else
                    >{{ name.charAt(0).toUpperCase() || 'L' }}</template
                  >
                </button>
              </div>
              <label class="setting-row"
                ><span>Name</span
                ><input v-model="name" aria-label="Workspace name" required /></label
              ><label class="setting-row about-row"
                ><span>Description<small>This device only</small></span
                ><textarea
                  v-model="extras.description"
                  aria-label="Workspace description"
                  placeholder="What does this workspace focus on?"
                /></label
              ><label class="setting-row about-row"
                ><span
                  >Research context<small
                    >Shared research questions, methods, and context for this workspace.</small
                  ></span
                ><textarea
                  v-model="context"
                  aria-label="Research context"
                  placeholder="Background information and context for AI agents working in this workspace"
                />
              </label>
              <div class="setting-row">
                <span>Slug<small>Part of every workspace URL. It can't be changed.</small></span>
                <div class="inline-setting">
                  <code>{{ state.data?.workspace.slug }}</code
                  ><button
                    type="button"
                    class="icon-button"
                    aria-label="Copy slug"
                    @click="copySlug"
                  >
                    <Icon name="copy" :size="14" />
                  </button>
                </div>
              </div>
              <div class="setting-row">
                <span
                  >Task prefix<small>Used in task numbers like {{ prefix }}-123.</small></span
                >
                <div class="inline-setting">
                  <input
                    v-if="prefixEditing"
                    v-model="prefix"
                    aria-label="Task prefix"
                    required
                    maxlength="12"
                  /><code v-else>{{ prefix }}</code
                  ><button
                    type="button"
                    class="button compact"
                    @click="prefixEditing = !prefixEditing"
                  >
                    {{ prefixEditing ? 'Done' : 'Change prefix…' }}
                  </button>
                </div>
              </div>
            </div>
            <button
              v-if="
                name !== state.data?.workspace.name ||
                context !== state.data?.workspace.researchContext ||
                prefix !== state.data?.workspace.taskPrefix
              "
              class="button primary save-button"
              :disabled="busy"
            >
              {{ busy ? 'Saving…' : 'Save changes' }}
            </button>
          </form>
          <h3 class="subsequent-heading">Danger Zone</h3>
          <div class="setting-box">
            <div class="setting-row">
              <span
                >Leave workspace<small
                  >Membership changes require a connected control plane.</small
                ></span
              ><button class="button" disabled>Leave workspace</button>
            </div>
            <div class="setting-row">
              <span
                >Delete workspace<small
                  >Workspace deletion is unavailable in this frontend.</small
                ></span
              ><button class="button danger-button" disabled>Delete workspace</button>
            </div>
          </div>
        </template>
        <template v-else-if="section === 'tokens'">
          <p class="table-description">Authenticate the CLI and API.</p>
          <div class="tokens-body">
            <p class="token-note">
              API tokens are credentials. Token management requires your connected control plane.
            </p>
            <details class="token-usage">
              <summary>Usage</summary>
              <p>
                Tokens are issued and revoked by the account service. This frontend never generates
                or stores token values.
              </p>
            </details>
            <TokenCreationForm />
            <div class="setting-box tokens-empty">
              <Icon name="link" :size="22" /><span
                >Token creation requires a connected identity API.</span
              ><button class="text-button" @click="open('connection')">Configure connection</button>
            </div>
          </div>
        </template>
        <template v-else-if="section === 'notifications'">
          <h3>Inbox Notifications</h3>
          <p class="section-description">
            Muted events remain visible on the task. These preferences are saved locally; delivery
            requires a connected control plane.
          </p>
          <div class="setting-box notification-box">
            <label v-for="item in notificationRows" :key="item.id" class="setting-row"
              ><span
                >{{ item.title }}<small>{{ item.description }}</small></span
              ><input
                v-model="settingsDraft.notifications[item.id]"
                type="checkbox"
                class="setting-switch"
                role="switch"
                :aria-label="item.title"
            /></label>
          </div>
          <h3 class="subsequent-heading">System Notifications</h3>
          <p class="section-description">
            Show system notifications when Lykeion is in the background.
          </p>
          <div class="setting-box">
            <label class="setting-row"
              ><span
                >Show system notifications<small
                  >System notification delivery is not connected.</small
                ></span
              ><input
                type="checkbox"
                class="setting-switch"
                role="switch"
                aria-label="Show system notifications"
                disabled
            /></label>
          </div>
        </template>
        <template v-else-if="section === 'labels'">
          <p class="table-description">Task labels and skill labels are separate.</p>
          <div class="segmented label-scope" role="tablist" aria-label="Labels">
            <button
              v-for="scope in ['Tasks', 'Skills'] as const"
              :key="scope"
              role="tab"
              :aria-selected="labelScope === scope"
              :aria-pressed="labelScope === scope"
              @click="labelScope = scope"
            >
              {{ scope }}
              <small>{{
                settingsDraft.labels.filter((item) => item.scope === scope).length
              }}</small>
            </button>
          </div>
          <div class="table-toolbar">
            <label class="table-search"
              ><Icon name="search" /><input
                v-model="tableFilter"
                placeholder="Filter by name or description..."
                aria-label="Filter labels" /></label
            ><button class="button primary" @click="openEditor('label')">
              <Icon name="plus" />New label
            </button>
          </div>
          <div class="settings-data-table">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Description</th>
                  <th>Used by</th>
                  <th>Updated</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="label in filteredLabels" :key="label.id">
                  <td>
                    <button @click="openEditor('label', label.id)">
                      <i class="label-color" :style="{ background: label.color }" />{{ label.name }}
                    </button>
                  </td>
                  <td>{{ label.description }}</td>
                  <td>—</td>
                  <td>{{ new Date(label.updatedAt).toLocaleDateString() }}</td>
                </tr>
              </tbody>
            </table>
            <div v-if="!filteredLabels.length" class="table-empty">
              <Icon name="tag" :size="24" />
              <p>{{ tableFilter ? 'No matching labels' : `No ${labelScope} labels yet` }}</p>
            </div>
          </div>
        </template>
        <template v-else-if="section === 'properties'">
          <p class="table-description">Add custom fields to tasks.</p>
          <div class="table-toolbar properties-toolbar">
            <label class="table-search"
              ><Icon name="search" /><input
                v-model="tableFilter"
                placeholder="Filter by name..."
                aria-label="Filter properties" /></label
            ><label class="archived-control"
              ><input
                v-model="showArchived"
                class="setting-switch"
                type="checkbox"
                role="switch"
              />Show archived</label
            ><small
              >{{ settingsDraft.properties.filter((item) => !item.archived).length }}/20 active
              properties</small
            ><button
              class="button primary"
              :disabled="settingsDraft.properties.filter((item) => !item.archived).length >= 20"
              @click="openEditor('property')"
            >
              <Icon name="plus" />New property
            </button>
          </div>
          <div class="settings-data-table">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Options</th>
                  <th>Used by</th>
                  <th>Updated</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="property in filteredProperties" :key="property.id">
                  <td>
                    <button @click="openEditor('property', property.id)">{{ property.name }}</button
                    ><small v-if="property.archived"> · Archived</small>
                  </td>
                  <td>{{ property.type }}</td>
                  <td>{{ property.options.join(', ') || '—' }}</td>
                  <td>—</td>
                  <td>
                    {{ new Date(property.updatedAt).toLocaleDateString() }}
                    <button class="text-button" @click="property.archived = !property.archived">
                      {{ property.archived ? 'Restore' : 'Archive' }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
            <div v-if="!filteredProperties.length" class="table-empty property-empty">
              <Icon name="filters" :size="24" />
              <p>{{ tableFilter ? 'No matching properties' : 'No properties yet' }}</p>
              <small>For example, Severity or Environment.</small>
            </div>
          </div>
        </template>
        <template v-else-if="section === 'analytics'">
          <div class="analytics-toolbar">
            <div class="analytics-tabs" role="tablist">
              <button
                v-for="tab in ['Usage', 'Errors']"
                :key="tab"
                role="tab"
                :aria-selected="analyticsTab === tab"
                @click="analyticsTab = tab"
              >
                {{ tab }}
              </button>
            </div>
            <select v-model="analyticsPeriod" aria-label="Period">
              <option value="7">7d</option>
              <option value="30">30d</option>
              <option value="90">90d</option></select
            ><select v-model="analyticsProject" aria-label="Project">
              <option value="">All projects</option>
              <option v-for="project in state.data?.projects" :key="project.id" :value="project.id">
                {{ project.name }}
              </option>
            </select>
          </div>
          <div class="analytics-content">
            <template v-if="analyticsTab === 'Usage'"
              ><div class="usage-metrics">
                <div v-for="metric in ['Cost', 'Tokens', 'Run time', 'Runs']" :key="metric">
                  <small>{{ metric.toUpperCase() }} · {{ analyticsPeriod }}D</small
                  ><strong>—</strong><small>No reported data</small>
                </div>
              </div>
              <section class="analytics-panel">
                <header>
                  <h3>{{ analyticsDimension }} {{ analyticsMetric.toLowerCase() }}</h3>
                  <div class="segmented">
                    <button
                      v-for="metric in ['Tokens', 'Cost', 'Time', 'Runs']"
                      :key="metric"
                      :aria-pressed="analyticsMetric === metric"
                      @click="analyticsMetric = metric"
                    >
                      {{ metric }}
                    </button>
                  </div>
                  <div class="segmented">
                    <button
                      v-for="dimension in ['Daily', 'Weekly']"
                      :key="dimension"
                      :aria-pressed="analyticsDimension === dimension"
                      @click="analyticsDimension = dimension"
                    >
                      {{ dimension }}
                    </button>
                  </div>
                </header>
                <p>No usage data available for this window.</p>
              </section>
              <section class="analytics-panel">
                <header>
                  <h3>Leaderboard</h3>
                  <div class="segmented">
                    <button
                      v-for="metric in ['Tokens', 'Cost', 'Time', 'Runs']"
                      :key="metric"
                      :aria-pressed="leaderboardMetric === metric"
                      @click="leaderboardMetric = metric"
                    >
                      {{ metric }}
                    </button>
                  </div>
                </header>
                <p>Agent usage will appear when your control plane reports it.</p>
              </section></template
            >
            <section v-else class="analytics-panel">
              <header><h3>Errors</h3></header>
              <p>No error-reporting service connected.</p>
            </section>
          </div>
        </template>
        <template v-else-if="section === 'code'">
          <h3>Code hosting</h3>
          <div class="setting-box">
            <div class="setting-row code-host">
              <span class="code-host-icon"><Icon name="folder" :size="24" /></span
              ><span
                >GitHub <small class="inline-muted">Not connected</small
                ><small
                  >GitHub integration requires a connected control plane. No credentials are stored
                  here.</small
                ></span
              ><button class="button compact" disabled>Connect GitHub</button>
            </div>
          </div>
          <header class="subsection-toolbar">
            <div>
              <h3>Repositories</h3>
              <p class="section-description">Repository references saved on this device.</p>
            </div>
            <button class="button primary compact" @click="openEditor('repository')">
              <Icon name="plus" :size="14" />Add repository<Icon name="down" :size="12" />
            </button>
          </header>
          <div class="setting-box">
            <p v-if="!settingsDraft.repositories.length" class="repositories-empty">
              No repositories yet.
            </p>
            <div
              v-for="repository in settingsDraft.repositories"
              :key="repository.id"
              class="setting-row"
            >
              <span class="repository-url">{{ repository.url }}</span
              ><button
                class="icon-button"
                aria-label="Remove repository reference"
                @click="
                  settingsDraft.repositories = settingsDraft.repositories.filter(
                    (item) => item.id !== repository.id,
                  )
                "
              >
                <Icon name="close" />
              </button>
            </div>
          </div>
          <h3 class="subsequent-heading">Pull requests & tasks</h3>
          <div class="setting-box">
            <label class="setting-row"
              ><span
                >Pull Request sidebar<small
                  >Show linked pull requests inside the task detail sidebar.</small
                ></span
              ><input
                v-model="settingsDraft.code.sidebar"
                class="setting-switch"
                type="checkbox"
                role="switch"
                aria-label="Pull Request sidebar" /></label
            ><label class="setting-row"
              ><span
                >Auto-link tasks and PRs<small
                  >Requires a connected code-hosting service.</small
                ></span
              ><input
                class="setting-switch"
                type="checkbox"
                role="switch"
                disabled
                aria-label="Auto-link tasks and PRs" /></label
            ><label class="setting-row"
              ><span
                >Co-authored-by trailer<small
                  >Commit attribution requires a connected code-hosting service.</small
                ></span
              ><input
                class="setting-switch"
                type="checkbox"
                role="switch"
                disabled
                aria-label="Co-authored-by trailer"
            /></label>
            <div class="setting-row">
              <span
                >After PRs merge, move the task to<small
                  >Scientific stages advance only after their evidence gates pass.</small
                ></span
              ><select aria-label="After PRs merge, move the task to" disabled>
                <option>Keep current stage</option>
              </select>
            </div>
          </div>
        </template>
        <template v-else-if="section === 'connection'">
          <h3>Research control plane</h3>
          <form @submit.prevent="applyConnection">
            <div class="setting-box">
              <label class="setting-row"
                ><span>API endpoint<small>Leave blank to use local drafts.</small></span
                ><input v-model="endpoint" type="url" placeholder="http://localhost:8080"
              /></label>
            </div>
            <p class="settings-note">
              {{
                service.mode === 'local'
                  ? 'Local drafts are saved on this device. Scientific execution requires a control plane.'
                  : 'Connected to the research control plane.'
              }}
              Changing the connection preserves local drafts.
            </p>
            <button class="button primary save-button" :disabled="busy">
              {{ busy ? 'Connecting…' : 'Save connection' }}
            </button>
          </form>
        </template>
        <template v-else-if="section === 'skills' || section === 'mcp'"
          ><h3>Workspace {{ section === 'skills' ? 'skills' : 'connectors' }}</h3>
          <div class="setting-box">
            <div class="setting-row">
              <span
                >Manage {{ section === 'skills' ? 'skills' : 'connectors' }} in your
                workspace.</span
              ><button
                class="button"
                @click="router.push(section === 'skills' ? '/skills' : '/connectors')"
              >
                Open {{ section === 'skills' ? 'Skills' : 'Connectors' }}<Icon name="right" />
              </button>
            </div></div
        ></template>
        <template v-else
          ><div class="settings-unavailable">
            <Icon
              :name="
                section === 'tokens'
                  ? 'link'
                  : section === 'notifications'
                    ? 'bell'
                    : section === 'analytics'
                      ? 'chart'
                      : 'settings'
              "
              :size="28"
            />
            <h3>{{ title }}</h3>
            <p>
              {{
                section === 'tokens'
                  ? 'API tokens will be managed by your connected control plane.'
                  : section === 'notifications'
                    ? 'Notification delivery requires your connected control plane.'
                    : section === 'analytics'
                      ? 'Usage analytics appear when your control plane records research activity.'
                      : 'This setting requires a connected control plane with settings support.'
              }}
            </p>
            <button class="button" @click="open('connection')">Configure connection</button>
          </div></template
        >
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      </section>
    </div>
    <Modal
      v-if="editor"
      class="settings-dialog"
      :title="
        editor === 'repository' ? 'Add repository' : `${editingId ? 'Edit' : 'New'} ${editor}`
      "
      @close="editor = null"
      ><form class="settings-editor" @submit.prevent="saveEditor">
        <header>
          <h2>
            {{
              editor === 'repository' ? 'Add repository' : `${editingId ? 'Edit' : 'New'} ${editor}`
            }}
          </h2>
          <button type="button" class="icon-button" aria-label="Close" @click="editor = null">
            <Icon name="close" />
          </button>
        </header>
        <p>
          {{
            editor === 'label'
              ? `This label belongs only to the ${labelScope} catalog.`
              : 'Saved on this device.'
          }}
        </p>
        <div class="settings-editor-fields">
          <template v-if="editor === 'repository'"
            ><label
              >Repository URL<input
                v-model="draftUrl"
                type="url"
                placeholder="https://github.com/owner/repository"
                autofocus
                required /></label></template
          ><template v-else-if="editor === 'label'"
            ><label
              >Name<input
                v-model="draftName"
                aria-label="Name"
                placeholder="e.g. Bug"
                maxlength="120"
                autofocus
                required /></label
            ><label
              >Description<textarea
                v-model="draftDescription"
                aria-label="Description"
                placeholder="Describe when this label should be used"
                maxlength="2000"
              />
            </label>
            <div class="editor-color-field">
              <span>Color</span><ColorPicker v-model="draftColor" label="Color" /></div></template
          ><template v-else
            ><div class="property-editor-row">
              <label
                >Icon<select v-model="draftIcon" aria-label="Choose icon">
                  <option value="filters">☷</option>
                  <option value="flag">⚑</option>
                  <option value="tag">◇</option>
                  <option value="clock">◷</option>
                  <option value="users">♙</option>
                </select></label
              ><label
                >Name<input
                  v-model="draftName"
                  aria-label="Name"
                  placeholder="e.g. Severity"
                  maxlength="120"
                  autofocus
                  required /></label
              ><label
                >Type<CompactSelect
                  v-model="draftType"
                  label="Type"
                  :width="148"
                  :options="propertyTypes.map((type) => ({ value: type, label: type }))"
              /></label>
            </div>
            <label
              >Description<textarea
                v-model="draftDescription"
                aria-label="Description"
                placeholder="What this property means and when to set it"
                maxlength="2000"
              />
            </label>
            <div v-if="['Select', 'Multi-select'].includes(draftType)" class="property-options">
              <span>Options</span>
              <div v-for="(option, index) in optionRows" :key="index" class="property-option-row">
                <Icon name="menu" :size="12" /><ColorPicker
                  :model-value="
                    draftOptionColors[index] || colorPresets[(index + 6) % colorPresets.length]!
                  "
                  :label="'Option ' + (index + 1) + ' color'"
                  swatch-only
                  @update:model-value="draftOptionColors[index] = $event"
                /><input
                  :value="option"
                  aria-label="Option name"
                  placeholder="Option name"
                  @input="updateOption(index, $event)"
                /><button
                  class="icon-button"
                  type="button"
                  aria-label="Remove option"
                  :disabled="optionRows.length === 1"
                  @click="removeOption(index)"
                >
                  <Icon name="close" :size="14" />
                </button>
              </div>
              <button class="add-option" type="button" @click="draftOptions += '\n'">
                <Icon name="plus" :size="14" />Add option
              </button>
            </div></template
          >
        </div>
        <p v-if="editorError" class="form-error" role="alert">{{ editorError }}</p>
        <footer>
          <small v-if="editor === 'label'" class="catalog-storage-note">Saved on this device.</small
          ><button type="button" class="cancel-editor" @click="editor = null">Cancel</button
          ><button
            class="button primary"
            type="submit"
            :disabled="editor !== 'repository' && !draftName.trim()"
          >
            {{
              editor === 'label'
                ? 'Save label'
                : editor === 'property'
                  ? 'Save property'
                  : 'Add repository'
            }}
          </button>
        </footer>
      </form></Modal
    >
  </div>
</template>
<style scoped>
.setting-row .reply-mode-select {
  width: 192px;
  max-width: 48%;
  flex: none;
}

.notifications-layout .settings-heading {
  margin-bottom: 31px;
}
.code-layout .settings-heading {
  margin-bottom: 32px;
}
.code-layout .code-host {
  min-height: 106px;
}
.code-layout .repositories-empty {
  min-height: 78px;
}
.code-layout .subsection-toolbar {
  margin-top: 31px;
}
.code-layout .setting-row:not(.code-host) {
  min-height: 71px;
}

.desktop-settings {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.settings-rail {
  width: 240px;
  flex: none;
  border-right: 1px solid var(--border);
  padding: 16px 12px;
  overflow: auto;
}
.settings-rail h1 {
  font-size: 20px;
  line-height: 28px;
  font-weight: 600;
  padding: 0 8px;
  margin-bottom: 8px;
}
.settings-search {
  height: 32px;
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  color: var(--muted);
  margin-bottom: 12px;
}
.settings-search input {
  border: 0;
  background: none;
  outline: none;
  min-width: 0;
  width: 100%;
  font-size: 14px;
}
.settings-rail section + section {
  border-top: 1px solid var(--border);
  margin-top: 12px;
  padding-top: 12px;
}
.settings-rail section.extension-group {
  border: 0;
  padding: 0;
  margin-top: 8px;
}
.settings-rail section header {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 32px;
  padding: 0 12px;
  font-size: 14px;
}
.settings-rail section header strong {
  font-size: 14px;
  font-weight: 500;
}
.settings-rail section header small {
  font-size: 11px;
  margin-left: auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  max-width: 95px;
}
.tiny-avatar {
  width: 16px;
  height: 16px;
  background: var(--input);
  color: var(--muted);
  border-radius: 4px;
  display: grid;
  place-items: center;
  font-size: 9px;
}
.settings-rail section:first-of-type .tiny-avatar {
  border-radius: 50%;
}
.settings-rail .extension-group header {
  padding: 0 12px;
  font-size: 12px;
  color: var(--muted);
}
.settings-rail .extension-group header strong {
  font-size: 12px;
  font-weight: 400;
}
.settings-rail button {
  height: 32px;
  margin-bottom: 1px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 0 12px;
  text-align: left;
  color: var(--muted);
  font-size: 14px;
}
.settings-rail button:hover {
  background: var(--input);
}
.settings-rail button.selected {
  background: var(--selected);
  color: var(--text);
}
.settings-scroll {
  flex: 1;
  min-width: 0;
  overflow: auto;
}
.settings-body {
  max-width: 888px;
  padding: 16px 16px 56px;
  margin: 0 auto;
}
.settings-body.wide {
  max-width: 1016px;
}
.settings-heading {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 34px;
  height: 28px;
}
.settings-heading h2 {
  font-size: 20px;
  font-weight: 600;
  line-height: 28px;
}
.scope-badge {
  font-size: 12px;
  font-weight: 400;
  background: var(--border);
  color: var(--muted);
  border-radius: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  line-height: 16px;
  white-space: nowrap;
}
.settings-body h3 {
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 2px 12px;
}
.setting-box {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}
.setting-row {
  min-height: 64px;
  padding: 14px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  font-size: 14px;
}
.setting-row + .setting-row {
  border-top: 1px solid var(--border);
}
.setting-row small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  margin-top: 4px;
}
.setting-row input:not([type='checkbox']),
.setting-row textarea,
.setting-row select {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 7px;
  font: inherit;
  padding: 6px 10px;
  width: 47%;
  min-width: 0;
  height: 32px;
}
.setting-row select {
  width: auto;
  max-width: 48%;
  min-width: 192px;
}
.setting-row.about-row {
  align-items: flex-start;
  min-height: 114px;
}
.setting-row textarea {
  height: 64px;
  resize: vertical;
  line-height: 20px;
}
.about-control {
  width: 47%;
}
.about-control textarea {
  width: 100%;
}
.about-control small {
  text-align: right;
  margin-top: 0;
}
.avatar-row {
  height: 92px;
}
.profile-avatar {
  height: 64px;
  width: 64px;
  border-radius: 50%;
  background: var(--border);
  font-size: 26px;
  color: var(--muted);
  overflow: hidden;
}
.profile-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.settings-body.wide h3:not(:first-of-type) {
  margin-top: 32px;
}
.theme-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}
.theme-card {
  text-align: left;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  background: var(--input);
  min-width: 0;
  position: relative;
}
.theme-card.chosen {
  outline: 1px solid var(--primary);
  outline-offset: 0;
  border-color: var(--primary);
}
.theme-preview {
  height: 106px;
  padding: 12px;
  background: var(--swatch-bg);
}
.theme-preview > div {
  background: var(--swatch-input);
  height: 100%;
  padding: 12px;
  border-radius: 8px;
}
.theme-preview i {
  display: block;
  width: 66%;
  height: 8px;
  border-radius: 5px;
  background: var(--swatch-text);
  opacity: 0.9;
  margin-bottom: 6px;
}
.theme-preview i + i {
  width: 50%;
  opacity: 0.4;
  margin-bottom: 12px;
}
.theme-preview span {
  display: inline-block;
  background: var(--swatch-accent);
  color: white;
  border-radius: 6px;
  padding: 4px 7px;
  font-size: 12px;
  line-height: 16px;
}
.theme-preview b {
  display: inline-block;
  background: var(--swatch-accent);
  width: 16px;
  height: 16px;
  border-radius: 50%;
  vertical-align: middle;
  margin-left: 8px;
}
.theme-card footer {
  padding: 11px 12px 10px;
  font-size: 14px;
  position: relative;
}
.theme-card footer small {
  display: block;
  font-size: 11px;
  line-height: 16px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 10px;
}
.theme-card footer > .icon {
  position: absolute;
  right: 12px;
  top: 18px;
  background: var(--primary);
  color: #fff;
  border-radius: 50%;
  padding: 4px;
  width: 20px;
  height: 20px;
}
.setting-row input.setting-switch {
  appearance: none;
  flex: none;
  width: 32px;
  height: 18px;
  background: var(--border);
  border-radius: 12px;
  position: relative;
  cursor: pointer;
  margin: 0;
}
.setting-switch:checked {
  background: var(--primary) !important;
}
.setting-switch::after {
  content: '';
  position: absolute;
  width: 14px;
  height: 14px;
  left: 2px;
  top: 2px;
  background: white;
  box-shadow: 0 1px 2px #0002;
  border-radius: 50%;
}
.setting-switch:checked::after {
  left: 16px;
}
.segmented {
  padding: 3px;
  display: flex;
  border-radius: 7px;
  background: var(--border);
  flex: none;
}
.segmented button {
  padding: 4px 14px;
  min-width: 104px;
  font-size: 13px;
  border-radius: 4px;
  color: var(--muted);
}
.segmented button[aria-pressed='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 1px 3px #0001;
}
.save-button {
  margin-top: 16px;
}
.settings-note {
  font-size: 13px;
  color: var(--muted);
  line-height: 20px;
  margin-top: 16px;
}
.settings-unavailable {
  display: flex;
  align-items: center;
  flex-direction: column;
  text-align: center;
  gap: 16px;
  padding: 80px 20px;
  color: var(--muted);
}
.settings-unavailable h3 {
  margin: 0;
}
.settings-unavailable p {
  max-width: 430px;
  font-size: 14px;
  line-height: 22px;
}
.tokens-body {
  margin-top: 38px;
}
.token-note {
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
  margin: 0 2px 20px;
}
.token-usage {
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 20px;
}
.token-usage summary {
  cursor: pointer;
}
.token-usage p {
  padding: 12px 16px;
  line-height: 20px;
}
.token-form {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  min-height: 66px;
}
.token-form input,
.token-form select {
  height: 32px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  padding: 0 10px;
  font-size: 14px;
  min-width: 0;
}
.token-form input {
  flex: 1;
}
.token-form select {
  width: 96px;
}
.token-form .button {
  margin-left: 26px;
}
.tokens-empty {
  margin-top: 12px;
  min-height: 72px;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  color: var(--muted);
  font-size: 13px;
}
.tokens-empty .text-button {
  margin-left: auto;
}
.section-description {
  font-size: 12px;
  color: var(--muted);
  line-height: 18px;
  margin: -7px 2px 14px;
}
.notification-box .setting-row {
  min-height: 71px;
}
.table-layout .settings-heading {
  margin-bottom: 8px;
}
.table-description {
  font-size: 14px;
  line-height: 22px;
  color: var(--muted);
  margin: 0 0 32px;
}
.label-scope {
  width: max-content;
  margin: 0 0 16px;
}
.label-scope button {
  min-width: 64px;
  padding: 4px 10px;
}
.label-scope small {
  font-size: 12px;
}
.table-toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;
}
.table-search {
  display: flex;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 12px;
  height: 32px;
  width: 384px;
  max-width: 50%;
  color: var(--muted);
  margin-right: auto;
}
.table-search input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
}
.table-toolbar > small {
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
.archived-control {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
  font-size: 12px;
  color: var(--muted);
}
.archived-control .setting-switch {
  appearance: none;
  width: 32px;
  height: 18px;
  background: var(--border);
  border-radius: 12px;
  position: relative;
  cursor: pointer;
  margin: 0;
}
.settings-data-table {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.settings-data-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  text-align: left;
  table-layout: fixed;
}
.settings-data-table th {
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
  height: 38px;
  background: var(--border);
}
.settings-data-table td,
.settings-data-table th {
  padding: 8px 16px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.settings-data-table th:first-child {
  width: 29%;
}
.settings-data-table th:nth-last-child(-n + 2) {
  width: 15%;
}
.settings-data-table td {
  border-top: 1px solid var(--border);
  height: 42px;
}
.settings-data-table td > button {
  padding: 0;
  text-align: left;
}
.settings-data-table td > button:hover {
  text-decoration: underline;
}
.label-color {
  display: inline-block;
  height: 10px;
  width: 10px;
  border-radius: 50%;
  margin-right: 8px;
}
.table-empty {
  height: 152px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  font-size: 14px;
}
.table-empty > .icon {
  color: var(--muted);
}
.table-empty small {
  font-size: 12px;
  color: var(--muted);
  margin-top: -6px;
}
.property-empty {
  height: 170px;
}
.settings-editor {
  padding: 24px;
  display: grid;
  gap: 18px;
}
.settings-editor header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.settings-editor h2 {
  font-size: 18px;
}
.settings-editor > p {
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
}
.settings-editor label {
  display: grid;
  gap: 8px;
  font-size: 13px;
}
.settings-editor input,
.settings-editor textarea,
.settings-editor select {
  border: 1px solid var(--border);
  background: var(--surface);
  padding: 8px 10px;
  border-radius: 6px;
  width: 100%;
}
.settings-editor textarea {
  height: 78px;
  resize: vertical;
}
.settings-editor input[type='color'] {
  height: 36px;
  width: 72px;
  padding: 3px;
}
.settings-editor footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.settings-editor .form-error {
  margin: 0;
}
.analytics-layout {
  max-width: none;
  padding: 0;
}
.analytics-layout .settings-heading {
  padding: 16px 20px;
  height: 56px;
  margin: 0;
}
.analytics-status {
  margin-left: auto;
  font-size: 12px;
  color: var(--muted);
  display: flex;
  align-items: center;
  gap: 8px;
}
.analytics-toolbar {
  height: 44px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
}
.analytics-tabs {
  display: flex;
  align-self: stretch;
  margin-right: auto;
}
.analytics-tabs button {
  padding: 0 12px;
  border-bottom: 2px solid transparent;
  color: var(--muted);
  font-size: 14px;
}
.analytics-tabs button[aria-selected='true'] {
  color: var(--text);
  border-color: var(--text);
}
.analytics-toolbar select {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  height: 28px;
  font-size: 13px;
  padding: 0 8px;
}
.analytics-content {
  max-width: 1136px;
  padding: 24px 16px;
  margin: 0 auto;
}
.usage-metrics {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 20px 0;
  margin-bottom: 20px;
}
.usage-metrics > div {
  padding: 0 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.usage-metrics > div + div {
  border-left: 1px solid var(--border);
}
.usage-metrics small {
  font-size: 11px;
  color: var(--muted);
}
.usage-metrics strong {
  font-size: 24px;
  font-weight: 500;
}
.analytics-panel {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 20px;
  padding: 20px;
}
.analytics-panel header {
  display: flex;
  align-items: center;
  gap: 12px;
}
.analytics-panel h3 {
  margin: 0 auto 0 0;
}
.analytics-panel .segmented button {
  min-width: 0;
  padding: 3px 9px;
  font-size: 12px;
}
.analytics-panel > p {
  height: 140px;
  display: grid;
  place-items: center;
  font-size: 13px;
  color: var(--muted);
}
.code-host {
  min-height: 108px;
  gap: 12px;
  align-items: flex-start;
}
.code-host > span:nth-child(2) {
  flex: 1;
  line-height: 22px;
  padding-top: 2px;
}
.code-host > .button {
  align-self: center;
}
.code-host-icon {
  background: var(--surface);
  border: 1px solid var(--border);
  height: 36px;
  width: 36px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  flex: none;
}
.inline-muted {
  display: inline !important;
  margin-left: 6px;
}
.subsection-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 32px;
}
.subsection-toolbar h3 {
  margin-bottom: 12px;
}
.subsection-toolbar .section-description {
  margin-bottom: 14px;
}
.repositories-empty {
  min-height: 80px;
  display: grid;
  place-items: center;
  color: var(--muted);
  font-size: 12px;
}
.repository-url {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.career-box {
  padding: 14px 16px 16px;
}
.career-box label {
  display: block;
  font-size: 14px;
}
.career-box select {
  display: block;
  width: 100%;
  height: 40px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  margin: 6px 0 16px;
  padding: 0 12px;
}
.subsequent-heading {
  margin-top: 32px !important;
}
.inline-setting {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}
.inline-setting code {
  font-size: 13px;
}
.inline-setting input {
  width: 100px !important;
}
.danger-button {
  color: #bd4444;
}
.no-match {
  font-size: 13px;
  padding: 10px;
  color: var(--muted);
}
.fields-description {
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
  margin: -5px 2px 14px;
}
.field-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  text-align: left;
}
.field-table th,
.field-table td {
  height: 42px;
  padding: 8px 16px;
  font-weight: 400;
  border-bottom: 1px solid var(--border);
}
.field-table thead th {
  font-size: 12px;
  color: var(--muted);
}
.field-table th:first-child {
  width: 50%;
}
.field-table td,
.field-table th:not(:first-child) {
  text-align: center;
}
.field-table tr:last-child th,
.field-table tr:last-child td {
  border-bottom: 0;
}
.field-table input {
  accent-color: var(--primary);
  width: 14px;
  height: 14px;
  cursor: pointer;
}
.field-table td > span {
  color: var(--muted);
}
@media (max-width: 1100px) {
  .settings-body {
    padding: 16px 24px 40px;
  }
  .settings-rail {
    width: 220px;
  }
  .theme-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .setting-row {
    gap: 12px;
  }
  .setting-row select {
    min-width: 140px;
  }
  .setting-row > span {
    max-width: 55%;
  }
}
:deep(.settings-dialog) {
  width: 514px;
  overflow: visible;
}
.settings-editor {
  padding: 16px;
  gap: 0;
}
.settings-editor header h2 {
  font-size: 16px;
  font-weight: 500;
}
.settings-editor header {
  height: 24px;
}
.settings-editor > p {
  font-size: 14px;
  line-height: 20px;
  margin-top: 4px;
}
.settings-editor-fields {
  display: grid;
  gap: 20px;
  margin-top: 24px;
  margin-bottom: 24px;
}
.settings-editor label,
.editor-color-field > span,
.property-options > span {
  font-size: 14px;
  line-height: 20px;
}
.settings-editor label {
  gap: 6px;
}
.settings-editor input,
.settings-editor textarea,
.settings-editor select {
  background: transparent;
  border-color: color-mix(in srgb, var(--surface) 40%, transparent);
  font-size: 14px;
  padding: 6px 10px;
  height: 32px;
}
.settings-editor textarea {
  height: 64px;
}
.settings-editor footer {
  border-top: 1px solid color-mix(in srgb, var(--surface) 30%, transparent);
  margin: 0 -16px -16px;
  padding: 16px;
  gap: 12px;
  background: #00000003;
  border-radius: 0 0 16px 16px;
}
.cancel-editor {
  font-size: 14px;
  padding: 4px 8px;
}
.property-editor-row {
  display: grid;
  grid-template-columns: 68px 1fr 160px;
  gap: 12px;
}
.property-editor-row label {
  min-width: 0;
}
.property-editor-row select {
  max-width: 100%;
}
.property-options {
  display: grid;
  gap: 6px;
}
.property-option-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.property-option-row > i {
  width: 22px;
  height: 22px;
  flex: none;
  border-radius: 50%;
}
.property-option-row input {
  flex: 1;
  min-width: 0;
}
.property-option-row .icon {
  color: var(--muted);
}
.add-option {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  padding: 8px 10px;
  text-align: left;
}
.editor-color-field {
  position: relative;
}
.editor-color-field > :deep(.settings-color) {
  margin-top: 6px;
}
.settings-editor .form-error {
  margin-bottom: 16px;
}
.field-table {
  table-layout: fixed;
}
.field-table th:first-child {
  width: calc(100% - 256px);
}
.field-table thead th {
  height: 48px;
  line-height: 16px;
}
.field-table thead th:not(:first-child) {
  width: 128px;
  padding-left: 22px;
  padding-right: 22px;
}
.field-table tbody th,
.field-table tbody td {
  height: 41px;
}
.field-table td {
  text-align: left !important;
  width: 128px;
}
.field-table input {
  width: 16px;
  height: 16px;
  margin: 0;
}
.field-table td > span {
  display: block;
  text-align: center;
}
.property-option-row > :deep(.settings-color) {
  flex: none;
}

.settings-editor-fields {
  margin-top: 18px;
  margin-bottom: 32px;
  gap: 18px;
}
.settings-editor label {
  gap: 4px;
}
.editor-color-field > :deep(.settings-color) {
  margin-top: 4px;
}
.catalog-storage-note {
  font-size: 11px;
  color: var(--muted);
  margin-right: auto;
  align-self: center;
}
.editor-color-field :deep(.color-surface) {
  gap: 9px;
}
</style>
