<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch, type Directive } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useRouter } from 'vue-router';
import { Columns3, List, FunnelX } from '@lucide/vue';
import Icon from '../../ui/Icon.vue';
import AvatarDisplay from '../../ui/AvatarDisplay.vue';
import Aristotle from '../coresearch/Aristotle.vue';
import {
  useAgents,
  useCards,
  useMe,
  useMembers,
  useResearches,
  useWorkspace,
} from '../../api/queries';
import type { Card } from '../../api/types';
import { agentDisplayName } from '../../lib/agent-name';
import {
  blockBadge,
  boardColumns,
  columnName,
  endLabel,
  filterCategories as categories,
  filterOptions,
  boardColumnOf,
  groupByColumn,
  boardActivity,
  matchesFilters,
  roleLabel,
  stages,
  stateLabel,
  studyNumber,
  studyDisplayTitle,
  timeAgo,
  type FilterCategory as Category,
  type StudyFilters,
} from '../../lib/studies';
import { readJSON, writeJSON } from '../../storage';
import { notify } from '../../state/notice';
import { useStudyComposer } from '../../state/study-composer';
const metadataObservers = new WeakMap<HTMLElement, { resize?: ResizeObserver; mutation?: MutationObserver }>();
const vMetadataRows: Directive<HTMLElement> = {
  mounted(el) {
    const update = () => {
      const assignee = el.querySelector<HTMLElement>('.card-assignee');
      const owner = el.querySelector<HTMLElement>('.card-owner-separated');
      owner?.classList.toggle('same-row', !!assignee && Math.abs(assignee.offsetTop - owner.offsetTop) < 2);
    };
    const resize = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(update);
    const mutation = typeof MutationObserver === 'undefined' ? undefined : new MutationObserver(update);
    resize?.observe(el);
    mutation?.observe(el, { childList: true, subtree: true, characterData: true });
    metadataObservers.set(el, { resize, mutation });
    update();
  },
  updated(el) {
    const assignee = el.querySelector<HTMLElement>('.card-assignee');
    const owner = el.querySelector<HTMLElement>('.card-owner-separated');
    owner?.classList.toggle('same-row', !!assignee && Math.abs(assignee.offsetTop - owner.offsetTop) < 2);
  },
  unmounted(el) {
    const observers = metadataObservers.get(el);
    observers?.resize?.disconnect();
    observers?.mutation?.disconnect();
    metadataObservers.delete(el);
  },
};
const props = withDefaults(defineProps<{ projectId?: string; scope?: 'workspace' | 'mine' }>(), {
  scope: 'workspace',
});
defineEmits<{ create: [] }>();
const router = useRouter();
const cards = useCards();
const me = useMe();
const members = useMembers();
const agents = useAgents();
const researches = useResearches();
const workspace = useWorkspace();
const { openComposer } = useStudyComposer();
const client = useQueryClient();
const actionError = ref('');
const studyMenuTrigger = ref<HTMLElement>();
const studyMenu = ref<{ id: string; x: number; y: number } | null>(null);
let workspaceGeneration = 0;
watch(
  workspaceId,
  () => {
    workspaceGeneration++;
    actionError.value = '';
    studyMenu.value = null;
  },
  { flush: 'sync' },
);
onUnmounted(() => {
  workspaceGeneration++;
});

function openStudyMenu(event: MouseEvent | KeyboardEvent, card: Card, fromButton = false) {
  // Only a study's owner deletes it, at any time.
  if (card.owner_id !== me.data.value?.id) return;
  event.preventDefault();
  closeMenus();
  studyMenuTrigger.value = event.currentTarget as HTMLElement;
  const rect = studyMenuTrigger.value.getBoundingClientRect();
  studyMenu.value = {
    id: card.id,
    x: fromButton
      ? Math.min(rect.right - 200, window.innerWidth - 208)
      : event instanceof MouseEvent
        ? event.clientX
        : rect.left + 60,
    y: fromButton ? rect.bottom + 4 : event instanceof MouseEvent ? event.clientY : rect.bottom,
  };
  void nextTick(() => document.querySelector<HTMLButtonElement>('.study-menu button')?.focus());
}
async function removeStudy(id: string) {
  studyMenu.value = null;
  const card = all.value.find((c) => c.id === id);
  if (!card || !confirm(`Delete ${number(card)} “${card.title}”? This cannot be undone.`)) return;
  const workspace = workspaceId.value;
  const ownScope = workspaceGeneration;
  const key = keys.cards(workspace);
  const current = () => workspaceGeneration === ownScope && workspaceId.value === workspace;
  actionError.value = '';
  try {
    await api('DELETE', `/api/cards/${id}`);
    if (current()) await client.invalidateQueries({ queryKey: key });
  } catch (e) {
    if (current()) actionError.value = (e as Error).message;
  }
}

type Display = { mode: 'Board' | 'List'; properties: string[] };
const propertyNames = ['Question', 'Owner', 'Assignee', 'Deadline', 'Project', 'Role', 'Updated'];
const displayKey = props.projectId ? 'lykeion.project-display' : 'lykeion.study-display';
const saved = readJSON<Partial<Display>>(displayKey, {});
const display = reactive<Display>({
  mode: saved.mode === 'List' ? 'List' : 'Board',
  properties: Array.isArray(saved.properties) ? saved.properties : [...propertyNames],
});
watch(
  display,
  (value) => {
    if (!writeJSON(displayKey, value)) notify('Display preferences could not be saved.');
  },
  { deep: true },
);

const tab = ref<'All' | 'Members' | 'Agents'>('All');
const actorTabs = computed(() => (props.projectId ? (['All', 'Members', 'Agents'] as const) : []));
const popup = ref('');
const submenu = ref<Category | null>(null);
const filters = ref<StudyFilters>({});
const menuRoot = ref<HTMLElement>();
const trigger = ref<HTMLElement>();
const categoryIcons: Record<Category, string> = {
  Column: 'columns',
  State: 'circle',
  Flag: 'flag',
  Owner: 'users',
};
const all = computed(() =>
  (cards.data.value ?? []).filter(
    (c) =>
      (!props.projectId || c.research_id === props.projectId) &&
      (props.scope === 'workspace' ||
        (Boolean(me.data.value?.id) && c.owner_id === me.data.value?.id)),
  ),
);
const number = (c: Card) => studyNumber(workspace.data.value, c);
function ownerName(id: string) {
  const m = members.data.value?.find((x) => x.user_id === id);
  return m ? m.name || m.email : 'Former member';
}
function assigneeName(card: Card) {
  if (card.assignee_type === 'team') return 'Team';
  if (!card.assignee_id) return 'Unassigned';
  if (card.assignee_type === 'member') return ownerName(card.assignee_id);
  return agentDisplayName(
    agents.data.value?.find((agent) => agent.id === card.assignee_id)?.name ?? 'Unavailable agent',
  );
}
function assignedAgent(card: Card) {
  return card.assignee_type === 'agent'
    ? agents.data.value?.find((agent) => agent.id === card.assignee_id)
    : undefined;
}
function cardStateLabel(card: Card) {
  return card.state === 'done' || (card.state === 'working' && !card.agent_running)
    ? ''
    : stateLabel(card);
}
function finalStage(card: Card) {
  return boardColumnOf(card) === 'done' && stages.includes(card.board_column)
    ? columnName(card.board_column)
    : '';
}
const options = (category: Category) =>
  category === 'Owner'
    ? (members.data.value ?? []).map((m) => ({ value: m.user_id, label: m.name || m.email }))
    : filterOptions[category];
const visible = computed(() =>
  all.value.filter(
    (c) =>
      (!props.projectId ||
        tab.value === 'All' ||
        (tab.value === 'Members' && c.assignee_type === 'member') ||
        (tab.value === 'Agents' && (c.assignee_type === 'agent' || c.assignee_type === 'team'))) &&
      matchesFilters(c, filters.value),
  ),
);
const grouped = computed(() => groupByColumn(visible.value));
const activity = computed(() => boardActivity(all.value));
const activeFilterCount = computed(() =>
  Object.values(filters.value).reduce((sum, items) => sum + (items?.length ?? 0), 0),
);
const listGroups = computed(() =>
  boardColumns.map((c) => ({
    ...c,
    cards: grouped.value[c.id],
  })),
);
const collapsedGroups = ref<string[]>([]);
function toggleGroup(id: string) {
  collapsedGroups.value = collapsedGroups.value.includes(id)
    ? collapsedGroups.value.filter((x) => x !== id)
    : [...collapsedGroups.value, id];
}
function toggleFilter(category: Category, value: string) {
  const selected = filters.value[category] ?? [];
  filters.value = {
    ...filters.value,
    [category]: selected.includes(value)
      ? selected.filter((x) => x !== value)
      : [...selected, value],
  };
}
function toggleProperty(name: string) {
  display.properties = display.properties.includes(name)
    ? display.properties.filter((x) => x !== name)
    : [...display.properties, name];
}
function open(id: string) {
  studyMenu.value = null;
  void router.push((props.scope === 'mine' ? '/my-studies/' : '/studies/') + id);
}
function closeMenus() {
  popup.value = '';
  submenu.value = null;
}
async function menu(name: string, event: Event) {
  trigger.value = event.currentTarget as HTMLElement;
  popup.value = popup.value === name ? '' : name;
  submenu.value = null;
  await nextTick();
  if (event instanceof KeyboardEvent || (event instanceof MouseEvent && event.detail === 0))
    menuRoot.value?.querySelector<HTMLElement>('button:not([disabled])')?.focus();
}
function dismiss(e: Event) {
  if (!(e.target as HTMLElement).closest('.menu-anchor')) closeMenus();
  if (!(e.target as HTMLElement).closest('.study-menu')) studyMenu.value = null;
}
function key(e: KeyboardEvent) {
  if (e.key === 'Escape' && studyMenu.value) {
    studyMenu.value = null;
    studyMenuTrigger.value?.focus();
  }
  if (!popup.value || e.key !== 'Escape') return;
  e.preventDefault();
  if (submenu.value) {
    const category = submenu.value;
    submenu.value = null;
    void nextTick(() =>
      menuRoot.value?.querySelector<HTMLElement>(`[data-category="${category}"]`)?.focus(),
    );
  } else {
    closeMenus();
    trigger.value?.focus();
  }
}
function menuKeys(e: KeyboardEvent) {
  const target = e.target as HTMLElement;
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) {
    e.preventDefault();
    const scope = target.closest('[role=menu]') ?? menuRoot.value;
    const buttons = [
      ...(scope?.querySelectorAll<HTMLButtonElement>(
        ':scope > button:not([disabled]), :scope > .category > button:not([disabled]), :scope > .property-chips > button:not([disabled])',
      ) ?? []),
    ];
    const index = buttons.indexOf(target as HTMLButtonElement);
    buttons[
      e.key === 'Home'
        ? 0
        : e.key === 'End'
          ? buttons.length - 1
          : (index + (e.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length
    ]?.focus();
  }
  if (e.key === 'ArrowRight' && target.dataset.category) {
    e.preventDefault();
    submenu.value = target.dataset.category as Category;
    void nextTick(() =>
      menuRoot.value?.querySelector<HTMLElement>('.filter-submenu button')?.focus(),
    );
  }
  if (e.key === 'ArrowLeft' && submenu.value) {
    e.preventDefault();
    const category = submenu.value;
    submenu.value = null;
    void nextTick(() =>
      menuRoot.value?.querySelector<HTMLElement>(`[data-category="${category}"]`)?.focus(),
    );
  }
}
onMounted(() => {
  document.addEventListener('click', dismiss);
  window.addEventListener('keydown', key);
});
onUnmounted(() => {
  document.removeEventListener('click', dismiss);
  window.removeEventListener('keydown', key);
});
</script>
<template>
  <div class="study-toolbar">
    <div v-if="actorTabs.length" class="toolbar-group">
      <button
        v-for="name in actorTabs"
        :key="name"
        class="button compact"
        :class="{ active: tab === name }"
        :aria-pressed="tab === name"
        @click="tab = name"
      >
        {{ name }}
      </button>
    </div>
    <div class="toolbar-group push-right">
      <div class="working-control">
        <button class="button compact" disabled>
          {{ activity.working }} agents working<template v-if="activity.waiting"
            >, {{ activity.waiting }} waiting</template
          >
        </button>
        <div class="working-tooltip" role="tooltip">
          {{
            activity.waiting
              ? 'Studies with a role running now, and studies waiting for a machine'
              : activity.working
                ? 'Studies with a role running now'
                : 'No agents working right now'
          }}
        </div>
      </div>
      <div class="menu-anchor">
        <button
          class="button compact"
          :class="{ active: activeFilterCount }"
          aria-label="Filter"
          title="Filter"
          :aria-expanded="popup === 'filter'"
          aria-haspopup="menu"
          @click="menu('filter', $event)"
          @keydown.down.prevent="menu('filter', $event)"
        >
          <Icon name="filter" :size="14" /><span v-if="activeFilterCount">{{
            activeFilterCount
          }}</span>
        </button>
        <div
          v-if="popup === 'filter'"
          ref="menuRoot"
          class="board-menu filter-menu"
          role="menu"
          aria-label="Filter"
          @keydown="menuKeys"
        >
          <div v-for="category in categories" :key="category" class="category">
            <button
              role="menuitem"
              :data-category="category"
              aria-haspopup="menu"
              :aria-expanded="submenu === category"
              @mouseenter="submenu = category"
              @click="submenu = category"
            >
              <Icon :name="categoryIcons[category]" :size="14" />{{ category
              }}<Icon name="right" class="push-right" :size="12" />
            </button>
            <div
              v-if="submenu === category"
              class="board-menu filter-submenu"
              role="menu"
              :aria-label="category"
            >
              <button
                v-for="option in options(category)"
                :key="option.value"
                role="menuitemcheckbox"
                :aria-checked="filters[category]?.includes(option.value) || false"
                @click="toggleFilter(category, option.value)"
              >
                <span class="menu-check"
                  ><Icon
                    v-if="filters[category]?.includes(option.value)"
                    name="check"
                    :size="12" /></span
                ><span
                  v-if="category === 'Column'"
                  class="stage-dot"
                  :class="'status-' + option.value"
                /><span>{{ option.label }}</span>
              </button>
            </div>
          </div>
          <button
            v-if="activeFilterCount"
            role="menuitem"
            class="clear-filters"
            @click="
              filters = {};
              closeMenus();
            "
          >
            Clear filters
          </button>
        </div>
      </div>
      <div class="menu-anchor">
        <button
          class="button compact"
          aria-label="Display"
          title="Display"
          :aria-expanded="popup === 'display'"
          @click="menu('display', $event)"
          @keydown.down.prevent="menu('display', $event)"
        >
          <Icon name="filters" :size="14" />
        </button>
        <div
          v-if="popup === 'display'"
          ref="menuRoot"
          class="board-menu display-popover"
          @keydown="menuKeys"
        >
          <span class="display-caption">Card properties</span>
          <div class="property-options">
            <button
              v-for="name in propertyNames"
              :key="name"
              role="switch"
              :aria-checked="display.properties.includes(name)"
              @click="toggleProperty(name)"
            >
              <span>{{ name }}</span>
              <span class="property-switch" aria-hidden="true"></span>
            </button>
          </div>
        </div>
      </div>
      <button
        class="button compact"
        :aria-label="display.mode"
        :title="display.mode"
        @click="display.mode = display.mode === 'Board' ? 'List' : 'Board'"
      >
        <component
          :is="display.mode === 'Board' ? Columns3 : List"
          :size="14"
          :stroke-width="1.7"
          aria-hidden="true"
        />
      </button>
    </div>
  </div>
  <div v-if="activeFilterCount" class="filter-summary">
    <template v-for="category in categories" :key="category"
      ><button
        v-for="value in filters[category]"
        :key="value"
        class="filter-chip"
        @click="toggleFilter(category, value)"
      >
        {{ category }}: {{ options(category).find((o) => o.value === value)?.label ?? value
        }}<Icon name="close" :size="11" /></button></template
    ><button class="text-button" @click="filters = {}">Clear</button>
  </div>
  <p v-if="actionError" class="board-action-error" role="alert">{{ actionError }}</p>
  <p v-if="cards.isPending.value" role="status">Loading studies…</p>
  <div v-if="cards.data.value && display.mode === 'Board'" class="board-scroll">
    <section
      v-for="column in boardColumns"
      :key="column.id"
      class="board-column"
      :class="'stage-' + column.id"
      :aria-label="column.name"
    >
      <header>
        <span v-if="column.id === 'blocked'" class="blocked-stage-icon" aria-hidden="true">❌</span>
        <span v-else-if="column.id === 'done'" class="done-stage-icon" aria-hidden="true">✅</span>
        <span v-else class="stage-dot" /><strong>{{ column.name }}</strong
        ><span>{{ grouped[column.id].length }}</span
        ><button
          class="icon-button push-right"
          aria-label="New study"
          @click="openComposer(undefined, projectId)"
        >
          <Icon name="plus" :size="14" />
        </button>
      </header>
      <div class="board-cards">
        <div v-for="card in grouped[column.id]" :key="card.id" class="study-card">
          <button
            class="task-card"
            :data-card="card.id"
            @contextmenu="openStudyMenu($event, card)"
            @keydown.shift.f10="openStudyMenu($event, card)"
            @click="open(card.id)"
          >
            <small class="card-id"
              >{{ number(card)
              }}<span
                v-if="card.ending && !(card.ending === 'stopped' && boardColumnOf(card) === 'done')"
                class="chip"
                :class="'chip-' + card.ending"
                >{{ endLabel(card) }}</span
              ><span
                v-if="cardStateLabel(card) && column.id !== 'blocked'"
                class="chip"
                :class="'chip-' + card.state"
                >{{ cardStateLabel(card) }}</span
              ><span v-if="column.id === 'blocked'" class="chip">{{
                columnName(card.board_column)
              }}</span
              ><span v-if="finalStage(card)" class="chip">{{ finalStage(card) }}</span
              ><span
                v-if="card.flag === 'escalated' || card.state === 'blocked'"
                class="chip chip-escalated"
                :title="card.hold_text || undefined"
                >{{ blockBadge(card) }}</span
              ></small
            ><span>{{ studyDisplayTitle(card.title) }}</span>
            <span
              v-if="display.properties.includes('Question') && card.question"
              class="card-question"
              >{{ card.question }}</span
            >
            <span v-if="card.waiting" class="card-waiting">{{ card.waiting }}</span>
            <div v-metadata-rows class="card-metadata">
              <span
                v-if="display.properties.includes('Assignee') && card.assignee_id"
                class="card-assignee"
              >
                <AvatarDisplay
                  v-if="assignedAgent(card)?.avatar_url"
                  :value="assignedAgent(card)!.avatar_url.slice(6)"
                  :size="14"
                />
                <span
                  v-else-if="assignedAgent(card)?.built_in_key === 'aristotle'"
                  class="card-aristotle"
                  ><Aristotle still
                /></span>
                <Icon v-else :name="card.assignee_type === 'agent' ? 'bot' : 'users'" :size="14" />
                {{ assigneeName(card) }}
              </span>
              <span
                v-if="display.properties.includes('Owner')"
                :class="{
                  'card-owner-separated':
                    display.properties.includes('Assignee') && card.assignee_id,
                }"
              >
                <Icon name="users" :size="12" />{{ ownerName(card.owner_id) }}
              </span>
              <span v-if="display.properties.includes('Deadline') && card.deadline"
                >Due {{ new Date(card.deadline).toLocaleDateString() }}</span
              >
              <span v-if="display.properties.includes('Project') && card.research_id">{{
                researches.data.value?.find((project) => project.id === card.research_id)?.name ??
                'Unavailable project'
              }}</span>
              <span v-if="display.properties.includes('Role') && card.role && card.role !== 'agent'"
                ><Icon name="bot" :size="12" />{{ roleLabel(card.role) }}</span
              ><span v-if="display.properties.includes('Updated')" class="push-right"
                >Updated {{ timeAgo(card.updated_at) }}</span
              >
            </div>
          </button>
          <button
            v-if="card.owner_id === me.data.value?.id"
            class="icon-button study-card-actions"
            :aria-label="`Actions for ${number(card)}`"
            aria-haspopup="menu"
            :aria-expanded="studyMenu?.id === card.id"
            @click.stop="openStudyMenu($event, card, true)"
          >
            <Icon name="more" :size="14" />
          </button>
        </div>
        <p v-if="!grouped[column.id].length" class="column-empty">No studies</p>
      </div>
    </section>
  </div>
  <div v-else-if="cards.data.value" class="study-list">
    <section v-for="group in listGroups" :key="group.id">
      <button
        class="list-group"
        :aria-expanded="!collapsedGroups.includes(group.id)"
        @click="toggleGroup(group.id)"
      >
        <Icon :name="collapsedGroups.includes(group.id) ? 'right' : 'down'" :size="12" /><span
          class="stage-dot"
          :class="'status-' + group.id"
        />{{ group.name }}<small>{{ group.cards.length }}</small>
      </button>
      <template v-if="!collapsedGroups.includes(group.id)">
        <button v-for="card in group.cards" :key="card.id" class="list-row" @click="open(card.id)">
          <small>{{ number(card) }}</small
          ><span class="truncate">{{ studyDisplayTitle(card.title) }}</span>
          <span class="row-meta push-right">
            <span
              v-if="cardStateLabel(card) && group.id !== 'blocked'"
              class="chip"
              :class="'chip-' + card.state"
              >{{ cardStateLabel(card) }}</span
            >
            <span v-if="card.queued" class="chip chip-queued">queued</span>
            <span v-if="card.flag" class="chip" :class="'chip-' + card.flag">{{
              card.flag.replace(/_/g, ' ')
            }}</span>
            <span v-if="group.id === 'blocked'" class="chip">{{
              columnName(card.board_column)
            }}</span>
            <span v-if="finalStage(card)" class="chip">{{ finalStage(card) }}</span>
            <span
              v-if="card.ending && !(card.ending === 'stopped' && boardColumnOf(card) === 'done')"
              class="chip"
              :class="'chip-' + card.ending"
              >{{ endLabel(card) }}</span
            >
            <span v-if="card.role">{{ roleLabel(card.role) }}</span>
            <span>{{ ownerName(card.owner_id) }}</span>
          </span>
        </button>
        <p v-if="!group.cards.length" class="list-empty">No studies</p>
      </template>
    </section>
  </div>
  <div
    v-if="cards.isSuccess.value && !visible.length && activeFilterCount"
    class="center-empty board-empty"
  >
    <FunnelX :size="40" :stroke-width="1.7" aria-hidden="true" /><span
      >No studies match these filters</span
    ><button
      class="button compact"
      @click="
        filters = {};
        tab = 'All';
      "
    >
      Clear filters
    </button>
  </div>
  <div
    v-if="studyMenu"
    class="board-menu study-menu"
    role="menu"
    aria-label="Study actions"
    :style="{ left: studyMenu.x + 'px', top: studyMenu.y + 'px' }"
    @keydown="menuKeys"
  >
    <button role="menuitem" class="danger-item" @click="removeStudy(studyMenu.id)">Delete</button>
  </div>
</template>

<style scoped>
.study-card {
  position: relative;
}
.study-card .card-id {
  padding-right: 24px;
}
.study-card-actions {
  position: absolute;
  top: 8px;
  right: 6px;
}

.board-menu {
  position: absolute;
  z-index: 40;
  top: calc(100% + 4px);
  right: 0;
  background: var(--fill-menu);
  border: 1px solid #ffffff55;
  box-shadow: 0 6px 16px #00000012;
  border-radius: 8px;
  padding: 4px;
  color: var(--text);
  min-width: 152px;
  font-size: 14px;
}
.board-menu > button,
.category > button {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  min-height: 28px;
  padding: 4px 6px;
  text-align: left;
  border-radius: 4px;
  white-space: nowrap;
}
.board-menu > button:hover,
.board-menu > button:focus-visible,
.category > button:hover,
.category > button:focus-visible,
.category > button[aria-expanded='true'] {
  background: #00000008;
  outline: 0;
}
.category {
  position: relative;
}
.filter-menu {
  right: 4px;
}
.filter-submenu {
  left: auto;
  right: 100%;
  top: -5px;
  min-width: 194px;
}
.filter-submenu > button {
  gap: 6px;
}
.filter-submenu small {
  font-size: 11px;
  color: var(--muted);
}
.menu-check {
  width: 16px;
  height: 16px;
  flex: none;
  border: 1px solid #ffffff50;
  border-radius: 3px;
  display: grid;
  place-items: center;
}
.filter-submenu .stage-dot {
  margin: 0;
  flex: none;
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
.views-menu {
  left: 0;
  right: auto;
  width: 180px;
}
.table-group-menu {
  width: 192px;
}
.mode-menu {
  min-width: 132px;
}
.mode-caption {
  display: block;
  height: 24px;
  padding: 3px 6px;
  font-size: 12px;
  color: var(--muted);
}
.display-popover {
  width: 258px;
  padding: 12px;
}
.display-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 14px;
}
.table-display .display-row:last-child {
  margin-bottom: 0;
}
.display-popover select {
  background: transparent;
  border: 1px solid #ffffff40;
  border-radius: 6px;
  padding: 5px 10px;
  color: var(--text);
  font-size: 14px;
  min-width: 128px;
}
.display-caption {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin: 0 0 8px;
}
.order-row {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.order-row select {
  flex: 1;
  min-width: 0;
}
.order-row > button {
  background: var(--surface);
  border-radius: 6px;
  padding: 5px 10px;
  font-size: 13px;
  white-space: nowrap;
}
.display-row input {
  appearance: none;
  width: 24px;
  height: 14px;
  background: var(--toggle-track-strong);
  border-radius: 10px;
  position: relative;
  cursor: pointer;
  margin: 0;
}
.display-row input:checked {
  background: var(--primary);
}
.display-row input:after {
  content: '';
  position: absolute;
  top: 1px;
  left: 1px;
  width: 12px;
  height: 12px;
  background: white;
  border-radius: 50%;
}
.display-row input:checked:after {
  left: 11px;
}
.property-options > button {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 28px;
  padding: 2px 0;
  font-size: 14px;
  color: var(--text);
}
.property-options > button:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
  border-radius: 3px;
}
.property-switch {
  position: relative;
  width: 24px;
  height: 14px;
  flex: none;
  border-radius: 10px;
  background: var(--toggle-track-strong);
}
.property-switch::after {
  content: '';
  position: absolute;
  top: 1px;
  left: 1px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: white;
}
.property-options > button[aria-checked='true'] .property-switch {
  background: var(--primary);
}
.property-options > button[aria-checked='true'] .property-switch::after {
  left: 11px;
}
.filter-summary {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 16px 8px;
  flex-wrap: wrap;
}
.filter-chip {
  display: flex;
  align-items: center;
  gap: 6px;
  background: var(--input);
  border-radius: 5px;
  padding: 3px 7px;
  font-size: 12px;
}
.card-metadata {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 5px;
  font-size: 11px;
  color: var(--muted);
}
.card-question {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  margin-top: 5px;
  color: var(--muted);
  font-size: 11px;
}
/* Why the study waits, from the machine's open wait: two lines at most. */
.card-waiting {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  margin-top: 5px;
  font-size: 11px;
  color: var(--muted);
}
.card-metadata:empty {
  display: none;
}
.card-metadata > span {
  display: flex;
  gap: 4px;
  align-items: center;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.card-metadata > .card-owner-separated {
  gap: 6px;
}
.card-owner-separated::before {
  content: '';
  width: 1px;
  height: 11px;
  flex: none;
  background: transparent;
}
.card-owner-separated:not(.same-row) {
  transform: translateX(-7px);
}
.card-owner-separated.same-row::before {
  background: color-mix(in srgb, var(--muted) 65%, var(--text));
}
.card-aristotle {
  display: inline-flex;
  width: 14px;
  height: 14px;
  flex: none;
  overflow: hidden;
  border-radius: 50%;
}
.card-aristotle :deep(.aristotle-art) {
  width: 100%;
  height: 100%;
}
.view-dialog {
  width: 578px;
  background: var(--fill-menu);
}
.view-dialog header {
  padding: 16px;
  display: flex;
  align-items: flex-start;
  font-size: 16px;
}
.view-dialog header small {
  display: block;
  margin-top: 6px;
  font-size: 14px;
  color: var(--muted);
}
.view-fields {
  padding: 0 16px 20px;
}
.view-fields > label,
.view-filter-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 0 0 18px;
  font-size: 12px;
}
.view-fields > label > span,
.view-filter-row > span {
  width: 64px;
  flex: none;
  color: var(--muted);
}
.view-fields input,
.view-fields select {
  width: 256px;
  padding: 6px 10px;
  border: 1px solid #ffffff50;
  border-radius: 6px;
  background: transparent;
  font-size: 14px;
  min-width: 0;
}
.view-filter-row {
  align-items: flex-start;
}
.view-filter-row small {
  display: block;
  margin-top: 6px;
}
.view-filter-controls {
  display: flex;
  gap: 4px;
  margin-top: 6px;
}
.view-filter-controls select {
  width: 160px;
  font-size: 12px;
}
.view-fields details {
  font-size: 12px;
  margin-top: 26px;
}
.view-fields summary {
  cursor: pointer;
}
.view-fields summary span {
  color: var(--muted);
  margin-left: 4px;
}
.view-fields details p {
  margin: 12px 0;
  color: var(--muted);
}
.view-dialog footer {
  display: flex;
  gap: 18px;
  background: #00000003;
  padding: 16px;
}
.manage-views > p {
  padding: 16px;
  color: var(--muted);
}
.manage-views > div {
  display: flex;
  padding: 12px 16px;
  justify-content: space-between;
  border-top: 1px solid var(--border);
}

.table-workspace {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.table-actions {
  height: 40px;
  display: flex;
  align-items: center;
  padding: 0 16px 6px 12px;
  gap: 12px;
}
.table-actions > label {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--muted);
}
.table-actions input {
  border: 0;
  outline: 0;
  background: none;
  width: 184px;
  font-size: 14px;
}
.table-actions > small {
  font-size: 12px;
  color: var(--muted);
}
.export-button {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--muted);
}
.table-scroll {
  overflow: auto;
  flex: 1;
  min-height: 0;
}
.studies-table {
  table-layout: fixed;
  border-collapse: collapse;
  font-size: 14px;
  min-width: 100%;
}
.studies-table th,
.studies-table td {
  border: 1px solid var(--border);
  border-left: 0;
  height: 40px;
  padding: 0 16px;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  position: relative;
}
.studies-table th {
  background: var(--table-header);
  font-weight: 400;
  font-size: 12px;
  letter-spacing: 0.3px;
  color: var(--muted);
  overflow: visible;
}
.studies-table th > button {
  font: inherit;
  padding: 0;
}
.studies-table th:first-child,
.studies-table td:first-child {
  text-align: center;
  padding: 0;
  width: 58px;
}
.studies-table input[type='checkbox'] {
  width: 14px;
  height: 14px;
  accent-color: var(--primary);
}
.studies-table td > select {
  appearance: none;
  border: 0;
  background: transparent;
  font: inherit;
  width: 100%;
  outline-offset: 4px;
  padding: 4px 0;
  cursor: pointer;
}
.studies-table tbody tr.selected {
  background: var(--input);
}
.table-title {
  display: flex;
  align-items: center;
  gap: 30px;
  padding-left: 22px;
}
.table-title > small {
  font-size: 12px;
  color: var(--muted);
  min-width: 40px;
}
.table-title > button:not(.rename-study) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
}
.table-title.nested-study {
  padding-left: 20px;
}
.table-title .rename-study {
  margin-left: auto;
  opacity: 0;
  flex: none;
}
.studies-table tr:hover .rename-study,
.rename-study:focus-visible {
  opacity: 1;
}
.table-edit-button {
  padding: 4px 0;
  color: var(--muted);
  text-align: left;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
}
.table-cell-editor {
  width: 100%;
  border: 1px solid var(--primary);
  border-radius: 4px;
  background: var(--surface);
  font: inherit;
  padding: 4px;
}
.column-resizer {
  position: absolute;
  right: -3px;
  top: 0;
  width: 7px;
  height: 100%;
  cursor: col-resize;
  touch-action: none;
  z-index: 1;
}
.column-resizer:hover {
  background: var(--primary);
}
.table-group-row td {
  background: var(--input);
  font-weight: 500;
  text-align: left !important;
  padding: 0 16px !important;
}
.table-group-row small {
  color: var(--muted);
  margin-left: 8px;
}
.table-empty {
  text-align: center;
  color: var(--muted);
  font-size: 14px;
  padding: 70px 16px;
}
.table-columns-popover {
  width: 256px;
  max-height: 366px;
  overflow: auto;
  padding: 8px;
}
.table-columns-popover input:focus {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}
.table-columns-popover input {
  width: 100%;
  border: 1px solid var(--border);
  background: transparent;
  border-radius: 6px;
  padding: 6px 10px;
  font-size: 14px;
  margin-bottom: 12px;
}
.table-columns-popover > small {
  font-size: 12px;
  color: var(--muted);
  padding: 0 4px;
  display: block;
  margin-bottom: 4px;
}
.table-columns-popover button:disabled {
  opacity: 0.45;
}
.hierarchy-help {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-top: 2px;
}
.swimlane-scroll {
  padding: 16px 16px;
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.swimlane-headings,
.swimlane-cells {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: 280px;
  gap: 16px;
  width: max-content;
  min-width: 100%;
}
.swimlane-headings > div {
  height: 44px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  padding: 0 12px;
  gap: 8px;
  font-size: 12px;
  background: var(--lane-bg);
}
.swimlane-headings strong {
  font-weight: 500;
}
.swimlane-headings small {
  color: var(--muted);
  font-size: 12px;
}
.lane-status-grounding {
  background: var(--lane-grounding) !important;
}
.lane-title {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 22px 4px 12px;
  font-size: 14px;
}
.lane-title strong {
  font-weight: 500;
}
.lane-title small {
  color: var(--muted);
  font-size: 12px;
}
.swimlane-cell {
  min-height: 144px;
  border-radius: 12px;
  background: var(--lane-bg);
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: stretch;
}
.swimlane-cell .task-card {
  margin: 0;
  width: 100%;
  flex: none;
}
.lane-dash {
  display: block;
  text-align: center;
  color: var(--muted);
  font-size: 14px;
  padding: 19px 0;
}
.lane-add {
  margin-top: auto;
  display: flex;
  justify-content: center;
  color: var(--muted);
  width: 100%;
  height: 20px;
}
.lane-add:hover {
  background: #00000004;
}
.working-caption {
  color: var(--muted);
  font-size: 12px;
  margin-right: 4px;
}
.working-control {
  position: relative;
}
.working-active {
  background: var(--primary) !important;
  color: var(--on-primary) !important;
  border-color: var(--primary) !important;
}
.working-tooltip {
  width: 288px;
  display: none;
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  background: var(--fill-menu);
  box-shadow: 0 4px 12px #0001;
  white-space: nowrap;
  padding: 10px 12px;
  border-radius: 7px;
  font-size: 12px;
  color: var(--muted);
  z-index: 41;
}
.working-control:hover .working-tooltip,
.working-control:focus-within .working-tooltip {
  display: block;
}
.board-scroll {
  overflow-y: hidden;
}
.board-column {
  display: flex;
  flex: 0 0 340px;
  flex-direction: column;
  width: 340px;
  height: 100%;
  max-height: 100%;
  min-height: 0;
}
.board-column > header {
  flex-shrink: 0;
}
.board-cards {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  scrollbar-width: none;
}
.board-cards::-webkit-scrollbar {
  display: none;
}
.board-column.stage-done {
  background: var(--stage-grounding-bg);
}
.card-id {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.study-list {
  flex: 1;
  overflow: auto;
  padding: 0 14px 24px;
}
.list-group {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  margin-top: 6px;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--input);
  font-size: 12.5px;
  font-weight: 600;
}
.list-group small {
  color: var(--muted);
  font-weight: 400;
}
.list-row {
  display: flex;
  align-items: center;
  gap: 14px;
  width: 100%;
  padding: 7px 10px 7px 38px;
  font-size: 13.5px;
  text-align: left;
  border-bottom: 1px solid var(--border);
}
.list-row:hover {
  background: var(--hover);
}
.list-row > small {
  color: var(--muted);
  width: 62px;
  flex: none;
}
.row-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 12.5px;
  color: var(--muted);
}
.list-empty {
  color: var(--muted);
  font-size: 12.5px;
  text-align: center;
  padding: 12px 0 6px;
}
.study-menu {
  position: fixed;
  right: auto;
  top: auto;
  width: 200px;
}
.study-menu hr {
  border: 0;
  border-top: 1px solid var(--border);
  margin: 3px 0;
}
.danger-item {
  color: var(--error-text);
}
.task-card[draggable='true'] {
  cursor: grab;
}
.board-action-error {
  margin: 8px 20px;
  color: var(--error-text);
  font-size: 13px;
}
</style>
