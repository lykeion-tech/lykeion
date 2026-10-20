<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useAgents, useMe, useMembers, useWorkspace } from '../api/queries';
import type { ResearchDetail, Research } from '../api/types';
import Icon from './Icon.vue';
import TaskBoard from '../views/TaskBoard.vue';
import { useTaskComposer } from '../state/task-composer';
const props = defineProps<{ project: ResearchDetail }>();
defineEmits<{ edit: [] }>();
const client = useQueryClient(),
  workspace = useWorkspace(),
  members = useMembers(),
  agents = useAgents(),
  me = useMe();
const { openComposer } = useTaskComposer();
const emptyTab = ref('All');
const descriptionConflict = ref(false);
const rail = ref(true),
  properties = ref(true),
  progress = ref(true),
  descriptionOpen = ref(true);
const draft = ref(props.project.description),
  busy = ref(false),
  error = ref('');
let visit = 0;
watch([workspaceId, () => props.project.id], () => {
  visit++;
  error.value = '';
  busy.value = false;
  draft.value = props.project.description;
  descriptionConflict.value = false;
});
watch(
  () => props.project.description,
  (value, previous) => {
    if (draft.value === previous || draft.value === value) {
      draft.value = value;
      descriptionConflict.value = false;
    } else descriptionConflict.value = true;
  },
);
const done = computed(() => props.project.studies.filter((s) => s.board_column === 'done').length);
const usableAgents = computed(() =>
  (agents.data.value ?? []).filter(
    (a) => !a.archived_at && (a.visibility === 'workspace' || a.owner_id === me.data.value?.id),
  ),
);
let lastUpdate: Partial<Research> = {};
const currentLead = computed(() =>
  props.project.lead_type && props.project.lead_id
    ? `${props.project.lead_type}:${props.project.lead_id}`
    : '',
);
const leadAvailable = computed(
  () =>
    !currentLead.value ||
    (props.project.lead_type === 'member'
      ? members.data.value?.some((m) => m.user_id === props.project.lead_id)
      : usableAgents.value.some((a) => a.id === props.project.lead_id)),
);
const statuses = [
  { value: 'planned', label: 'Planned' },
  { value: 'in_progress', label: 'In Progress' },
  { value: 'paused', label: 'Paused' },
  { value: 'completed', label: 'Completed' },
  { value: 'cancelled', label: 'Cancelled' },
];
const priorities = [
  { value: 'none', label: 'No priority' },
  { value: 'urgent', label: 'Urgent' },
  { value: 'high', label: 'High' },
  { value: 'medium', label: 'Medium' },
  { value: 'low', label: 'Low' },
];
async function update(fields: Partial<Research>) {
  if (busy.value) return;
  const ws = workspaceId.value,
    id = props.project.id,
    ownVisit = visit;
  lastUpdate = fields;
  busy.value = true;
  error.value = '';
  try {
    await api('PATCH', `/api/researches/${id}`, fields, undefined, { workspace: ws });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.researches(ws) }),
      client.invalidateQueries({ queryKey: keys.research(ws, id) }),
    ]);
  } catch (e) {
    if (visit === ownVisit) error.value = (e as Error).message;
  } finally {
    if (visit === ownVisit) busy.value = false;
  }
}
function pickLead(event: Event) {
  const value = (event.target as HTMLSelectElement).value;
  const [type, id] = value.split(':');
  void update({ lead_type: type ? (type as 'member' | 'agent') : null, lead_id: id ?? null });
}
</script>
<template>
  <div class="project-detail-layout">
    <section class="project-main">
      <header class="project-heading">
        <nav aria-label="Project breadcrumb">
          <RouterLink to="/projects">{{ workspace.data.value?.name }}</RouterLink
          ><Icon name="right" :size="12" /><span>{{ project.name }}</span>
        </nav>
        <button
          class="icon-button"
          :aria-pressed="rail"
          aria-label="Toggle project properties"
          @click="rail = !rail"
        >
          <Icon name="panel" :size="17" />
        </button>
      </header>
      <div class="project-board">
        <TaskBoard v-if="project.studies.length" :key="project.id" :project-id="project.id" />
        <template v-else>
          <div class="project-empty-toolbar">
            <button
              v-for="tab in ['All', 'Members', 'Agents']"
              :key="tab"
              class="button compact"
              :class="{ active: emptyTab === tab }"
              :aria-pressed="emptyTab === tab"
              @click="emptyTab = tab"
            >
              {{ tab }}
            </button>
          </div>
          <div class="project-empty">
            <Icon name="tasks" :size="36" />
            <h2>No studies linked</h2>
            <p>Create a new study or assign existing ones to this project.</p>
            <button class="button compact" @click="openComposer(undefined, undefined, project.id)">
              <Icon name="plus" :size="14" />New study
            </button>
          </div>
        </template>
      </div>
    </section>
    <aside v-if="rail" class="project-properties-rail" aria-label="Project properties">
      <span class="project-detail-folder" aria-hidden="true">📁</span>
      <div class="project-title-row">
        <h1>{{ project.name }}</h1>
        <button class="icon-button" aria-label="Edit Project" @click="$emit('edit')">
          <Icon name="edit" :size="14" />
        </button>
      </div>
      <section>
        <h2>
          <button :aria-expanded="properties" @click="properties = !properties">
            Properties<Icon :name="properties ? 'down' : 'right'" :size="11" />
          </button>
        </h2>
        <div v-if="properties" class="project-property-rows">
          <label
            >Status<select
              aria-label="Project status"
              :value="project.status"
              :disabled="busy"
              @change="
                update({ status: ($event.target as HTMLSelectElement).value as Research['status'] })
              "
            >
              <option v-for="s in statuses" :key="s.value" :value="s.value">{{ s.label }}</option>
            </select></label
          >
          <label
            >Priority<select
              aria-label="Project priority"
              :value="project.priority"
              :disabled="busy"
              @change="
                update({
                  priority: ($event.target as HTMLSelectElement).value as Research['priority'],
                })
              "
            >
              <option v-for="p in priorities" :key="p.value" :value="p.value">{{ p.label }}</option>
            </select></label
          >
          <label
            >Lead<select
              aria-label="Project lead"
              :value="currentLead"
              :disabled="busy"
              @change="pickLead"
            >
              <option value="">No lead</option>
              <option v-if="!leadAvailable" :value="currentLead" disabled>Unavailable lead</option>
              <optgroup label="Members">
                <option
                  v-for="m in members.data.value ?? []"
                  :key="m.user_id"
                  :value="`member:${m.user_id}`"
                >
                  {{ m.name || m.email }}
                </option>
              </optgroup>
              <optgroup label="Agents">
                <option v-for="a in usableAgents" :key="a.id" :value="`agent:${a.id}`">
                  {{ a.name }}
                </option>
              </optgroup>
            </select></label
          >
        </div>
      </section>
      <section v-if="project.studies.length">
        <h2>
          <button :aria-expanded="progress" @click="progress = !progress">
            Progress<Icon :name="progress ? 'down' : 'right'" :size="11" />
          </button>
        </h2>
        <div v-if="progress" class="project-progress">
          <progress
            :max="project.studies.length"
            :value="done"
            aria-label="Completed studies"
          /><span>{{ done }}/{{ project.studies.length }}</span>
        </div>
      </section>
      <section>
        <h2>
          <button :aria-expanded="descriptionOpen" @click="descriptionOpen = !descriptionOpen">
            Description<Icon :name="descriptionOpen ? 'down' : 'right'" :size="11" />
          </button>
        </h2>
        <textarea
          v-if="descriptionOpen"
          v-model="draft"
          aria-label="Project description"
          placeholder="Add description..."
          :disabled="busy"
          @blur="
            !descriptionConflict && draft !== project.description && update({ description: draft })
          "
        />
      </section>
      <div v-if="descriptionConflict" class="description-conflict" role="status">
        <p>Description changed elsewhere. Your draft has been kept.</p>
        <button
          class="text-button"
          @click="
            descriptionConflict = false;
            update({ description: draft });
          "
        >
          Save my description
        </button>
        <button
          class="text-button"
          @click="
            draft = project.description;
            descriptionConflict = false;
          "
        >
          Use saved description
        </button>
      </div>
      <p v-if="error" class="inline-error" role="alert">
        {{ error }}<button class="text-button" @click="update(lastUpdate)">Retry saving</button>
      </p>
    </aside>
  </div>
</template>
<style scoped>
.project-detail-layout {
  display: flex;
  flex: 1;
  min-height: 0;
  height: 100%;
}
.project-main {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
}
.project-heading {
  height: 48px;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  border-bottom: 1px solid var(--border);
  gap: 12px;
}
.project-heading nav {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-size: 14px;
}
.project-heading nav a {
  color: var(--muted);
  flex: none;
}
.project-heading nav span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.project-board {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.project-properties-rail {
  width: 420px;
  flex: none;
  border-left: 1px solid var(--border);
  padding: 20px 16px;
  overflow: auto;
}
.project-detail-folder {
  font-size: 24px;
  display: block;
  margin-bottom: 16px;
}
.project-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 20px;
}
.project-title-row h1 {
  font-size: 16px;
  line-height: 24px;
  font-weight: 600;
  overflow-wrap: anywhere;
  flex: 1;
}
.project-properties-rail section {
  margin-bottom: 24px;
}
.project-properties-rail h2 {
  margin: 0 0 12px;
  font-size: 12px;
  font-weight: 500;
}
.project-properties-rail h2 button {
  display: flex;
  align-items: center;
  gap: 4px;
  background: none;
  border: 0;
  padding: 3px 8px;
  color: var(--text);
  font: inherit;
}
.project-property-rows {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 0 8px;
}
.project-property-rows label {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  align-items: center;
  min-height: 30px;
  font-size: 12px;
  color: var(--muted);
}
.project-property-rows select {
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--text);
  padding: 5px 0;
  min-width: 0;
  font: inherit;
  cursor: pointer;
}
.project-properties-rail textarea {
  border: 0;
  background: none;
  padding: 0 8px;
  width: 100%;
  min-height: 70px;
  resize: vertical;
  font: inherit;
  font-size: 14px;
  line-height: 22px;
  color: var(--text);
  box-shadow: none;
  outline: none;
}
.project-properties-rail textarea::placeholder {
  color: var(--muted);
}
.project-progress {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 8px;
  color: var(--muted);
  font-size: 12px;
}
.project-progress progress {
  flex: 1;
  width: 0;
  height: 8px;
  appearance: none;
  border: 0;
  border-radius: 8px;
  background: var(--fill-card);
  overflow: hidden;
}
.project-progress progress::-webkit-progress-bar {
  background: var(--fill-card);
  border-radius: 8px;
}
.project-progress progress::-webkit-progress-value {
  background: var(--stage-reporting-dot);
  border-radius: 8px;
}
.project-empty-toolbar {
  height: 56px;
  display: flex;
  align-items: center;
  padding: 0 16px;
  gap: 6px;
  font-size: 13px;
}
.project-empty-toolbar > span {
  padding: 5px 10px;
}
.project-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  color: var(--muted);
  padding: 32px;
  text-align: center;
}
.project-empty h2 {
  font-size: 14px;
  font-weight: 400;
  margin: 0;
}
.project-empty p {
  font-size: 12px;
  margin: 0;
}
@media (max-width: 1200px) {
  .project-properties-rail {
    width: 320px;
  }
}
@media (max-width: 760px) {
  .project-properties-rail {
    width: 260px;
  }
  .project-heading nav {
    font-size: 12px;
  }
}
@media (max-width: 600px) {
  .project-detail-layout {
    flex-direction: column;
    overflow: auto;
  }
  .project-main {
    min-height: 500px;
    flex: none;
  }
  .project-properties-rail {
    width: 100%;
    border-left: 0;
    border-top: 1px solid var(--border);
  }
}
</style>
<style>
.project-board .task-toolbar {
  height: 56px;
  padding: 10px 16px;
}
.project-board .working-control {
  display: none;
}
.project-board .toolbar-group.push-right > .menu-anchor > .button,
.project-board .toolbar-group.push-right > .button {
  font-size: 0;
  width: 28px;
  height: 28px;
  padding: 0;
  gap: 0;
}
.project-board .board-scroll {
  padding: 8px 16px 24px;
}
</style>
