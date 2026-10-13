<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import type { Inquiry, InquiryDetail, Stage } from '../../../../packages/contracts/src/research';
import { stages } from '../../../../packages/contracts/src/research';
import { service, state, refresh, notify } from '../services/workspace';
import Icon from '../components/Icon.vue';
import TaskAuxiliary from '../components/TaskAuxiliary.vue';
const props = defineProps<{ id: string }>();
const emit = defineEmits<{ close: [] }>();
const route = useRoute();
const router = useRouter();
const preview = computed(() => route.query.preview === '1');
const detail = ref<InquiryDetail>();
const error = ref('');
const comment = ref('');
const busy = ref(false);
const saving = ref(false);
const editing = ref(false);
const title = ref('');
const description = ref('');
const showProperties = ref(true);
const showPriority = ref(false);
const propertyMenu = ref(false),
  shownFields = ref<string[]>([]);
const optionalFields = [
  { name: 'Priority', icon: 'chart' },
  { name: 'Start date', icon: 'calendar' },
  { name: 'Due date', icon: 'calendar' },
  { name: 'Labels', icon: 'tag' },
];
const labelText = computed(() => (detail.value?.inquiry.labels ?? []).join(', '));
function togglePropertyMenu() {
  propertyMenu.value = !propertyMenu.value;
  if (propertyMenu.value)
    void nextTick(() =>
      document.querySelector<HTMLButtonElement>('.property-add-menu button')?.focus(),
    );
}
function addField(name: string) {
  shownFields.value.push(name);
  if (name === 'Priority') showPriority.value = true;
  propertyMenu.value = false;
  void nextTick(() =>
    document
      .querySelector<HTMLInputElement | HTMLSelectElement>('[aria-label="' + name + '"]')
      ?.focus(),
  );
}
function propertyKeys(event: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const buttons = [
    ...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('button'),
  ];
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
  const index =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? buttons.length - 1
        : (current + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
  buttons[index]?.focus();
}
function dismissProperty(event: PointerEvent) {
  if (!(event.target as HTMLElement).closest('.property-menu-anchor')) propertyMenu.value = false;
}
const paneWidth = ref(
  Math.max(260, Math.min(480, Number(localStorage.getItem('lykeion.detail-width')) || 364)),
);
type InspectorTab = 'Files' | 'Notebook' | 'Compute';
const inspectorOpen = ref(false);
const inspectorFocused = ref(false);
const inspectorTabs = ref<InspectorTab[]>(['Files', 'Notebook']);
const inspectorActive = ref<InspectorTab>('Files');
const inspectorWidth = ref(
  Math.max(280, Math.min(720, Number(localStorage.getItem('lykeion.task-inspector-width')) || 364)),
);
function toggleInspector() {
  if (!inspectorOpen.value && !inspectorTabs.value.length) {
    inspectorTabs.value = ['Files', 'Notebook'];
    inspectorActive.value = 'Files';
  }
  inspectorOpen.value = !inspectorOpen.value;
  if (!inspectorOpen.value) inspectorFocused.value = false;
}
function closeInspector() {
  inspectorOpen.value = false;
  inspectorFocused.value = false;
  void nextTick(() => document.getElementById('open-task-inspector')?.focus());
}
function closeInspectorTab(tab: InspectorTab) {
  const index = inspectorTabs.value.indexOf(tab);
  inspectorTabs.value.splice(index, 1);
  if (!inspectorTabs.value.length) {
    closeInspector();
    return;
  }
  if (inspectorActive.value === tab)
    inspectorActive.value = inspectorTabs.value[Math.max(0, index - 1)]!;
}
function openCompute() {
  if (!inspectorTabs.value.includes('Compute'))
    inspectorTabs.value.splice(Math.min(1, inspectorTabs.value.length), 0, 'Compute');
  inspectorActive.value = 'Compute';
}
function inspectorKey(event: KeyboardEvent, tab: InspectorTab) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  let index = inspectorTabs.value.indexOf(tab);
  if (event.key === 'Home') index = 0;
  else if (event.key === 'End') index = inspectorTabs.value.length - 1;
  else
    index =
      (index + (event.key === 'ArrowRight' ? 1 : -1) + inspectorTabs.value.length) %
      inspectorTabs.value.length;
  inspectorActive.value = inspectorTabs.value[index]!;
  void nextTick(() => document.getElementById('inspector-tab-' + inspectorActive.value)?.focus());
}
function inspectorResize(event: PointerEvent) {
  const start = event.clientX;
  const width = inspectorWidth.value;
  const handle = event.currentTarget as HTMLElement;
  handle.setPointerCapture(event.pointerId);
  const move = (e: PointerEvent) => {
    inspectorWidth.value = Math.max(280, Math.min(720, width + start - e.clientX));
  };
  const finish = () => {
    handle.removeEventListener('pointermove', move);
    handle.removeEventListener('pointerup', finish);
    handle.removeEventListener('pointercancel', finish);
    localStorage.setItem('lykeion.task-inspector-width', String(inspectorWidth.value));
  };
  handle.addEventListener('pointermove', move);
  handle.addEventListener('pointerup', finish, { once: true });
  handle.addEventListener('pointercancel', finish, { once: true });
}
function inspectorAdjust(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
  event.preventDefault();
  inspectorWidth.value = Math.max(
    280,
    Math.min(720, inspectorWidth.value + (event.key === 'ArrowLeft' ? 16 : -16)),
  );
  localStorage.setItem('lykeion.task-inspector-width', String(inspectorWidth.value));
}
const pinned = ref(true);
const showPullRequests = ref(true);
let generation = 0;
const taskCode = computed(
  () =>
    `${state.data?.workspace.taskPrefix || 'LYK'}-${Math.max(0, state.data?.inquiries.findIndex((item) => item.id === props.id) ?? 0) + 1}`,
);
function readPrefs() {
  try {
    pinned.value =
      JSON.parse(localStorage.getItem('lykeion.preferences') || '{}').pinComments !== false;
  } catch {
    pinned.value = true;
  }
}
function readSettings() {
  try {
    const values = JSON.parse(localStorage.getItem('lykeion.settings-drafts') || '{}');
    showPullRequests.value = values[state.data?.workspace.id || 'local']?.code?.sidebar !== false;
  } catch {
    showPullRequests.value = true;
  }
}
function date(value: string) {
  return new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}
async function load() {
  const current = ++generation;
  try {
    const next = await service.value.detail(props.id);
    if (current !== generation) return;
    detail.value = next;
    title.value = next.inquiry.title;
    description.value = next.inquiry.description;
    error.value = '';
  } catch (e) {
    if (current === generation) error.value = (e as Error).message;
  }
}
async function update(
  patch: Partial<
    Pick<
      Inquiry,
      | 'title'
      | 'description'
      | 'priority'
      | 'projectId'
      | 'assigneeId'
      | 'startDate'
      | 'dueDate'
      | 'labels'
    >
  >,
) {
  if (!detail.value || saving.value) return;
  saving.value = true;
  try {
    await service.value.updateInquiry(props.id, detail.value.inquiry.revision, patch);
    editing.value = false;
    await load();
    await refresh();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    saving.value = false;
  }
}
function save() {
  if (!title.value.trim()) {
    error.value = 'Enter a task title.';
    return;
  }
  return update({ title: title.value.trim(), description: description.value });
}
async function send() {
  if (busy.value || !comment.value.trim()) return;
  busy.value = true;
  try {
    await service.value.comment(props.id, comment.value);
    comment.value = '';
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function transition(event: Event) {
  const element = event.target as HTMLSelectElement;
  const target = element.value as Stage;
  if (!detail.value) return;
  try {
    const result = await service.value.transition(props.id, detail.value.inquiry.revision, target);
    notify(result.accepted ? `Moved to ${target}` : result.reasons.join(' '));
    await load();
    await refresh();
  } catch (e) {
    error.value = (e as Error).message;
  }
  if (detail.value) element.value = detail.value.inquiry.stage;
}
function selected(event: Event) {
  return (event.target as HTMLSelectElement).value;
}
function persistWidth() {
  localStorage.setItem('lykeion.detail-width', String(paneWidth.value));
}
function resize(event: PointerEvent) {
  const start = event.clientX;
  const width = paneWidth.value;
  const handle = event.currentTarget as HTMLElement;
  handle.setPointerCapture(event.pointerId);
  const move = (e: PointerEvent) => {
    paneWidth.value = Math.max(260, Math.min(480, width + e.clientX - start));
  };
  const finish = () => {
    handle.removeEventListener('pointermove', move);
    handle.removeEventListener('pointerup', finish);
    handle.removeEventListener('pointercancel', finish);
    persistWidth();
  };
  handle.addEventListener('pointermove', move);
  handle.addEventListener('pointerup', finish, { once: true });
  handle.addEventListener('pointercancel', finish, { once: true });
}
function adjust(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
  event.preventDefault();
  paneWidth.value = Math.max(
    260,
    Math.min(480, paneWidth.value + (event.key === 'ArrowRight' ? 16 : -16)),
  );
  persistWidth();
}
function key(event: KeyboardEvent) {
  if (event.key === 'Escape' && propertyMenu.value) {
    propertyMenu.value = false;
    event.preventDefault();
    return;
  }
  if (
    event.defaultPrevented ||
    event.key !== 'Escape' ||
    editing.value ||
    document.querySelector('dialog[open]')
  )
    return;
  if (inspectorFocused.value) {
    inspectorFocused.value = false;
    event.preventDefault();
  } else if (inspectorOpen.value) {
    closeInspector();
    event.preventDefault();
  } else if (preview.value) emit('close');
}
function createSubtask() {
  if (detail.value)
    window.dispatchEvent(
      new CustomEvent('lykeion:create-task', { detail: { parentId: detail.value.inquiry.id } }),
    );
}
watch(
  () => props.id,
  () => {
    detail.value = undefined;
    editing.value = false;
    comment.value = '';
    load();
  },
);
watch(() => state.data?.workspace.id, readSettings);
onMounted(() => {
  document.addEventListener('pointerdown', dismissProperty);
  readPrefs();
  readSettings();
  load();
  window.addEventListener('lykeion:settings-drafts', readSettings);
  window.addEventListener('lykeion:preferences', readPrefs);
  window.addEventListener('keydown', key);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', dismissProperty);
  generation++;
  window.removeEventListener('lykeion:settings-drafts', readSettings);
  window.removeEventListener('lykeion:preferences', readPrefs);
  window.removeEventListener('keydown', key);
});
</script>
<template>
  <section
    class="task-detail desktop-task"
    :class="{
      'task-preview': preview,
      'comments-pinned': pinned,
      'has-inspector': inspectorOpen,
      'inspector-focused': inspectorFocused,
    }"
    :style="{ '--properties-width': paneWidth + 'px', '--inspector-width': inspectorWidth + 'px' }"
    aria-label="Task detail"
  >
    <aside v-if="showProperties" class="task-properties">
      <header>
        <h2>Properties</h2>
        <Icon name="down" :size="12" />
      </header>
      <template v-if="detail">
        <div class="property-rows">
          <label
            ><span>Status</span
            ><select
              :value="detail.inquiry.stage"
              aria-label="Scientific stage"
              @change="transition"
            >
              <option v-for="stage in stages" :key="stage">{{ stage }}</option>
            </select></label
          >
          <p v-if="detail.inquiry.status !== 'active'" class="state-note">
            {{ detail.inquiry.status === 'blocked' ? 'Blocked' : 'Done' }} ·
            {{ detail.inquiry.stage }}
          </p>
          <label
            ><span>Assignee</span
            ><select
              :value="detail.inquiry.assigneeId || ''"
              aria-label="Assignee"
              :disabled="saving"
              @change="update({ assigneeId: selected($event) || null })"
            >
              <option value="">Unassigned</option>
              <option v-for="item in state.data?.agents" :key="item.id" :value="item.id">
                {{ item.name }}
              </option>
            </select></label
          >
          <label
            ><span>Project</span
            ><select
              :value="detail.inquiry.projectId || ''"
              aria-label="Project"
              :disabled="saving"
              @change="update({ projectId: selected($event) || null })"
            >
              <option value="">No project</option>
              <option v-for="item in state.data?.projects" :key="item.id" :value="item.id">
                {{ item.name }}
              </option>
            </select></label
          >
          <label v-if="showPriority || detail.inquiry.priority !== 'none'"
            ><span>Priority</span
            ><select
              :value="detail.inquiry.priority"
              aria-label="Priority"
              :disabled="saving"
              @change="update({ priority: selected($event) as Inquiry['priority'] })"
            >
              <option value="none">No priority</option>
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="urgent">Urgent</option>
            </select></label
          >
          <label
            v-for="field in ['startDate', 'dueDate'] as const"
            v-show="
              detail.inquiry[field] ||
              shownFields.includes(field === 'startDate' ? 'Start date' : 'Due date')
            "
            :key="field"
            ><span>{{ field === 'startDate' ? 'Start date' : 'Due date' }}</span
            ><input
              type="date"
              :aria-label="field === 'startDate' ? 'Start date' : 'Due date'"
              :value="detail.inquiry[field] || ''"
              :disabled="saving"
              @change="update({ [field]: selected($event) || null })"
          /></label>
          <label v-if="detail.inquiry.labels?.length || shownFields.includes('Labels')"
            ><span>Labels</span
            ><input
              aria-label="Labels"
              :value="labelText"
              placeholder="Comma-separated labels"
              :disabled="saving"
              @change="
                update({
                  labels: [
                    ...new Set(
                      selected($event)
                        .split(',')
                        .map((x) => x.trim())
                        .filter(Boolean),
                    ),
                  ],
                })
              "
          /></label>
          <div class="property-menu-anchor">
            <button
              class="add-property"
              :aria-expanded="propertyMenu"
              aria-haspopup="menu"
              @click="togglePropertyMenu"
            >
              <Icon name="plus" :size="12" />Add property
            </button>
            <div v-if="propertyMenu" class="property-add-menu" role="menu" @keydown="propertyKeys">
              <button
                v-for="field in optionalFields"
                :key="field.name"
                role="menuitem"
                @click="addField(field.name)"
              >
                <Icon :name="field.icon" :size="14" />{{ field.name }}
              </button>
            </div>
          </div>
        </div>
        <TaskAuxiliary
          :detail="detail"
          :task-code="taskCode"
          :show-pull-requests="showPullRequests"
          @changed="load"
        />
        <details class="properties-section" open>
          <summary>Evidence<Icon name="down" :size="12" /></summary>
          <p>
            {{
              detail.claims.length
                ? `${detail.claims.length} validated claims in the ledger.`
                : 'No validated claims yet.'
            }}
          </p>
        </details>
        <details class="properties-section task-metadata" open>
          <summary>Details<Icon name="down" :size="12" /></summary>
          <p><span>Created by</span>{{ service.mode === 'local' ? 'You' : 'Workspace member' }}</p>
          <p><span>Created</span>{{ date(detail.inquiry.createdAt) }}</p>
          <p><span>Revision</span>{{ detail.inquiry.revision }}</p>
        </details>
      </template>
    </aside>
    <div
      v-if="showProperties"
      class="task-splitter"
      role="separator"
      tabindex="0"
      aria-label="Resize properties"
      aria-orientation="vertical"
      :aria-valuenow="paneWidth"
      :aria-valuemin="260"
      :aria-valuemax="480"
      @pointerdown="resize"
      @keydown="adjust"
    />
    <div class="task-main">
      <header class="task-heading">
        <button
          v-if="preview"
          class="icon-button"
          aria-label="Close preview"
          @click="emit('close')"
        >
          <Icon name="close" /></button
        ><button
          v-else
          class="icon-button back-task"
          aria-label="Back to tasks"
          @click="emit('close')"
        >
          <Icon name="back" /></button
        ><span>{{ taskCode }} {{ detail?.inquiry.title }}</span>
        <div class="push-right">
          <button
            v-if="preview"
            class="icon-button"
            aria-label="Open full page"
            @click="router.replace({ path: route.path, query: {} })"
          >
            <Icon name="expand" /></button
          ><button
            class="icon-button"
            :aria-pressed="showProperties"
            aria-label="Toggle properties"
            @click="showProperties = !showProperties"
          >
            <Icon name="sidebar" /></button
          ><button
            id="open-task-inspector"
            class="icon-button"
            :aria-expanded="inspectorOpen"
            aria-label="Open task inspector"
            @click="toggleInspector"
          >
            <Icon name="panel" />
          </button>
        </div>
      </header>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <div v-if="detail" class="transcript-scroll">
        <div class="transcript-content">
          <div class="task-description">
            <template v-if="editing"
              ><input v-model="title" class="task-title-input" aria-label="Task title" /><textarea
                v-model="description"
                class="task-description-input"
                aria-label="Description"
              />
              <div class="toolbar-group">
                <button class="button primary" :disabled="saving" @click="save">
                  {{ saving ? 'Saving…' : 'Save changes' }}</button
                ><button
                  class="button"
                  @click="
                    editing = false;
                    title = detail.inquiry.title;
                    description = detail.inquiry.description;
                  "
                >
                  Cancel
                </button>
              </div></template
            ><template v-else
              ><button class="task-title" @click="editing = true">{{ detail.inquiry.title }}</button
              ><button class="task-description-text" @click="editing = true">
                {{ detail.inquiry.description || 'Add description...' }}
              </button></template
            >
          </div>
          <div class="subtask-row">
            <button class="add-subtasks" @click="createSubtask">
              <Icon name="plus" :size="13" />Add sub-tasks
            </button>
          </div>
          <header class="transcript-heading">
            <h2>Transcript</h2>
            <span
              >{{ detail.activity.length }}
              {{ detail.activity.length === 1 ? 'entry' : 'entries' }}</span
            >
          </header>
          <div class="transcript-entry creation-entry">
            <span class="avatar">L</span
            ><span>{{
              service.mode === 'local' ? 'You saved this local draft' : 'Task created'
            }}</span
            ><time>{{ date(detail.inquiry.createdAt) }}</time>
          </div>
          <article v-for="entry in detail.activity" :key="entry.id" class="transcript-entry">
            <span class="avatar">{{ entry.kind === 'comment' ? 'Y' : 'L' }}</span>
            <div>
              <header>
                <strong>{{
                  entry.kind === 'comment'
                    ? service.mode === 'local'
                      ? 'You'
                      : 'Workspace comment'
                    : 'Lykeion'
                }}</strong
                ><time>{{
                  new Date(entry.createdAt).toLocaleTimeString([], {
                    hour: '2-digit',
                    minute: '2-digit',
                  })
                }}</time>
              </header>
              <p>{{ entry.text }}</p>
            </div>
          </article>
          <section v-if="detail.claims.length" class="task-claims">
            <h2>Claim ledger</h2>
            <article v-for="claim in detail.claims" :key="claim.id">
              <span class="pill">{{ claim.status }}</span>
              <p>{{ claim.text }}</p>
              <small>{{ claim.evidenceRefs.join(', ') }}</small>
            </article>
          </section>
          <form v-if="!pinned" class="task-comment" @submit.prevent="send">
            <textarea
              v-model="comment"
              placeholder="Leave a comment..."
              aria-label="Comment"
              @keydown.meta.enter.prevent="send"
              @keydown.ctrl.enter.prevent="send"
            />
            <footer>
              <small v-if="service.mode === 'local'">Local comment</small
              ><button
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
      </div>
      <div v-if="detail && pinned" class="pinned-composer">
        <form class="task-comment" @submit.prevent="send">
          <textarea
            v-model="comment"
            placeholder="Leave a comment..."
            aria-label="Comment"
            @keydown.meta.enter.prevent="send"
            @keydown.ctrl.enter.prevent="send"
          />
          <footer>
            <small v-if="service.mode === 'local'">Local comment</small
            ><button
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
      <p v-if="!detail && !error" class="detail-loading" role="status">Loading task…</p>
    </div>
    <div
      v-if="inspectorOpen && !inspectorFocused"
      class="inspector-splitter"
      role="separator"
      tabindex="0"
      aria-label="Resize task inspector"
      aria-orientation="vertical"
      :aria-valuenow="inspectorWidth"
      :aria-valuemin="280"
      :aria-valuemax="720"
      @pointerdown="inspectorResize"
      @keydown="inspectorAdjust"
    />
    <aside v-if="inspectorOpen" class="task-inspector" aria-label="Task inspector">
      <header class="inspector-toolbar">
        <div class="inspector-tabs" role="tablist" aria-label="Task inspector">
          <div
            v-for="tab in inspectorTabs"
            :key="tab"
            class="inspector-tab"
            :class="{ active: inspectorActive === tab }"
          >
            <button
              :id="'inspector-tab-' + tab"
              role="tab"
              :aria-selected="inspectorActive === tab"
              :tabindex="inspectorActive === tab ? 0 : -1"
              :aria-controls="'inspector-panel-' + tab"
              @click="inspectorActive = tab"
              @keydown="inspectorKey($event, tab)"
            >
              <Icon v-if="tab === 'Notebook'" name="book" :size="12" />{{ tab }}</button
            ><button
              class="inspector-tab-close"
              :aria-label="'Close inspector ' + tab"
              @click="closeInspectorTab(tab)"
            >
              <Icon name="close" :size="12" />
            </button>
          </div>
        </div>
        <button
          class="icon-button"
          :aria-label="inspectorFocused ? 'Restore task' : 'Focus inspector'"
          :aria-pressed="inspectorFocused"
          @click="inspectorFocused = !inspectorFocused"
        >
          <Icon name="expand" :size="15" /></button
        ><button class="icon-button" aria-label="Close inspector" @click="closeInspector">
          <Icon name="close" :size="16" />
        </button>
      </header>
      <section
        :id="'inspector-panel-' + inspectorActive"
        class="inspector-panel"
        role="tabpanel"
        :aria-labelledby="'inspector-tab-' + inspectorActive"
      >
        <template v-if="inspectorActive === 'Files'"
          ><header>
            <Icon name="file" :size="14" />
            <h2>Files</h2>
          </header>
          <p>
            {{
              service.mode === 'local'
                ? 'No files saved in this task yet.'
                : 'The task file service is not connected.'
            }}
          </p></template
        >
        <template v-else-if="inspectorActive === 'Notebook'"
          ><header>
            <Icon name="book" :size="14" />
            <h2>Notebook</h2>
          </header>
          <p>
            No cells yet. Connect a runtime to execute code; each task keeps its own notebook
            context.
          </p>
          <footer class="notebook-footer">
            <div class="notebook-environment">
              <label
                >Language
                <select aria-label="Language" disabled>
                  <option>Python</option>
                </select></label
              ><label
                >Environment
                <select aria-label="Environment" disabled>
                  <option>Not connected</option>
                </select></label
              >
            </div>
            <button class="notebook-link" @click="openCompute">Set up environment</button>
            <p>· view only — no kernel is connected</p>
            <div>
              <button class="notebook-link" @click="openCompute">Full details</button
              ><span>0 cells</span>
            </div>
          </footer></template
        >
        <template v-else
          ><header>
            <Icon name="monitor" :size="14" />
            <h2>Compute</h2>
            <small>Not connected</small>
          </header>
          <p class="compute-runtime">No runtime assigned</p>
          <div class="compute-stats">
            <div v-for="metric in ['Memory', 'Processor', 'Disk free']" :key="metric">
              <small>{{ metric }}</small
              ><span>—</span><i />
            </div>
          </div>
          <p class="compute-description">The Python kernel environment has not been set up.</p>
          <div class="kernel-section">
            <h3>Kernels</h3>
            <small>No running kernel</small
            ><button class="notebook-link" @click="router.push('/runtimes')">
              Configure a machine
            </button>
          </div></template
        >
      </section>
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
</style>
