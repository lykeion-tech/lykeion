<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import type { Inquiry } from '../../../../packages/contracts/src/research';
import Modal from '../components/Modal.vue';
import ComposerCalendar from '../components/ComposerCalendar.vue';
import { useRouter } from 'vue-router';
import Icon from '../components/Icon.vue';
import { state, workspaceName, service, refresh, notify } from '../services/workspace';
const props = defineProps<{
  initialManual?: boolean;
  initialAssigneeId?: string;
  initialParentId?: string;
  initialProjectId?: string;
}>();
const emit = defineEmits<{ close: [] }>();
const router = useRouter();
const isMac = navigator.platform.includes('Mac');
const parentId = ref(props.initialParentId ?? '');
const manual = ref(Boolean(props.initialParentId || props.initialManual)),
  expanded = ref(false),
  title = ref(''),
  description = ref(''),
  projectId = ref(props.initialProjectId ?? ''),
  assigneeId = ref(props.initialAssigneeId ?? ''),
  another = ref(false),
  busy = ref(false),
  error = ref(''),
  menu = ref(''),
  query = ref(''),
  labels = ref<string[]>([]),
  dueDate = ref(''),
  startDate = ref('');
const priority = ref<Inquiry['priority']>('none'),
  form = ref<HTMLFormElement>();
const defaults = {
  agent: ['Project'],
  manual: ['Status', 'Priority', 'Assignee', 'Labels', 'Project'],
};
function readFields() {
  try {
    const value = JSON.parse(localStorage.getItem('lykeion.preferences') ?? '{}').createFields;
    return {
      agent: Array.isArray(value?.agent) ? value.agent : defaults.agent,
      manual: Array.isArray(value?.manual) ? value.manual : defaults.manual,
    };
  } catch {
    return defaults;
  }
}
const preferences = ref(readFields());
function preferencesChanged() {
  preferences.value = readFields();
}
function show(field: string) {
  return (
    preferences.value[manual.value ? 'manual' : 'agent'].includes(field) ||
    extraFields.value.includes(field)
  );
}
const extraFields = ref<string[]>([]),
  childIds = ref<string[]>([]);
const taskMatches = computed(() =>
  query.value.trim()
    ? (state.data?.inquiries ?? []).filter(
        (x) =>
          `${taskLabel(x.id)} ${x.title}`.toLowerCase().includes(query.value.toLowerCase()) &&
          !childIds.value.includes(x.id),
      )
    : [],
);
function taskLabel(id: string) {
  const index = state.data?.inquiries.findIndex((x) => x.id === id) ?? -1;
  return `${state.data?.workspace.taskPrefix ?? 'TASK'}-${index + 1}`;
}
function revealDate(name: 'due' | 'start') {
  extraFields.value.push(name === 'due' ? 'Due date' : 'Start date');
  openMenu(name);
}
function customize() {
  router.push('/settings?tab=preferences');
  emit('close');
}
const agents = computed(() => state.data?.agents ?? []);
const assignees = computed(() => [
  ...agents.value.map((x) => ({ id: x.id, name: x.name, kind: 'agent', status: x.status })),
  ...(state.data?.colleagues ?? []).map((x) => ({
    id: x.id,
    name: x.name,
    kind: 'person',
    status: '',
  })),
]);
const selectedAssignee = computed(() => assignees.value.find((x) => x.id === assigneeId.value));
const parentLabel = computed(() => {
  const index = state.data?.inquiries.findIndex((x) => x.id === parentId.value) ?? -1;
  return index >= 0 ? `${state.data?.workspace.taskPrefix}-${index + 1}` : parentId.value;
});
const selectedProject = computed(() => state.data?.projects.find((x) => x.id === projectId.value));
const matches = computed(() =>
  assignees.value.filter(
    (x) =>
      (manual.value || x.kind === 'agent') &&
      x.name.toLowerCase().includes(query.value.toLowerCase()),
  ),
);
const projects = computed(() =>
  (state.data?.projects ?? []).filter((x) =>
    x.name.toLowerCase().includes(query.value.toLowerCase()),
  ),
);
const priorityOptions = [
  { id: 'none', label: 'No priority' },
  { id: 'urgent', label: 'Urgent' },
  { id: 'high', label: 'High' },
  { id: 'medium', label: 'Medium' },
  { id: 'low', label: 'Low' },
] as const;
const knownLabels = computed(() =>
  [
    ...new Set([...(state.data?.inquiries.flatMap((x) => x.labels ?? []) ?? []), ...labels.value]),
  ].filter((x) => x.toLowerCase().includes(query.value.toLowerCase())),
);
async function openMenu(name: string) {
  menu.value = menu.value === name ? '' : name;
  query.value = '';
  await nextTick();
  form.value?.querySelector<HTMLInputElement>('.composer-menu input')?.focus();
}
function closeMenu() {
  menu.value = '';
  query.value = '';
}
function chooseAssignee(id: string) {
  assigneeId.value = id;
  closeMenu();
}
function toggleLabel(label: string) {
  labels.value = labels.value.includes(label)
    ? labels.value.filter((x) => x !== label)
    : [...labels.value, label];
}
function addLabel() {
  const value = query.value.trim();
  if (value && !labels.value.includes(value)) labels.value.push(value);
  query.value = '';
}
function keys(event: KeyboardEvent) {
  if (
    event.key === 'Enter' &&
    menu.value &&
    event.target instanceof HTMLInputElement &&
    menu.value !== 'labels'
  ) {
    event.preventDefault();
    if (!['due', 'start'].includes(menu.value))
      form.value?.querySelector<HTMLButtonElement>('.composer-menu button')?.click();
    return;
  }
  if (event.key === 'Escape' && menu.value) {
    event.preventDefault();
    event.stopPropagation();
    closeMenu();
    return;
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    const list = [
      ...(form.value?.querySelectorAll<HTMLButtonElement>('.composer-menu button:not(:disabled)') ??
        []),
    ];
    if (!list.length) return;
    event.preventDefault();
    const index = list.indexOf(document.activeElement as HTMLButtonElement);
    list[(index + (event.key === 'ArrowDown' ? 1 : -1) + list.length) % list.length]?.focus();
  }
}
function outside(event: PointerEvent) {
  if (!(event.target as HTMLElement).closest('.property-anchor,.created-by,.task-relation-picker'))
    closeMenu();
}
async function switchMode() {
  manual.value = !manual.value;
  error.value = '';
  closeMenu();
  await nextTick();
  form.value
    ?.querySelector<HTMLInputElement | HTMLTextAreaElement>(
      manual.value ? '.composer-title' : '.composer-prompt',
    )
    ?.focus();
}
async function submit() {
  if (busy.value) return;
  if (!manual.value) {
    error.value = !assigneeId.value
      ? 'Choose an agent to continue.'
      : 'Agent-assisted task creation requires an execution adapter. Your instructions have not been sent.';
    return;
  }
  if (!title.value.trim()) return;
  if (childIds.value.length) {
    error.value =
      'Attaching existing sub-tasks while creating a task requires a connected relationship adapter. No task has been created.';
    return;
  }
  busy.value = true;
  error.value = '';
  try {
    await service.value.createInquiry({
      title: title.value,
      description: description.value,
      priority: priority.value,
      projectId: projectId.value || null,
      assigneeId: assigneeId.value || null,
      labels: labels.value,
      dueDate: dueDate.value || null,
      startDate: startDate.value || null,
      parentId: parentId.value || null,
    });
    await refresh();
    notify(service.value.mode === 'local' ? 'Task saved as a local draft.' : 'Task created.');
    if (another.value) {
      title.value = '';
      description.value = '';
      await nextTick();
      form.value?.querySelector<HTMLInputElement>('.composer-title')?.focus();
    } else emit('close');
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  window.addEventListener('lykeion:preferences', preferencesChanged);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('lykeion:preferences', preferencesChanged);
});
</script>
<template>
  <Modal
    :title="manual ? 'Create manually' : 'Create with agent'"
    :class="['task-composer', { manual, expanded }]"
    @close="emit('close')"
  >
    <!-- keys() must see modified keys too (Shift+Escape, Cmd+Enter in a menu), so no .exact. -->
    <!-- eslint-disable vue/use-v-on-exact -->
    <form
      ref="form"
      @submit.prevent="submit"
      @keydown="keys"
      @keydown.meta.enter.prevent="submit"
      @keydown.ctrl.enter.prevent="submit"
    >
      <!-- eslint-enable vue/use-v-on-exact -->
      <header class="composer-breadcrumb">
        <span>{{ workspaceName }}</span>
        <Icon name="right" :size="12" />
        <strong>{{ manual ? 'Create manually' : 'Create with agent' }}</strong>
        <button
          type="button"
          class="icon-button push-right"
          :aria-label="expanded ? 'Collapse' : 'Expand'"
          :aria-pressed="expanded"
          @click="expanded = !expanded"
        >
          <Icon name="expand" :size="15" />
        </button>
        <button type="button" class="icon-button" aria-label="Close" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </header>
      <div class="composer-body">
        <template v-if="manual">
          <input
            v-model="title"
            autofocus
            class="composer-title"
            placeholder="Task title"
            aria-label="Task title"
            required
          />
          <textarea
            v-model="description"
            class="composer-description"
            placeholder="Add description..."
            aria-label="Description"
          />
        </template>
        <template v-else>
          <div class="created-by">
            <span>Created by</span>
            <button type="button" :aria-expanded="menu === 'agent'" @click="openMenu('agent')">
              <Icon v-if="selectedAssignee" name="bot" :size="14" />{{
                selectedAssignee?.name ?? 'Pick an agent…'
              }}<Icon name="down" :size="12" />
            </button>
            <div v-if="menu === 'agent'" class="composer-menu" role="menu">
              <input v-model="query" placeholder="Search agents..." aria-label="Search agents" />
              <button
                v-for="agent in matches"
                :key="agent.id"
                type="button"
                @click="chooseAssignee(agent.id)"
              >
                <Icon name="bot" :size="14" />
                <span>{{ agent.name }}</span>
                <small>{{ agent.status }}</small>
                <Icon v-if="assigneeId === agent.id" name="check" :size="14" />
              </button>
              <p v-if="!matches.length">
                {{ agents.length ? 'No matching agents' : 'No agents available' }}
              </p>
            </div>
          </div>
          <textarea
            v-model="description"
            autofocus
            class="composer-prompt"
            placeholder='Tell the agent what to do, e.g. "review the literature for this research question"'
            aria-label="Agent instructions"
          />
        </template>
      </div>
      <p v-if="manual && selectedAssignee" class="assignee-note">
        <Icon :name="selectedAssignee.kind === 'agent' ? 'bot' : 'users'" :size="14" />
        <span
          >{{ selectedAssignee.name
          }}{{
            selectedAssignee.kind === 'agent'
              ? ' is assigned. Execution requires a connected control plane.'
              : ' is assigned to this task.'
          }}</span
        >
      </p>
      <div class="composer-properties">
        <div v-if="manual" class="property-line">
          <div v-if="show('Status')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('stage')">
              <Icon name="circle" :size="14" />Grounding
            </button>
            <div v-if="menu === 'stage'" class="composer-menu stage-menu">
              <button type="button" @click="closeMenu">
                <Icon name="circle" :size="14" />Grounding<Icon
                  name="check"
                  class="push-right"
                  :size="14"
                />
              </button>
              <p>New inquiries begin in Grounding. Later stages require evidence gates.</p>
            </div>
          </div>
          <div v-if="show('Priority')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('priority')">
              <Icon name="flag" :size="14" />{{
                priorityOptions.find((x) => x.id === priority)?.label
              }}
            </button>
          </div>
          <div v-if="show('Assignee')" class="property-anchor">
            <button type="button" class="property-pill assignee-pill" @click="openMenu('assignee')">
              <Icon :name="selectedAssignee?.kind === 'agent' ? 'bot' : 'users'" :size="14" />
              <span>{{ selectedAssignee?.name ?? 'Unassigned' }}</span>
            </button>
          </div>
          <div v-if="show('Labels')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('labels')">
              <Icon name="tag" :size="14" />{{ labels.length ? labels.join(', ') : 'Add label' }}
            </button>
          </div>
        </div>
        <div class="property-line">
          <div v-if="!manual && show('Priority')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('priority')">
              <Icon name="flag" :size="14" />{{
                priorityOptions.find((x) => x.id === priority)?.label
              }}
            </button>
          </div>
          <div v-if="show('Project')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('project')">
              <Icon name="folder" :size="14" />{{ selectedProject?.name ?? 'No project' }}
            </button>
          </div>
          <div v-if="parentId" class="property-anchor">
            <button
              type="button"
              class="property-pill parent-pill"
              aria-label="Remove parent task"
              :title="'Remove parent ' + parentLabel"
              @click="parentId = ''"
            >
              <Icon name="up" :size="12" />Sub-task of {{ parentLabel
              }}<Icon name="close" :size="12" />
            </button>
          </div>
          <div v-if="show('Due date')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('due')">
              <Icon name="clock" :size="14" />{{ dueDate || 'Due date' }}</button
            ><ComposerCalendar
              v-if="menu === 'due'"
              v-model="dueDate"
              kind="due"
              @close="closeMenu"
            />
          </div>
          <div v-if="show('Start date')" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('start')">
              <Icon name="clock" :size="14" />{{ startDate || 'Start date' }}</button
            ><ComposerCalendar
              v-if="menu === 'start'"
              v-model="startDate"
              kind="start"
              @close="closeMenu"
            />
          </div>
          <div v-for="id in childIds" :key="id" class="property-anchor">
            <button
              type="button"
              class="property-pill"
              @click="childIds = childIds.filter((x) => x !== id)"
            >
              <Icon name="down" :size="12" />{{ taskLabel(id) }}<Icon name="close" :size="12" />
            </button>
          </div>
          <div class="property-anchor">
            <button
              type="button"
              class="property-pill more-pill"
              aria-label="More properties"
              @click="openMenu('more')"
            >
              <Icon name="more" :size="15" />
            </button>
            <div v-if="menu === 'more'" class="composer-menu more-menu" role="menu">
              <button type="button" @click="revealDate('due')">
                <Icon name="clock" :size="14" />Set due date...
              </button>
              <button v-if="manual" type="button" @click="revealDate('start')">
                <Icon name="clock" :size="14" />Set start date...
              </button>
              <button v-if="manual" type="button" @click="openMenu('parent')">
                <Icon name="up" :size="14" />Set parent task...
              </button>
              <button v-if="manual" type="button" @click="openMenu('child')">
                <Icon name="down" :size="14" />Add sub-task...
              </button>
              <hr />
              <button type="button" @click="customize">
                <Icon name="filters" :size="14" />Customize fields...
              </button>
            </div>
          </div>
        </div>
        <div
          v-if="['priority', 'assignee', 'labels', 'project'].includes(menu)"
          class="property-anchor properties-menu-anchor"
        >
          <div class="composer-menu" role="menu">
            <template v-if="menu === 'priority'">
              <button
                v-for="p in priorityOptions"
                :key="p.id"
                type="button"
                @click="
                  priority = p.id;
                  closeMenu();
                "
              >
                <Icon name="flag" :size="14" />{{ p.label
                }}<Icon v-if="priority === p.id" name="check" :size="14" class="push-right" />
              </button>
            </template>
            <template v-else-if="menu === 'assignee'">
              <input
                v-model="query"
                placeholder="Search people and agents..."
                aria-label="Search assignees"
              />
              <button type="button" @click="chooseAssignee('')">
                <Icon name="users" :size="14" />Unassigned<Icon
                  v-if="!assigneeId"
                  name="check"
                  class="push-right"
                  :size="14"
                />
              </button>
              <button v-for="a in matches" :key="a.id" type="button" @click="chooseAssignee(a.id)">
                <Icon :name="a.kind === 'agent' ? 'bot' : 'users'" :size="14" />
                <span>{{ a.name }}</span>
                <Icon v-if="assigneeId === a.id" name="check" class="push-right" :size="14" />
              </button>
              <p v-if="!matches.length">
                {{ assignees.length ? 'No matching assignees' : 'No people or agents available' }}
              </p>
            </template>
            <template v-else-if="menu === 'project'">
              <input
                v-model="query"
                placeholder="Search projects..."
                aria-label="Search projects"
              />
              <button
                type="button"
                @click="
                  projectId = '';
                  closeMenu();
                "
              >
                <Icon name="folder" :size="14" />No project
              </button>
              <button
                v-for="project in projects"
                :key="project.id"
                type="button"
                @click="
                  projectId = project.id;
                  closeMenu();
                "
              >
                <Icon name="folder" :size="14" />{{ project.name
                }}<Icon
                  v-if="projectId === project.id"
                  name="check"
                  :size="14"
                  class="push-right"
                />
              </button>
              <p v-if="!projects.length">
                {{ state.data?.projects.length ? 'No matching projects' : 'No projects available' }}
              </p>
            </template>
            <template v-else-if="menu === 'labels'">
              <input
                v-model="query"
                placeholder="Search or create label..."
                aria-label="Search labels"
                @keydown.enter.prevent="addLabel"
              />
              <button
                v-for="label in knownLabels"
                :key="label"
                type="button"
                @click="toggleLabel(label)"
              >
                <Icon name="tag" :size="14" />{{ label
                }}<Icon v-if="labels.includes(label)" name="check" :size="14" class="push-right" />
              </button>
              <button
                v-if="query.trim() && !knownLabels.includes(query.trim())"
                type="button"
                @click="addLabel"
              >
                <Icon name="plus" :size="14" />Create “{{ query.trim() }}”
              </button>
              <p v-if="!knownLabels.length && !query">Type to create a label.</p>
            </template>
          </div>
        </div>
      </div>
      <div
        v-if="menu === 'parent' || menu === 'child'"
        class="task-relation-backdrop"
        @click.self="closeMenu"
      >
        <section
          class="task-relation-picker property-anchor"
          role="dialog"
          :aria-label="menu === 'parent' ? 'Set parent task' : 'Add sub-task'"
        >
          <div class="composer-menu relation-results">
            <div class="relation-search">
              <Icon name="search" :size="16" /><input
                v-model="query"
                autofocus
                role="combobox"
                aria-expanded="true"
                aria-controls="composer-task-suggestions"
                placeholder="Search tasks..."
                aria-label="Search tasks"
              />
            </div>
            <div id="composer-task-suggestions" role="listbox" aria-label="Suggestions">
              <button
                v-for="task in taskMatches"
                :key="task.id"
                type="button"
                @click="
                  menu === 'parent' ? (parentId = task.id) : childIds.push(task.id);
                  closeMenu();
                "
              >
                <Icon name="circle" :size="14" /><small>{{ taskLabel(task.id) }}</small
                ><span>{{ task.title }}</span>
              </button>
              <p v-if="!taskMatches.length">
                {{
                  !query.trim()
                    ? 'Type to search tasks'
                    : state.data?.inquiries.length
                      ? 'No matching tasks'
                      : 'No tasks available'
                }}
              </p>
            </div>
          </div>
        </section>
      </div>
      <p v-if="error" class="composer-error" role="alert">{{ error }}</p>
      <footer class="composer-footer">
        <button
          type="button"
          class="icon-button attachment-button"
          aria-label="Attach file"
          @click="
            error =
              'Attachments require a connected file-storage adapter. No file has been uploaded.'
          "
        >
          <Icon name="attachment" :size="14" />
        </button>
        <button type="button" class="mode-switch push-right" @click="switchMode">
          <Icon name="filters" :size="14" />Switch to {{ manual ? 'Agent' : 'Manual' }}
        </button>
        <label class="another-switch">
          <input v-model="another" type="checkbox" role="switch" />
          <span class="switch-track" />Create another</label
        >
        <button
          type="submit"
          class="button primary compact"
          :disabled="busy || (manual ? !title.trim() : !description.trim())"
        >
          {{ busy ? 'Creating…' : manual ? 'Create Task' : 'Create'
          }}<kbd>{{ isMac ? '⌘' : 'Ctrl' }}</kbd>
          <kbd>↵</kbd>
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.task-composer {
  width: 578px;
  max-width: calc(100vw - 48px);
  background: #e1e1e6;
  overflow: visible;
}
.task-composer.manual {
  width: 676px;
}
.task-composer.expanded {
  width: min(1100px, calc(100vw - 64px));
}
.task-composer form {
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 308px;
}
.task-composer.manual form {
  min-height: 384px;
}
.task-composer.expanded form {
  height: min(740px, 85vh);
}
.composer-breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 20px;
  height: 50px;
  flex: none;
  font-size: 12px;
}
.composer-breadcrumb > span {
  color: var(--muted);
}
.composer-breadcrumb strong {
  font-weight: 400;
}
.composer-breadcrumb > .icon {
  color: #a2a4a9;
}
.composer-breadcrumb .icon-button {
  width: 24px;
  height: 24px;
}
.composer-body {
  display: flex;
  flex-direction: column;
  padding: 0 20px;
  flex: 1;
  min-height: 166px;
}
.manual .composer-body {
  min-height: 171px;
}
.composer-title {
  padding: 0;
  border: 0;
  background: none;
  font-size: 18px;
  font-weight: 500;
  line-height: 25px;
  outline: 0;
  width: 100%;
  margin-bottom: 10px;
}
.composer-description,
.composer-prompt {
  padding: 0;
  border: 0;
  background: none;
  font-size: 14px;
  line-height: 23px;
  resize: none;
  outline: 0;
  flex: 1;
  min-height: 120px;
  width: 100%;
}
.composer-prompt {
  margin-top: 7px;
}
.composer-title::placeholder,
.composer-description::placeholder,
.composer-prompt::placeholder {
  color: var(--muted);
  opacity: 1;
}
.created-by {
  display: flex;
  align-items: center;
  gap: 8px;
  position: relative;
  font-size: 12px;
  height: 22px;
}
.created-by > span {
  color: var(--muted);
}
.created-by > button {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 0;
  font-size: 12px;
  max-width: calc(100% - 70px);
}
.created-by > button > .icon:last-child {
  opacity: 0;
}
.created-by > button:hover > .icon:last-child {
  opacity: 1;
}
.composer-properties {
  position: relative;
  padding: 8px 16px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.property-line {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 26px;
}
.property-anchor {
  position: relative;
}
.property-pill {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 10px;
  border: 1px solid #ffffff42;
  border-radius: 18px;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
  max-width: 240px;
}
.property-pill:hover {
  background: #ffffff30;
}
.assignee-pill > span {
  overflow: hidden;
  text-overflow: ellipsis;
}
.more-pill {
  padding: 0 10px;
}
.assignee-note {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 8px 20px;
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  line-height: 16px;
}
.composer-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-top: 1px solid #ffffff40;
  min-height: 54px;
  flex: none;
}
.composer-footer > .button {
  padding: 0 9px;
  gap: 5px;
  height: 26px;
  font-weight: 400;
  line-height: 18px;
}
.composer-footer kbd {
  height: 20px;
  min-width: 23px;
  border: 1px solid #ffffff4d;
  color: white;
  background: #ffffff15;
  font-size: 13px;
  padding: 0 3px;
}
.attachment-button {
  width: 24px;
  height: 24px;
}
.mode-switch {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  padding: 4px 6px;
  white-space: nowrap;
  border-radius: 5px;
}
.mode-switch:hover {
  background: #cdd9e433;
}
.another-switch {
  display: flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
  position: relative;
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
}
.another-switch input {
  position: absolute;
  opacity: 0;
  width: 24px;
  height: 16px;
  margin: 0;
}
.switch-track {
  height: 14px;
  width: 24px;
  border-radius: 12px;
  background: #e9e9ec;
  padding: 1px;
  display: flex;
}
.switch-track:before {
  content: '';
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: #fff;
}
.another-switch input:checked + .switch-track {
  background: var(--primary);
  justify-content: flex-end;
}
.another-switch input:focus-visible + .switch-track {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
.composer-error {
  color: #963d39;
  font-size: 12px;
  line-height: 18px;
  padding: 4px 20px 12px;
}
.composer-menu {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 70;
  min-width: 250px;
  max-width: 340px;
  max-height: 290px;
  overflow: auto;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 5px;
  box-shadow: 0 6px 22px #0002;
  color: var(--text);
  font-size: 13px;
}
.composer-menu > input {
  width: 100%;
  background: none;
  border: 0;
  border-bottom: 1px solid var(--border);
  outline: 0;
  padding: 9px 8px;
  margin-bottom: 4px;
  font-size: 13px;
}
.composer-menu > button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 31px;
  padding: 6px 8px;
  text-align: left;
  border-radius: 4px;
  font-size: 13px;
}
.composer-menu > button:hover,
.composer-menu > button:focus-visible {
  background: var(--input);
}
.composer-menu small {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
}
.composer-menu p {
  padding: 8px;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  white-space: normal;
}
.composer-menu label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
}
.composer-menu label input {
  background: var(--input);
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 5px;
}
.more-menu {
  min-width: 164px;
  max-width: 190px;
  background: #e1e1e6;
  padding: 4px;
}
.more-menu > button {
  font-size: 12px;
  min-height: 28px;
  padding: 5px 7px;
  gap: 7px;
}
.more-menu hr {
  border: 0;
  border-top: 1px solid #ffffff60;
  margin: 3px -4px;
}
.task-relation-backdrop {
  position: absolute;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: 32px;
}
.task-relation-picker {
  width: 386px;
  max-width: calc(100vw - 40px);
  background: #e1e1e6;
  border: 1px solid #ffffff70;
  border-radius: 14px;
  padding: 8px;
  box-shadow: 0 12px 36px #0002;
}
.task-relation-picker header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.task-relation-picker h2 {
  font-size: 16px;
  margin: 0;
}
.task-relation-picker > p {
  font-size: 12px;
  color: var(--muted);
  margin: 8px 0 15px;
}
.relation-search {
  display: flex;
  align-items: center;
  gap: 7px;
  background: #ffffff12;
  border: 1px solid #ffffff30;
  border-radius: 7px;
  padding: 7px 9px;
}
.relation-search input {
  background: none;
  border: 0;
  outline: 0;
  font-size: 14px;
  width: 100%;
}
.relation-results [role='listbox'] > button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 9px 8px;
  font-size: 14px;
  border-radius: 5px;
  text-align: left;
}
.relation-results [role='listbox'] > button:hover,
.relation-results [role='listbox'] > button:focus-visible {
  background: #ffffff50;
}
.relation-results {
  position: static;
  padding: 0;
  min-width: 100%;
  max-width: none;
  background: transparent;
  border: 0;
  box-shadow: none;
}
.relation-results small {
  margin: 0;
  white-space: nowrap;
}
.relation-results span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.properties-menu-anchor {
  position: absolute;
  bottom: 8px;
  left: 16px;
}
.stage-menu {
  bottom: calc(100% + 6px);
  top: auto;
}
.expanded .composer-body {
  min-height: 0;
}
.expanded .composer-description,
.expanded .composer-prompt {
  min-height: 0;
}
@media (max-width: 650px) {
  .composer-footer {
    gap: 5px;
    padding: 12px;
  }
  .composer-footer kbd {
    display: none;
  }
  .mode-switch {
    font-size: 11px;
    padding: 4px;
  }
  .another-switch {
    font-size: 11px;
  }
}
</style>
