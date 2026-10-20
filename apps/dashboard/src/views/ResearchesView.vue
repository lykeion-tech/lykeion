<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import {
  useAgents,
  useMe,
  useMembers,
  useResearch,
  useResearches,
  useWorkspace,
} from '../api/queries';
import type { Research } from '../api/types';
import Modal from '../components/Modal.vue';
import Icon from '../components/Icon.vue';
const props = defineProps<{ id?: string }>();
const list = useResearches(),
  detail = useResearch(() => props.id ?? '');
const client = useQueryClient(),
  router = useRouter();
const workspace = useWorkspace();
const members = useMembers(),
  agents = useAgents(),
  me = useMe();
const statuses: { value: Research['status']; label: string }[] = [
  { value: 'planned', label: 'Planned' },
  { value: 'in_progress', label: 'In Progress' },
  { value: 'paused', label: 'Paused' },
  { value: 'completed', label: 'Completed' },
  { value: 'cancelled', label: 'Cancelled' },
];
const priorities: { value: Research['priority']; label: string; bars: number }[] = [
  { value: 'urgent', label: 'Urgent', bars: 4 },
  { value: 'high', label: 'High', bars: 3 },
  { value: 'medium', label: 'Medium', bars: 2 },
  { value: 'low', label: 'Low', bars: 1 },
  { value: 'none', label: 'No priority', bars: 0 },
];
const status = ref<Research['status']>('planned'),
  priority = ref<Research['priority']>('none'),
  leadType = ref<Research['lead_type']>(null),
  leadId = ref<string | null>(null),
  picker = ref<'status' | 'priority' | 'lead' | null>(null),
  leadFilter = ref('');
const statusLabel = computed(() => statuses.find((s) => s.value === status.value)!.label);
const priorityOption = computed(() => priorities.find((p) => p.value === priority.value)!);
const leadLabel = computed(() =>
  leadType.value === 'member'
    ? (members.data.value?.find((m) => m.user_id === leadId.value)?.name ?? 'Unavailable member')
    : leadType.value === 'agent'
      ? (agents.data.value?.find((a) => a.id === leadId.value)?.name ?? 'Unavailable agent')
      : 'Lead',
);
const filteredMembers = computed(() =>
  (members.data.value ?? []).filter((m) =>
    m.name.toLowerCase().includes(leadFilter.value.toLowerCase()),
  ),
);
const filteredAgents = computed(() =>
  (agents.data.value ?? []).filter(
    (a) =>
      !a.archived_at &&
      (a.visibility === 'workspace' || a.owner_id === me.data.value?.id) &&
      a.name.toLowerCase().includes(leadFilter.value.toLowerCase()),
  ),
);
let pickerTrigger: HTMLButtonElement | null = null;
async function togglePicker(value: NonNullable<typeof picker.value>, event: MouseEvent) {
  if (picker.value === value) return closePicker();
  pickerTrigger = event.currentTarget as HTMLButtonElement;
  picker.value = value;
  leadFilter.value = '';
  await nextTick();
  document
    .querySelector<HTMLElement>('.project-picker-menu input, .project-picker-menu button')
    ?.focus();
}
function closePicker() {
  picker.value = null;
  leadFilter.value = '';
  pickerTrigger?.focus();
}
function selectLead(type: Research['lead_type'], id: string | null) {
  leadType.value = type;
  leadId.value = id;
  closePicker();
}
function pickerKey(event: KeyboardEvent) {
  if (!picker.value) return;
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    closePicker();
  } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    const items = [...document.querySelectorAll<HTMLButtonElement>('.project-picker-menu button')];
    if (!items.length) return;
    event.preventDefault();
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : index < 0
            ? event.key === 'ArrowDown'
              ? 0
              : items.length - 1
            : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
    items[next]?.focus();
  }
}
function leavePicker(event: FocusEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null))
    picker.value = null;
}
function outsidePicker(event: PointerEvent) {
  if (picker.value && !(event.target as HTMLElement).closest('.project-picker'))
    picker.value = null;
}
onMounted(() => document.addEventListener('pointerdown', outsidePicker));
onUnmounted(() => document.removeEventListener('pointerdown', outsidePicker));
const expanded = ref(false);
const editor = ref(false),
  name = ref(''),
  description = ref(''),
  busy = ref(false),
  error = ref('');
let visit = 0;
watch(
  [workspaceId, () => props.id],
  () => {
    visit++;
    editor.value = false;
    picker.value = null;
    error.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
onUnmounted(() => visit++);
const failure = computed(() => (props.id ? detail.error.value : list.error.value));
function edit() {
  name.value = detail.data.value?.name ?? '';
  description.value = detail.data.value?.description ?? '';
  status.value = detail.data.value?.status ?? 'planned';
  priority.value = detail.data.value?.priority ?? 'none';
  leadType.value = detail.data.value?.lead_type ?? null;
  leadId.value = detail.data.value?.lead_id ?? null;
  picker.value = null;
  error.value = '';
  expanded.value = false;
  editor.value = true;
}
async function save() {
  const ws = workspaceId.value,
    id = props.id,
    v = visit;
  busy.value = true;
  error.value = '';
  try {
    const saved = await api<Research>(
      id ? 'PATCH' : 'POST',
      id ? `/api/researches/${id}` : '/api/researches',
      {
        name: name.value,
        description: description.value,
        status: status.value,
        priority: priority.value,
        lead_type: leadType.value,
        lead_id: leadId.value,
      },
    );
    if (v !== visit || ws !== workspaceId.value) return;
    editor.value = false;
    await client.invalidateQueries({ queryKey: keys.researches(ws) });
    if (v !== visit || ws !== workspaceId.value) return;
    if (id) await client.invalidateQueries({ queryKey: keys.research(ws, id) });
    else await router.push('/projects/' + saved.id);
  } catch (e) {
    if (v === visit) error.value = (e as Error).message;
  } finally {
    if (v === visit) busy.value = false;
  }
}
</script>
<template>
  <section class="directory-page">
    <template v-if="id"
      ><RouterLink to="/projects" class="text-button">← Projects</RouterLink>
      <div v-if="detail.data.value" class="research-detail">
        <header>
          <h1>{{ detail.data.value.name }}</h1>
          <button class="button" @click="edit">Edit Project</button>
        </header>
        <p class="description">{{ detail.data.value.description || 'No description.' }}</p>
        <h2>Studies</h2>
        <div class="research-studies">
          <RouterLink
            v-for="study in detail.data.value.studies"
            :key="study.id"
            :to="'/studies/' + study.id"
            ><Icon name="tasks" /><span
              >{{ study.title
              }}<small>{{ study.board_column }} · round {{ study.round }}</small></span
            ><Icon name="right"
          /></RouterLink>
          <p v-if="!detail.data.value.studies.length" class="muted">
            No studies linked yet. Choose this Project in a study’s properties.
          </p>
        </div>
      </div></template
    ><template v-else
      ><Teleport to="#page-header-actions">
        <button class="button primary" @click="edit">
          <Icon name="plus" :size="14" />New Project
        </button>
      </Teleport>
      <div class="directory-list">
        <RouterLink v-for="r in list.data.value ?? []" :key="r.id" :to="'/projects/' + r.id"
          ><Icon name="folder" /><span
            >{{ r.name }}<small>{{ r.description || 'No description' }}</small></span
          ><Icon name="right"
        /></RouterLink>
      </div>
      <div v-if="list.isSuccess.value && !list.data.value?.length" class="directory-empty">
        <Icon name="folder" :size="30" />
        <h2>No Projects yet</h2>
        <p>Group related studies in a Project.</p>
        <button class="button" @click="edit">Create Project</button>
      </div></template
    >
    <p v-if="failure" class="inline-error" role="alert">
      {{ failure.message }} <button @click="id ? detail.refetch() : list.refetch()">Retry</button>
    </p>
    <p v-if="id ? detail.isPending.value : list.isPending.value" role="status">Loading Projects…</p>
    <Modal
      v-if="editor"
      :title="id ? 'Edit Project' : 'New Project'"
      class="project-editor"
      :class="{ expanded }"
      @close="editor = false"
    >
      <form class="project-create" @submit.prevent="save" @keydown.capture="pickerKey">
        <header class="project-breadcrumb">
          <span class="project-workspace">{{ workspace.data.value?.name }}</span>
          <Icon name="right" :size="12" />
          <strong>{{ id ? 'Edit project' : 'New project' }}</strong>
          <button
            type="button"
            class="icon-button push-right"
            :aria-label="expanded ? 'Collapse project editor' : 'Expand project editor'"
            :aria-pressed="expanded"
            @click="expanded = !expanded"
          >
            <Icon name="expand" :size="15" />
          </button>
          <button type="button" class="icon-button" aria-label="Close" @click="editor = false">
            <Icon name="close" :size="15" />
          </button>
        </header>
        <div class="project-create-body">
          <span class="project-folder" aria-hidden="true">📁</span>
          <input
            v-model="name"
            class="project-title"
            placeholder="Project title"
            aria-label="Project name"
            required
            autofocus
          />
          <textarea
            v-model="description"
            placeholder="Add description..."
            aria-label="Project description"
          />
        </div>
        <p v-if="error" class="inline-error project-error" role="alert">{{ error }}</p>
        <footer class="project-create-footer">
          <div class="project-properties" aria-label="Project properties">
            <div class="project-picker" @focusout="leavePicker">
              <button
                type="button"
                class="project-property"
                aria-haspopup="menu"
                :aria-expanded="picker === 'status'"
                @click="togglePicker('status', $event)"
              >
                <span class="project-status-dot" :class="status" aria-hidden="true" />{{
                  statusLabel
                }}
              </button>
              <div
                v-if="picker === 'status'"
                class="project-picker-menu"
                role="menu"
                aria-label="Project status"
              >
                <button
                  v-for="option in statuses"
                  :key="option.value"
                  type="button"
                  role="menuitemradio"
                  :aria-checked="status === option.value"
                  @click="
                    status = option.value;
                    closePicker();
                  "
                >
                  <span class="project-status-dot" :class="option.value" aria-hidden="true" />{{
                    option.label
                  }}
                </button>
              </div>
            </div>
            <div class="project-picker" @focusout="leavePicker">
              <button
                type="button"
                class="project-property"
                aria-haspopup="menu"
                :aria-expanded="picker === 'priority'"
                @click="togglePicker('priority', $event)"
              >
                <span class="project-priority-mark" :class="priority" aria-hidden="true"
                  ><template v-if="priorityOption.bars"
                    ><i
                      v-for="n in 4"
                      :key="n"
                      :class="{ filled: n <= priorityOption.bars }"
                      :style="{ height: `${3 + n * 2}px` }" /></template
                  ><template v-else>—</template></span
                >{{ priorityOption.label }}
              </button>
              <div
                v-if="picker === 'priority'"
                class="project-picker-menu"
                role="menu"
                aria-label="Project priority"
              >
                <button
                  v-for="option in priorities"
                  :key="option.value"
                  type="button"
                  role="menuitemradio"
                  :aria-checked="priority === option.value"
                  @click="
                    priority = option.value;
                    closePicker();
                  "
                >
                  <span class="project-priority-mark" :class="option.value" aria-hidden="true"
                    ><template v-if="option.bars"
                      ><i
                        v-for="n in 4"
                        :key="n"
                        :class="{ filled: n <= option.bars }"
                        :style="{ height: `${3 + n * 2}px` }" /></template
                    ><template v-else>—</template></span
                  >{{ option.label }}
                </button>
              </div>
            </div>
            <div class="project-picker" @focusout="leavePicker">
              <button
                type="button"
                class="project-property project-lead"
                aria-haspopup="menu"
                :aria-expanded="picker === 'lead'"
                @click="togglePicker('lead', $event)"
              >
                <span>{{ leadLabel }}</span>
              </button>
              <div v-if="picker === 'lead'" class="project-picker-menu project-lead-menu">
                <input
                  v-model="leadFilter"
                  aria-label="Search project leads"
                  placeholder="Search members and agents…"
                  @keydown.enter.prevent
                />
                <div role="menu" aria-label="Project lead" class="project-lead-options">
                  <button
                    type="button"
                    role="menuitemradio"
                    :aria-checked="!leadType"
                    @click="selectLead(null, null)"
                  >
                    No lead
                  </button>
                  <p v-if="filteredMembers.length" class="project-picker-label">Members</p>
                  <button
                    v-for="member in filteredMembers"
                    :key="member.user_id"
                    type="button"
                    role="menuitemradio"
                    :aria-checked="leadType === 'member' && leadId === member.user_id"
                    @click="selectLead('member', member.user_id)"
                  >
                    {{ member.name }}
                  </button>
                  <p v-if="filteredAgents.length" class="project-picker-label">Agents</p>
                  <button
                    v-for="agent in filteredAgents"
                    :key="agent.id"
                    type="button"
                    role="menuitemradio"
                    :aria-checked="leadType === 'agent' && leadId === agent.id"
                    @click="selectLead('agent', agent.id)"
                  >
                    {{ agent.name }}
                  </button>
                  <p
                    v-if="members.isPending.value || agents.isPending.value"
                    role="status"
                    class="project-picker-label"
                  >
                    Loading leads…
                  </p>
                  <p
                    v-if="members.error.value || agents.error.value"
                    role="alert"
                    class="project-picker-label"
                  >
                    Could not load all leads.
                    <button
                      type="button"
                      @click="
                        members.refetch();
                        agents.refetch();
                      "
                    >
                      Retry
                    </button>
                  </p>
                  <p
                    v-if="leadFilter && !filteredMembers.length && !filteredAgents.length"
                    class="project-picker-label"
                  >
                    No results
                  </p>
                </div>
              </div>
            </div>
          </div>
          <button class="button primary compact" :disabled="busy || !name.trim()">
            {{ busy ? 'Saving…' : id ? 'Save Project' : 'Create Project' }}
          </button>
        </footer>
      </form>
    </Modal>
  </section>
</template>
<style src="./directory.css" />

<style scoped>
.project-editor {
  width: 676px;
  max-width: calc(100vw - 48px);
  background: var(--fill-menu);
}
.project-create {
  display: flex;
  flex-direction: column;
  min-height: 384px;
}
.project-breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 50px;
  flex: none;
  padding: 12px 20px;
  font-size: 12px;
}
.project-workspace {
  min-width: 0;
  max-width: 45%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
}
.project-breadcrumb > .icon {
  color: var(--icon-dim);
}
.project-breadcrumb strong {
  font-weight: 400;
  white-space: nowrap;
}
.project-breadcrumb .icon-button {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
}
.project-create-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1;
  min-height: 0;
  padding: 0 20px 20px;
}
.project-folder {
  font-size: 24px;
  line-height: 30px;
}
.project-create-body input,
.project-create-body textarea {
  width: 100%;
  padding: 0;
  border: 0;
  background: none;
  border-radius: 0;
  color: var(--text);
  outline: none;
}
.project-create-body input:focus-visible,
.project-create-body textarea:focus-visible {
  box-shadow: none;
}
.project-title {
  font-size: 18px;
  font-weight: 500;
  line-height: 25px;
}
.project-create-body textarea {
  flex: 1;
  min-height: 120px;
  font-size: 14px;
  line-height: 23px;
  resize: none;
}
.project-create-body input::placeholder,
.project-create-body textarea::placeholder {
  color: var(--muted);
  opacity: 1;
}
.project-create-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  flex: none;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
}
.project-properties {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.project-property {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 26px;
  padding: 3px 10px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: none;
  color: var(--text);
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
}
.project-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--icon-faint);
}
.project-lead {
  color: var(--muted);
}
.project-create-footer > .button {
  margin-left: auto;
}
.project-error {
  padding: 0 20px 10px;
}
.project-editor.expanded {
  width: min(1100px, calc(100vw - 64px));
}
.project-editor.expanded .project-create {
  height: min(740px, 85vh);
  min-height: 0;
}
.project-picker {
  position: relative;
  max-width: 100%;
}
.project-property:hover,
.project-picker-menu button:hover {
  background: var(--fill-highlight);
}
.project-lead span {
  max-width: 145px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.project-picker-menu {
  position: absolute;
  left: 0;
  bottom: calc(100% + 8px);
  z-index: 2;
  width: 176px;
  max-width: calc(100vw - 80px);
  padding: 4px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--fill-menu);
  box-shadow: 0 8px 24px #0002;
}
.project-picker-menu button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 30px;
  padding: 5px 8px;
  border: 0;
  border-radius: 4px;
  background: none;
  text-align: left;
  font-size: 12px;
  color: var(--text);
}
.project-picker-menu button[aria-checked='true'] {
  background: var(--fill-accent-soft);
}
.project-lead-menu {
  width: 228px;
  left: auto;
  right: 0;
}
.project-lead-menu input {
  width: 100%;
  border: 0;
  border-bottom: 1px solid var(--border);
  border-radius: 0;
  padding: 7px 8px;
  background: none;
  color: var(--text);
  font-size: 12px;
}
.project-lead-options {
  max-height: 240px;
  overflow-y: auto;
}
.project-picker-label {
  margin: 6px 8px 3px;
  color: var(--muted);
  font-size: 11px;
}
.project-status-dot.in_progress {
  background: var(--stage-experimentation-dot);
}
.project-status-dot.completed {
  background: var(--stage-reporting-dot);
}
.project-status-dot.cancelled {
  background: var(--error);
}
.project-priority-mark {
  display: inline-flex;
  align-items: flex-end;
  gap: 2px;
  width: 14px;
  color: var(--muted);
}
.project-priority-mark i {
  width: 2px;
  background: currentColor;
  opacity: 0.2;
}
.project-priority-mark i.filled {
  opacity: 1;
}
.project-priority-mark.urgent {
  color: var(--error);
}
.project-priority-mark.high,
.project-priority-mark.medium {
  color: var(--stage-experimentation-dot);
}
.project-priority-mark.low {
  color: var(--stage-reporting-dot);
}
</style>
