<script setup lang="ts">
import SelectPicker from '../../ui/SelectPicker.vue';
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useAgents, useMe, useMembers } from '../../api/queries';
import type { ResearchDetail, Research } from '../../api/types';
import Icon from '../../ui/Icon.vue';
import { usePaneResize } from '../../lib/resize';
import { readText } from '../../storage';
import StudyBoard from '../studies/StudyBoard.vue';
import ProjectCoresearch from './ProjectCoresearch.vue';
const props = defineProps<{ project: ResearchDetail }>();
defineEmits<{ edit: [] }>();
const client = useQueryClient(),
  members = useMembers(),
  agents = useAgents(),
  me = useMe();
const inspector = usePaneResize('lykeion.project-inspector-width', 364, 240, 480, -1, () =>
  Math.max(160, window.innerWidth - (Number(readText('lykeion.sidebar-width')) || 224) - 400),
);
const projectTabs = ['Studies', 'Coresearches'] as const;
const selectedTab = ref<(typeof projectTabs)[number]>('Studies');
function switchProjectTab(event: KeyboardEvent) {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
  event.preventDefault();
  const next = selectedTab.value === 'Studies' ? 'Coresearches' : 'Studies';
  selectedTab.value = next;
  (event.currentTarget as HTMLElement)
    .querySelector<HTMLElement>(`[data-project-tab="${next}"]`)
    ?.focus();
}
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
  selectedTab.value = 'Studies';
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
  <div
    class="project-detail-layout"
    :style="{ '--project-properties-width': inspector.width.value + 'px' }"
  >
    <section class="project-main">
      <header class="project-heading">
        <nav aria-label="Project breadcrumb">
          <RouterLink to="/projects" class="icon-button back-project" aria-label="Back to projects">
            <Icon name="back" /> </RouterLink
          ><span>{{ project.name }}</span>
        </nav>
        <button
          class="icon-button"
          :aria-pressed="rail"
          aria-label="Toggle project properties"
          @click="rail = !rail"
        >
          <Icon name="panel" />
        </button>
      </header>
      <div class="project-view-toolbar">
        <div
          class="project-view-tabs"
          role="tablist"
          aria-label="Project views"
          @keydown="switchProjectTab"
        >
          <button
            v-for="tab in projectTabs"
            :id="`project-view-${tab}`"
            :key="tab"
            type="button"
            class="button compact"
            role="tab"
            :class="{ active: selectedTab === tab }"
            :data-project-tab="tab"
            :aria-selected="selectedTab === tab"
            :aria-controls="`project-panel-${tab}`"
            :tabindex="selectedTab === tab ? 0 : -1"
            @click="selectedTab = tab"
          >
            <Icon :name="tab === 'Studies' ? 'tasks' : 'chat'" :size="14" />
            {{ tab }}
          </button>
        </div>
      </div>
      <div
        v-if="selectedTab === 'Studies'"
        id="project-panel-Studies"
        class="project-board"
        role="tabpanel"
        aria-labelledby="project-view-Studies"
      >
        <StudyBoard :key="project.id" :project-id="project.id" />
      </div>
      <ProjectCoresearch
        v-else
        id="project-panel-Coresearches"
        :project-id="project.id"
        role="tabpanel"
        aria-labelledby="project-view-Coresearches"
      />
    </section>
    <div
      v-if="rail"
      class="project-splitter"
      role="separator"
      tabindex="0"
      aria-orientation="vertical"
      aria-label="Resize project properties"
      :aria-valuenow="inspector.width.value"
      :aria-valuemin="inspector.minimum.value"
      :aria-valuemax="inspector.maximum.value"
      @pointerdown="inspector.pointerdown($event)"
      @keydown="inspector.keydown"
    />
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
            >Status<SelectPicker :fit-content-width="180"
              ><select
                aria-label="Project status"
                :value="project.status"
                :disabled="busy"
                @change="
                  update({
                    status: ($event.target as HTMLSelectElement).value as Research['status'],
                  })
                "
              >
                <option v-for="s in statuses" :key="s.value" :value="s.value">{{ s.label }}</option>
              </select></SelectPicker
            ></label
          >
          <label
            >Priority<SelectPicker :fit-content-width="180"
              ><select
                aria-label="Project priority"
                :value="project.priority"
                :disabled="busy"
                @change="
                  update({
                    priority: ($event.target as HTMLSelectElement).value as Research['priority'],
                  })
                "
              >
                <option v-for="p in priorities" :key="p.value" :value="p.value">
                  {{ p.label }}
                </option>
              </select></SelectPicker
            ></label
          >
          <label
            >Lead<SelectPicker :fit-content-width="180"
              ><select
                aria-label="Project lead"
                :value="currentLead"
                :disabled="busy"
                @change="pickLead"
              >
                <option value="">No lead</option>
                <option v-if="!leadAvailable" :value="currentLead" disabled>
                  Unavailable lead
                </option>
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
              </select></SelectPicker
            ></label
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
  height: 56px;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  gap: 8px;
}
.project-heading nav {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 14px;
}
.project-heading nav a {
  color: var(--muted);
  flex: none;
}
.project-heading .back-project {
  width: 20px;
  margin-left: -8px;
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
  width: var(--project-properties-width);
  flex: 0 0 var(--project-properties-width);
  padding: 20px 12px 24px;
  overflow: auto;
}
.project-splitter {
  flex: none;
  width: 1px;
  background: var(--border);
  cursor: col-resize;
  position: relative;
  touch-action: none;
}
.project-splitter::before {
  content: '';
  position: absolute;
  inset: 0 -3px;
  z-index: 2;
}
.project-splitter:hover,
.project-splitter:focus-visible {
  background: var(--primary);
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
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
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
  gap: 0;
  padding-inline: 8px;
}
.project-property-rows label {
  display: grid;
  grid-template-columns: 64px minmax(0, 180px);
  gap: 5px;
  align-items: center;
  min-height: 34px;
  font-size: 13px;
  color: var(--muted);
}
.project-property-rows :deep(.select-picker) {
  justify-self: start;
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
  resize: none;
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
  font-size: 13px;
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
.project-view-toolbar,
.project-view-tabs {
  display: flex;
  flex: none;
  align-items: center;
  gap: 6px;
}
.project-view-toolbar {
  padding: 8px 16px;
}
@media (max-width: 760px) {
  .project-heading nav {
    font-size: 13px;
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
  .project-splitter {
    display: none;
  }
  .project-properties-rail {
    width: 100%;
    flex-basis: auto;
    border-left: 0;
    border-top: 1px solid var(--border);
  }
}
</style>
<style>
:is(.project-board, .project-coresearch) .study-toolbar {
  height: 56px;
  padding: 10px 16px;
}
.project-board .working-control {
  display: none;
}
:is(.project-board, .project-coresearch) .toolbar-group.push-right > .menu-anchor > .button,
.project-board .toolbar-group.push-right > .button {
  width: 28px;
  height: 28px;
  padding: 0;
  gap: 0;
}
.project-board .toolbar-group.push-right > .menu-anchor > .button {
  font-size: 0;
}
.project-board .board-scroll {
  padding: 8px 16px 24px;
}
</style>
