<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { Columns3, List, Table2, Waves, FunnelX } from '@lucide/vue';
const layoutIcons: Record<string, unknown> = {
  Board: Columns3,
  List,
  Table: Table2,
  Swimlane: Waves,
};
import { stages, type Inquiry, type Stage } from '../../../../packages/contracts/src/research';
import { state, service, refresh, notify } from '../services/workspace';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
import TaskContextMenu from '../components/TaskContextMenu.vue';
defineProps<{ personal?: boolean }>();
defineEmits<{ create: [] }>();
const router = useRouter();
const categories = [
  'Status',
  'Priority',
  'Date',
  'Assignee',
  'Creator',
  'Project',
  'Project status',
  'Label',
] as const;
type Category = (typeof categories)[number];
type Filters = Partial<Record<Category, string[]>>;
type Display = {
  mode: string;
  grouping: string;
  order: string;
  descending: boolean;
  subtasks: boolean;
  properties: string[];
  swimGroup: string;
  hierarchy: boolean;
};
const defaults: Display = {
  mode: 'Board',
  grouping: 'Status',
  order: 'Created date',
  descending: true,
  subtasks: true,
  properties: ['Priority', 'Assignee', 'Due date', 'Project', 'Sub-task progress'],
  swimGroup: 'Assignee',
  hierarchy: true,
};
function read<T>(key: string, fallback: T): T {
  try {
    return JSON.parse(localStorage.getItem(key) || 'null') ?? fallback;
  } catch {
    return fallback;
  }
}
const saved = read<Partial<Display>>('lykeion.task-display', {});
const display = reactive<Display>({
  ...defaults,
  ...saved,
  properties: Array.isArray(saved.properties) ? saved.properties : defaults.properties,
});
const workingOnly = ref(false),
  taskContext = ref<{ id: string; x: number; y: number } | null>(null);
const contextTask = computed(() => all.value.find((t) => t.id === taskContext.value?.id));
function openTaskContext(task: Inquiry, event: MouseEvent | KeyboardEvent) {
  event.preventDefault();
  closeMenus();
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
  taskContext.value = {
    id: task.id,
    x: event instanceof MouseEvent ? event.clientX : rect.left + 60,
    y: event instanceof MouseEvent ? event.clientY : rect.bottom,
  };
}
function createFromColumn() {
  window.dispatchEvent(new CustomEvent('lykeion:create-task', { detail: { manual: true } }));
}
const tab = ref('All'),
  popup = ref(''),
  submenu = ref<Category | null>(null),
  filters = ref<Filters>({}),
  menuRoot = ref<HTMLElement>(),
  trigger = ref<HTMLElement>();
const propertyNames = [
  'Priority',
  'Description',
  'Assignee',
  'Start date',
  'Due date',
  'Project',
  'Labels',
  'Sub-task progress',
];
const columns = [...stages.slice(0, 4), 'Blocked', 'Reporting', 'Done'];
const categoryIcons: Record<Category, string> = {
  Status: 'circle',
  Priority: 'chart',
  Date: 'clock',
  Assignee: 'users',
  Creator: 'users',
  Project: 'folder',
  'Project status': 'circle',
  Label: 'tag',
};
const all = computed(() => state.data?.inquiries ?? []);
const priorities = ['urgent', 'high', 'medium', 'low', 'none'];
function status(t: Inquiry) {
  return t.status === 'active' ? t.stage : t.status === 'done' ? 'Done' : 'Blocked';
}
function displayDate(value?: string | null) {
  return value
    ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    : '';
}
function taskCode(t: Inquiry) {
  return `${state.data?.workspace.taskPrefix || 'LYK'}-${all.value.findIndex((x) => x.id === t.id) + 1}`;
}
function assignee(id: string | null) {
  return (
    state.data?.agents.find((x) => x.id === id)?.name ??
    state.data?.colleagues.find((x) => x.id === id)?.name ??
    (id ? 'Unknown assignee' : 'Unassigned')
  );
}
function project(id: string | null) {
  return state.data?.projects.find((x) => x.id === id)?.name ?? 'No project';
}
function values(t: Inquiry, category: Category): string[] {
  switch (category) {
    case 'Status':
      return [status(t)];
    case 'Priority':
      return [t.priority];
    case 'Assignee':
      return [t.assigneeId ?? 'none'];
    case 'Creator':
      return [t.creatorId ?? 'unknown'];
    case 'Project':
      return [t.projectId ?? 'none'];
    case 'Project status':
      return [state.data?.projects.find((p) => p.id === t.projectId)?.status ?? 'unknown'];
    case 'Label':
      return t.labels?.length ? t.labels : ['none'];
    case 'Date': {
      const today = new Date();
      today.setHours(0, 0, 0, 0);
      const due = t.dueDate ? new Date(t.dueDate) : null;
      return [
        !due
          ? 'No due date'
          : due < today
            ? 'Overdue'
            : due.getTime() < today.getTime() + 86400000
              ? 'Due today'
              : 'Upcoming',
      ];
    }
  }
}
function options(category: Category): { value: string; label: string }[] {
  switch (category) {
    case 'Status':
      return [...columns, 'Cancelled'].map((x) => ({ value: x, label: x }));
    case 'Priority':
      return priorities.map((x) => ({
        value: x,
        label: x === 'none' ? 'No priority' : x[0]!.toUpperCase() + x.slice(1),
      }));
    case 'Date':
      return ['Overdue', 'Due today', 'Upcoming', 'No due date'].map((x) => ({
        value: x,
        label: x,
      }));
    case 'Assignee':
      return [
        { value: 'none', label: 'Unassigned' },
        ...[...(state.data?.agents ?? []), ...(state.data?.colleagues ?? [])].map((x) => ({
          value: x.id,
          label: x.name,
        })),
      ];
    case 'Creator':
      return [
        { value: 'unknown', label: 'Not recorded' },
        ...(state.data?.colleagues ?? []).map((x) => ({ value: x.id, label: x.name })),
      ];
    case 'Project':
      return [
        { value: 'none', label: 'No project' },
        ...(state.data?.projects ?? []).map((x) => ({ value: x.id, label: x.name })),
      ];
    case 'Project status':
      return [
        { value: 'unknown', label: 'Not recorded' },
        ...[
          ...new Set(
            (state.data?.projects ?? []).map((x) => x.status).filter((x): x is string => !!x),
          ),
        ].map((x) => ({ value: x, label: x })),
      ];
    case 'Label':
      return [
        { value: 'none', label: 'No labels' },
        ...[...new Set(all.value.flatMap((x) => x.labels ?? []))].map((x) => ({
          value: x,
          label: x,
        })),
      ];
  }
}
function matches(t: Inquiry) {
  return Object.entries(filters.value).every(
    ([key, selected]) =>
      !selected?.length || selected.some((value) => values(t, key as Category).includes(value)),
  );
}
const tasks = computed(() => {
  let rows = all.value.filter(matches);
  if (workingOnly.value)
    rows = rows.filter((t) =>
      state.data?.agents.some((a) => a.id === t.assigneeId && a.status === 'busy'),
    );
  if (!display.subtasks) rows = rows.filter((t) => !t.parentId);
  if (tab.value === 'Agents')
    rows = rows.filter((t) => state.data?.agents.some((a) => a.id === t.assigneeId));
  if (tab.value === 'Members')
    rows = rows.filter((t) => state.data?.colleagues.some((a) => a.id === t.assigneeId));
  return [...rows].sort((a, b) => {
    const comparison =
      display.order === 'Title'
        ? a.title.localeCompare(b.title)
        : display.order === 'Priority'
          ? priorities.indexOf(a.priority) - priorities.indexOf(b.priority)
          : display.order === 'Due date'
            ? (a.dueDate ?? '9999').localeCompare(b.dueDate ?? '9999')
            : display.order === 'Updated date'
              ? (a.updatedAt ?? a.createdAt).localeCompare(b.updatedAt ?? b.createdAt)
              : a.createdAt.localeCompare(b.createdAt);
    return comparison * (display.descending ? -1 : 1);
  });
});
const activeFilterCount = computed(() =>
  Object.values(filters.value).reduce((sum, items) => sum + (items?.length ?? 0), 0),
);
const boardColumns = computed(() =>
  display.grouping === 'Status'
    ? columns
        .filter((name) => name !== 'Done' || tasks.value.some((t) => t.status === 'done'))
        .map((name) => ({ id: name, name, tasks: tasks.value.filter((t) => status(t) === name) }))
    : display.grouping === 'Priority'
      ? priorities.map((id) => ({
          id,
          name: id === 'none' ? 'No priority' : id[0]!.toUpperCase() + id.slice(1),
          tasks: tasks.value.filter((t) => t.priority === id),
        }))
      : display.grouping === 'Assignee'
        ? [
            { id: 'none', name: 'Unassigned' },
            ...[...(state.data?.agents ?? []), ...(state.data?.colleagues ?? [])].map((x) => ({
              id: x.id,
              name: x.name,
            })),
          ].map((group) => ({
            ...group,
            tasks: tasks.value.filter((t) => (t.assigneeId ?? 'none') === group.id),
          }))
        : display.grouping === 'Project'
          ? [{ id: 'none', name: 'No project' }, ...(state.data?.projects ?? [])].map((group) => ({
              id: group.id,
              name: group.name,
              tasks: tasks.value.filter((t) => (t.projectId ?? 'none') === group.id),
            }))
          : [{ id: 'all', name: 'All tasks', tasks: tasks.value }],
);
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
function open(id: string, event?: MouseEvent) {
  let preview = false;
  try {
    preview =
      JSON.parse(localStorage.getItem('lykeion.preferences') || '{}').taskOpen === 'preview';
  } catch {
    // Storage can be unavailable or hold bad JSON; tasks open in full.
  }
  if (event?.shiftKey) preview = !preview;
  void router.push('/tasks/' + id + (preview ? '?preview=1' : ''));
}
function drag(event: DragEvent, id: string) {
  event.dataTransfer?.setData('text/plain', id);
}
async function drop(event: DragEvent, destination: string) {
  event.preventDefault();
  if (display.grouping !== 'Status' && display.mode !== 'Swimlane') {
    notify('Drag tasks between scientific stages using Status grouping.');
    return;
  }
  const id = event.dataTransfer?.getData('text/plain');
  const task = all.value.find((x) => x.id === id);
  if (!task || task.stage === destination) return;
  if (!stages.includes(destination as Stage)) {
    notify('Operational status changes require the control plane.');
    return;
  }
  try {
    const receipt = await service.value.transition(task.id, task.revision, destination as Stage);
    notify(receipt.accepted ? `Moved to ${destination}` : receipt.reasons.join(' '));
    if (receipt.accepted) await refresh();
  } catch (e) {
    notify((e as Error).message);
  }
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
  const root = trigger.value?.closest('.menu-anchor')?.querySelector<HTMLElement>('.board-menu');
  if (name === 'table-columns') root?.querySelector<HTMLInputElement>('input')?.focus();
  else if (event instanceof KeyboardEvent || (event instanceof MouseEvent && event.detail === 0))
    root?.querySelector<HTMLElement>('button:not([disabled]),select,input')?.focus();
}
function dismiss(e: Event) {
  if (!(e.target as HTMLElement).closest('.menu-anchor')) closeMenus();
}
function key(e: KeyboardEvent) {
  if (!popup.value) return;
  if (e.key === 'Escape') {
    e.preventDefault();
    if (submenu.value) {
      submenu.value = null;
    } else {
      closeMenus();
      trigger.value?.focus();
    }
  }
}
function menuKeys(e: KeyboardEvent) {
  const target = e.target as HTMLElement;
  if (
    target instanceof HTMLInputElement &&
    target.closest('.table-columns-popover') &&
    e.key === 'ArrowDown'
  ) {
    e.preventDefault();
    target
      .closest('.table-columns-popover')
      ?.querySelector<HTMLButtonElement>('button:not([disabled])')
      ?.focus();
    return;
  }
  if (
    ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key) &&
    !['SELECT', 'INPUT', 'TEXTAREA'].includes(target.tagName)
  ) {
    e.preventDefault();
    const scope = target.closest('[role=menu]') ?? menuRoot.value;
    const buttons = [
      ...(scope?.querySelectorAll<HTMLButtonElement>(
        ':scope > button:not([disabled]), :scope > .category > button:not([disabled])',
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
    nextTick(() => menuRoot.value?.querySelector<HTMLElement>('.filter-submenu button')?.focus());
  }
  if (e.key === 'ArrowLeft' && submenu.value) {
    e.preventDefault();
    const category = submenu.value;
    submenu.value = null;
    nextTick(() =>
      menuRoot.value?.querySelector<HTMLElement>(`[data-category="${category}"]`)?.focus(),
    );
  }
}
type SavedView = { id: string; name: string; tab: string; filters: Filters; display: Display };
const views = ref<SavedView[]>(read('lykeion.saved-views', [])),
  viewDialog = ref(''),
  viewName = ref(''),
  viewTab = ref('All'),
  viewFilters = ref<Filters>({}),
  viewCategory = ref<Category>('Status'),
  viewChoice = ref(''),
  viewError = ref('');
function newView() {
  closeMenus();
  viewName.value = '';
  viewTab.value = tab.value;
  viewFilters.value = JSON.parse(JSON.stringify(filters.value));
  viewError.value = '';
  viewDialog.value = 'new';
}
function saveView() {
  if (!viewName.value.trim()) return;
  const entry: SavedView = {
    id: crypto.randomUUID(),
    name: viewName.value.trim(),
    tab: viewTab.value,
    filters: JSON.parse(JSON.stringify(viewFilters.value)),
    display: JSON.parse(JSON.stringify(display)),
  };
  try {
    const next = [...views.value, entry];
    localStorage.setItem('lykeion.saved-views', JSON.stringify(next));
    views.value = next;
    viewDialog.value = '';
  } catch {
    viewError.value = 'This view could not be saved on this device.';
  }
}
function applyView(view: SavedView) {
  filters.value = JSON.parse(JSON.stringify(view.filters));
  Object.assign(display, view.display);
  tab.value = view.tab;
  closeMenus();
  viewDialog.value = '';
}
function removeView(id: string) {
  const next = views.value.filter((v) => v.id !== id);
  try {
    localStorage.setItem('lykeion.saved-views', JSON.stringify(next));
    views.value = next;
  } catch {
    viewError.value = 'Could not remove the saved view.';
  }
}
watch(
  display,
  (value) => {
    try {
      localStorage.setItem('lykeion.task-display', JSON.stringify(value));
      localStorage.setItem('lykeion.task-view', value.mode);
    } catch {
      notify('Display preferences could not be saved.');
    }
  },
  { deep: true },
);
const tableColumnNames = [
  'Task',
  'Identifier',
  'Status',
  'Priority',
  'Assignee',
  'Labels',
  'Project',
  'Start date',
  'Due date',
  'Created',
  'Updated',
  'Sub-task progress',
  'Created by',
];
const tablePrefs = reactive(
  read<{ columns: string[]; group: string; widths: Record<string, number> }>('lykeion.task-table', {
    columns: ['Task', 'Status', 'Priority', 'Assignee', 'Due date', 'Labels'],
    group: 'No grouping',
    widths: {},
  }),
);
const columnSearch = ref(''),
  tableSearch = ref(''),
  selectedRows = ref<string[]>([]),
  draggedColumn = ref(''),
  editingCell = ref(''),
  cellDraft = ref(''),
  savingCell = ref(false);
const tableTasks = computed(() => {
  const rows = tasks.value.filter((t) =>
    `${taskCode(t)} ${t.title}`.toLowerCase().includes(tableSearch.value.toLowerCase()),
  );
  if (!display.hierarchy) return rows;
  const result: Inquiry[] = [];
  const seen = new Set<string>();
  function visit(t: Inquiry) {
    if (seen.has(t.id)) return;
    seen.add(t.id);
    result.push(t);
    rows.filter((x) => x.parentId === t.id).forEach(visit);
  }
  rows.filter((t) => !t.parentId || !rows.some((p) => p.id === t.parentId)).forEach(visit);
  rows.forEach(visit);
  return result;
});
const collapsedLanes = ref<string[]>([]);
const swimColumns = computed(() =>
  columns.filter((x) => x !== 'Done' || tasks.value.some((t) => t.status === 'done')),
);
const swimLanes = computed(() => {
  const groups = new Map<string, Inquiry[]>();
  for (const t of tasks.value) {
    const name =
      display.swimGroup === 'Project'
        ? project(t.projectId)
        : display.swimGroup === 'Parent task'
          ? (all.value.find((p) => p.id === t.parentId)?.title ?? 'No parent task')
          : assignee(t.assigneeId);
    groups.set(name, [...(groups.get(name) ?? []), t]);
  }
  return [...groups].map(([name, tasks]) => ({ name, tasks }));
});
function toggleLane(name: string) {
  collapsedLanes.value = collapsedLanes.value.includes(name)
    ? collapsedLanes.value.filter((x) => x !== name)
    : [...collapsedLanes.value, name];
}
async function stageCellChange(t: Inquiry, event: Event) {
  const select = event.target as HTMLSelectElement;
  await updateCell(t, 'Status', select.value);
  select.value = status(all.value.find((x) => x.id === t.id) ?? t);
}

const tableGroups = computed(() => {
  if (tablePrefs.group === 'No grouping') return [{ name: '', tasks: tableTasks.value }];
  const groups = new Map<string, Inquiry[]>();
  for (const t of tableTasks.value) {
    const name =
      tablePrefs.group === 'Status'
        ? status(t)
        : tablePrefs.group === 'Assignee'
          ? assignee(t.assigneeId)
          : project(t.projectId);
    groups.set(name, [...(groups.get(name) ?? []), t]);
  }
  return [...groups].map(([name, tasks]) => ({ name, tasks }));
});
function columnWidth(name: string) {
  return (
    tablePrefs.widths[name] ??
    (
      {
        Task: 478,
        Status: 200,
        Priority: 172,
        Assignee: 240,
        'Due date': 185,
        Labels: 292,
      } as Record<string, number>
    )[name] ??
    190
  );
}
function toggleColumn(name: string) {
  if (name === 'Task') return;
  tablePrefs.columns = tablePrefs.columns.includes(name)
    ? tablePrefs.columns.filter((x) => x !== name)
    : [...tablePrefs.columns, name];
}
function reorderColumn(name: string) {
  if (name === 'Task' || draggedColumn.value === 'Task') return;
  const from = tablePrefs.columns.indexOf(draggedColumn.value),
    to = tablePrefs.columns.indexOf(name);
  if (from < 0 || to < 0) return;
  const next = [...tablePrefs.columns];
  next.splice(from, 1);
  next.splice(to, 0, draggedColumn.value);
  tablePrefs.columns = next;
  draggedColumn.value = '';
}
function resizeColumn(event: PointerEvent, name: string) {
  event.preventDefault();
  const start = event.clientX,
    width = columnWidth(name),
    handle = event.currentTarget as HTMLElement;
  handle.setPointerCapture(event.pointerId);
  const move = (e: PointerEvent) =>
    (tablePrefs.widths[name] = Math.max(110, width + e.clientX - start));
  const finish = () => {
    handle.removeEventListener('pointermove', move);
    handle.removeEventListener('pointerup', finish);
    handle.removeEventListener('pointercancel', finish);
  };
  handle.addEventListener('pointermove', move);
  handle.addEventListener('pointerup', finish, { once: true });
  handle.addEventListener('pointercancel', finish, { once: true });
}
function tableValue(t: Inquiry, name: string): string {
  switch (name) {
    case 'Task':
      return t.title;
    case 'Identifier':
      return taskCode(t);
    case 'Status':
      return status(t);
    case 'Priority':
      return t.priority === 'none' ? 'No priority' : t.priority;
    case 'Assignee':
      return assignee(t.assigneeId);
    case 'Project':
      return project(t.projectId);
    case 'Labels':
      return t.labels?.join(', ') || 'Empty';
    case 'Start date':
      return displayDate(t.startDate) || 'Start date';
    case 'Due date':
      return displayDate(t.dueDate) || 'Due date';
    case 'Created':
      return displayDate(t.createdAt);
    case 'Updated':
      return displayDate(t.updatedAt) || '—';
    case 'Created by':
      return state.data?.colleagues.find((x) => x.id === t.creatorId)?.name || 'Not recorded';
    case 'Sub-task progress': {
      const children = all.value.filter((x) => x.parentId === t.id);
      return children.length
        ? `${children.filter((x) => x.status === 'done').length}/${children.length}`
        : '—';
    }
    default:
      return '';
  }
}
function editCell(t: Inquiry, name: string) {
  editingCell.value = `${t.id}:${name}`;
  cellDraft.value =
    name === 'Labels'
      ? (t.labels ?? []).join(', ')
      : name === 'Due date'
        ? (t.dueDate ?? '')
        : name === 'Start date'
          ? (t.startDate ?? '')
          : t.title;
  nextTick(() => document.querySelector<HTMLInputElement>('.table-cell-editor')?.focus());
}
async function updateCell(t: Inquiry, name: string, value: string) {
  if (savingCell.value) return;
  savingCell.value = true;
  try {
    if (name === 'Status') {
      if (!stages.includes(value as Stage)) {
        notify('Operational status changes require the control plane.');
        return;
      }
      const receipt = await service.value.transition(t.id, t.revision, value as Stage);
      notify(receipt.accepted ? `Moved to ${value}` : receipt.reasons.join(' '));
    } else {
      const patch =
        name === 'Task'
          ? { title: value }
          : name === 'Priority'
            ? { priority: value as Inquiry['priority'] }
            : name === 'Assignee'
              ? { assigneeId: value || null }
              : name === 'Project'
                ? { projectId: value || null }
                : name === 'Labels'
                  ? {
                      labels: value
                        .split(',')
                        .map((x) => x.trim())
                        .filter(Boolean),
                    }
                  : name === 'Due date'
                    ? { dueDate: value || null }
                    : { startDate: value || null };
      await service.value.updateInquiry(t.id, t.revision, patch);
    }
    editingCell.value = '';
    await refresh();
  } catch (e) {
    notify((e as Error).message);
  } finally {
    savingCell.value = false;
  }
}
function toggleRow(id: string) {
  selectedRows.value = selectedRows.value.includes(id)
    ? selectedRows.value.filter((x) => x !== id)
    : [...selectedRows.value, id];
}
function exportTasks(format: 'csv' | 'json') {
  const records = selectedRows.value.length
    ? tableTasks.value.filter((t) => selectedRows.value.includes(t.id))
    : tableTasks.value;
  const csvCell = (value: string) =>
    '"' + (/^(?:\s*[=+@-]|[\t\r\n])/.test(value) ? "'" + value : value).replaceAll('"', '""') + '"';
  const content =
    format === 'json'
      ? JSON.stringify(records, null, 2)
      : [
          tablePrefs.columns,
          ...records.map((t) => tablePrefs.columns.map((name) => tableValue(t, name))),
        ]
          .map((row) => row.map(csvCell).join(','))
          .join('\r\n');
  const url = URL.createObjectURL(
    new Blob([content], {
      type: format === 'json' ? 'application/json' : 'text/csv;charset=utf-8',
    }),
  );
  const link = document.createElement('a');
  link.href = url;
  link.download = `lykeion-tasks.${format}`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
  closeMenus();
}
watch(
  tablePrefs,
  (value) => {
    try {
      localStorage.setItem('lykeion.task-table', JSON.stringify(value));
    } catch {
      notify('Table preferences could not be saved.');
    }
  },
  { deep: true },
);

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
  <div class="task-toolbar">
    <div class="toolbar-group">
      <button
        v-for="name in ['All', 'Members', 'Agents']"
        :key="name"
        class="button compact"
        :class="{ active: tab === name }"
        @click="tab = name"
      >
        {{ name }}
      </button>
      <div class="menu-anchor">
        <button
          class="icon-button"
          aria-label="Views"
          aria-haspopup="menu"
          @click="menu('views', $event)"
          @keydown.down.prevent="menu('views', $event)"
        >
          <Icon name="layers" :size="14" />
        </button>
        <div
          v-if="popup === 'views'"
          ref="menuRoot"
          class="board-menu views-menu"
          role="menu"
          aria-label="Views"
          @keydown="menuKeys"
        >
          <button role="menuitem" @click="newView"><Icon name="plus" />New view</button
          ><button
            role="menuitem"
            @click="
              viewDialog = 'manage';
              closeMenus();
            "
          >
            <Icon name="settings" />Manage views</button
          ><button v-for="view in views" :key="view.id" role="menuitem" @click="applyView(view)">
            {{ view.name }}
          </button>
        </div>
      </div>
    </div>
    <div class="toolbar-group push-right">
      <small v-if="workingOnly" class="working-caption">Viewing tasks with agents working</small>
      <div class="working-control">
        <button
          class="button compact"
          :class="{ 'working-active': workingOnly }"
          role="checkbox"
          :aria-checked="workingOnly"
          @click="workingOnly = !workingOnly"
        >
          {{ state.data?.agents.filter((x) => x.status === 'busy').length ?? 0 }} agents working
        </button>
        <div class="working-tooltip" role="tooltip">
          {{
            state.data?.agents.some((x) => x.status === 'busy')
              ? 'Filter by agents currently marked busy'
              : 'No agents working right now'
          }}
        </div>
      </div>
      <div class="menu-anchor">
        <button
          class="button compact"
          :class="{ active: activeFilterCount }"
          :aria-expanded="popup === 'filter'"
          aria-haspopup="menu"
          @click="menu('filter', $event)"
          @keydown.down.prevent="menu('filter', $event)"
        >
          <Icon name="filter" :size="14" />Filter<span v-if="activeFilterCount">{{
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
              @click="submenu = submenu === category ? null : category"
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
                  v-if="category === 'Status'"
                  class="stage-dot"
                  :class="'status-' + option.value.toLowerCase()"
                /><span>{{ option.label }}</span
                ><small
                  v-if="all.filter((t) => values(t, category).includes(option.value)).length"
                  class="push-right"
                  >{{ all.filter((t) => values(t, category).includes(option.value)).length }} task{{
                    all.filter((t) => values(t, category).includes(option.value)).length === 1
                      ? ''
                      : 's'
                  }}</small
                >
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
      <template v-if="display.mode === 'Table'"
        ><div class="menu-anchor">
          <button
            class="button compact"
            @click="menu('table-group', $event)"
            @keydown.down.prevent="menu('table-group', $event)"
          >
            <Icon name="list" :size="14" />Group<Icon name="down" :size="11" />
          </button>
          <div
            v-if="popup === 'table-group'"
            class="board-menu table-group-menu"
            role="menu"
            aria-label="Group"
            @keydown="menuKeys"
          >
            <button
              v-for="name in ['No grouping', 'Status', 'Assignee', 'Project']"
              :key="name"
              role="menuitemradio"
              :aria-checked="tablePrefs.group === name"
              @click="
                tablePrefs.group = name;
                closeMenus();
              "
            >
              {{ name
              }}<Icon v-if="tablePrefs.group === name" name="check" class="push-right" :size="14" />
            </button>
          </div>
        </div>
        <div class="menu-anchor">
          <button
            class="button compact"
            @click="menu('table-columns', $event)"
            @keydown.down.prevent="menu('table-columns', $event)"
          >
            <Icon name="columns" :size="14" />Columns<Icon name="down" :size="11" />
          </button>
          <div
            v-if="popup === 'table-columns'"
            class="board-menu table-columns-popover"
            role="menu"
            @keydown="menuKeys"
          >
            <input
              v-model="columnSearch"
              autofocus
              placeholder="Search columns…"
              aria-label="Search columns"
            /><small>Task properties</small
            ><button
              v-for="name in tableColumnNames.filter((x) =>
                x.toLowerCase().includes(columnSearch.toLowerCase()),
              )"
              :key="name"
              role="menuitemcheckbox"
              :aria-checked="tablePrefs.columns.includes(name)"
              :disabled="name === 'Task'"
              @click="toggleColumn(name)"
            >
              {{ name
              }}<Icon
                v-if="tablePrefs.columns.includes(name)"
                name="check"
                class="push-right"
                :size="14"
              />
            </button>
          </div></div
      ></template>
      <div class="menu-anchor">
        <button
          class="button compact"
          :aria-expanded="popup === 'display'"
          @click="menu('display', $event)"
          @keydown.down.prevent="menu('display', $event)"
        >
          <Icon name="filters" :size="14" />Display
        </button>
        <div
          v-if="popup === 'display'"
          ref="menuRoot"
          class="board-menu display-popover"
          :class="{ 'table-display': display.mode === 'Table' }"
          @keydown="menuKeys"
        >
          <label v-if="display.mode === 'Table'" class="display-row"
            ><span
              >Hierarchy<small class="hierarchy-help"
                >Nest sub-tasks beneath their parents</small
              ></span
            ><input
              v-model="display.hierarchy"
              type="checkbox"
              role="switch"
              aria-label="Hierarchy" /></label
          ><label v-else-if="display.mode === 'Swimlane'" class="display-row"
            >Grouping<select v-model="display.swimGroup" aria-label="Grouping">
              <option v-for="group in ['Parent task', 'Project', 'Assignee']" :key="group">
                {{ group }}
              </option>
            </select></label
          ><label v-else class="display-row"
            >Grouping<select v-model="display.grouping" aria-label="Grouping">
              <option
                v-for="group in ['Status', 'Priority', 'Assignee', 'Project', 'None']"
                :key="group"
              >
                {{ group }}
              </option>
            </select></label
          ><span class="display-caption">Ordering</span>
          <div class="order-row">
            <select v-model="display.order" aria-label="Ordering">
              <option
                v-for="order in ['Created date', 'Updated date', 'Title', 'Priority', 'Due date']"
                :key="order"
              >
                {{ order }}
              </option></select
            ><button @click="display.descending = !display.descending">
              {{ display.descending ? 'Newest first' : 'Oldest first' }}
            </button>
          </div>
          <label class="display-row"
            >Show sub-tasks<input
              v-model="display.subtasks"
              type="checkbox"
              role="switch"
              aria-label="Show sub-tasks" /></label
          ><span v-if="display.mode !== 'Table'" class="display-caption">Card properties</span>
          <div v-if="display.mode !== 'Table'" class="property-chips">
            <button
              v-for="name in propertyNames"
              :key="name"
              :aria-pressed="display.properties.includes(name)"
              @click="toggleProperty(name)"
            >
              {{ name }}
            </button>
          </div>
        </div>
      </div>
      <div class="menu-anchor">
        <button
          class="button compact"
          :aria-expanded="popup === 'mode'"
          @click="menu('mode', $event)"
          @keydown.down.prevent="menu('mode', $event)"
        >
          <component
            :is="layoutIcons[display.mode]"
            :size="14"
            :stroke-width="1.7"
            aria-hidden="true"
          />{{ display.mode }}
        </button>
        <div
          v-if="popup === 'mode'"
          ref="menuRoot"
          class="board-menu mode-menu"
          role="menu"
          @keydown="menuKeys"
        >
          <small class="mode-caption">View</small
          ><button
            v-for="name in ['Board', 'List', 'Table', 'Swimlane']"
            :key="name"
            role="menuitemradio"
            :aria-checked="display.mode === name"
            @click="
              display.mode = name;
              closeMenus();
            "
          >
            <component
              :is="layoutIcons[name]"
              :size="16"
              :stroke-width="1.7"
              aria-hidden="true"
            />{{ name }}<Icon v-if="display.mode === name" name="check" class="push-right" />
          </button>
        </div>
      </div>
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
  <div v-if="display.mode === 'Board' && tasks.length" class="board-scroll">
    <div
      v-for="column in boardColumns"
      :key="column.id"
      class="board-column"
      :class="
        display.grouping === 'Status' ? 'stage-' + column.id.toLowerCase() : 'stage-grounding'
      "
      @dragover.prevent
      @drop="drop($event, column.id)"
    >
      <header>
        <span class="stage-dot" /><strong>{{ column.name }}</strong
        ><span>{{ column.tasks.length }}</span
        ><button
          class="icon-button push-right"
          :aria-label="'New task in ' + column.name"
          @click="createFromColumn"
        >
          <Icon name="plus" :size="14" />
        </button>
      </header>
      <button
        v-for="task in column.tasks"
        :key="task.id"
        class="task-card"
        draggable="true"
        @contextmenu="openTaskContext(task, $event)"
        @keydown.shift.f10="openTaskContext(task, $event)"
        @dragstart="drag($event, task.id)"
        @click="open(task.id, $event)"
      >
        <small>{{ taskCode(task) }}</small
        ><span>{{ task.title }}</span>
        <p v-if="display.properties.includes('Description') && task.description">
          {{ task.description }}
        </p>
        <div class="card-metadata">
          <span v-if="display.properties.includes('Priority') && task.priority !== 'none'"
            ><Icon name="flag" :size="12" />{{ task.priority }}</span
          ><span v-if="display.properties.includes('Assignee') && task.assigneeId"
            ><Icon name="users" :size="12" />{{ assignee(task.assigneeId) }}</span
          ><span v-if="display.properties.includes('Project') && task.projectId"
            ><Icon name="folder" :size="12" />{{ project(task.projectId) }}</span
          ><span v-if="display.properties.includes('Start date') && task.startDate"
            >Starts {{ displayDate(task.startDate) }}</span
          ><span v-if="display.properties.includes('Due date') && task.dueDate"
            >Due {{ displayDate(task.dueDate) }}</span
          ><template v-if="display.properties.includes('Labels')"
            ><span v-for="label in task.labels" :key="label"
              ><Icon name="tag" :size="12" />{{ label }}</span
            ></template
          ><span
            v-if="
              display.properties.includes('Sub-task progress') &&
              all.some((x) => x.parentId === task.id)
            "
            >{{ all.filter((x) => x.parentId === task.id && x.status === 'done').length }}/{{
              all.filter((x) => x.parentId === task.id).length
            }}
            sub-tasks</span
          >
        </div>
      </button>
      <p v-if="!column.tasks.length" class="column-empty">No tasks</p>
    </div>
  </div>
  <section v-else-if="display.mode === 'Swimlane' && tasks.length" class="swimlane-scroll">
    <div class="swimlane-headings">
      <div
        v-for="column in swimColumns"
        :key="column"
        :class="'lane-status-' + column.toLowerCase()"
      >
        <span class="stage-dot" :class="'status-' + column.toLowerCase()" /><strong>{{
          column
        }}</strong
        ><small>{{ tasks.filter((t) => status(t) === column).length }}</small
        ><button
          class="icon-button push-right"
          :aria-label="'Add task in ' + column"
          @click="createFromColumn"
        >
          <Icon name="more" :size="14" />
        </button>
      </div>
    </div>
    <section v-for="lane in swimLanes" :key="lane.name" class="swimlane">
      <button
        class="lane-title"
        :aria-expanded="!collapsedLanes.includes(lane.name)"
        @click="toggleLane(lane.name)"
      >
        <Icon name="more" :size="12" /><Icon
          :name="collapsedLanes.includes(lane.name) ? 'right' : 'down'"
          :size="12"
        /><Icon
          :name="
            display.swimGroup === 'Project'
              ? 'folder'
              : display.swimGroup === 'Parent task'
                ? 'tasks'
                : 'users'
          "
          :size="18"
        /><strong>{{ lane.name }}</strong
        ><small>{{ lane.tasks.length }}</small>
      </button>
      <div v-if="!collapsedLanes.includes(lane.name)" class="swimlane-cells">
        <div
          v-for="column in swimColumns"
          :key="column"
          class="swimlane-cell"
          :class="'lane-status-' + column.toLowerCase()"
          @dragover.prevent
          @drop="drop($event, column)"
        >
          <button
            v-for="task in lane.tasks.filter((t) => status(t) === column)"
            :key="task.id"
            class="task-card"
            draggable="true"
            @contextmenu="openTaskContext(task, $event)"
            @keydown.shift.f10="openTaskContext(task, $event)"
            @dragstart="drag($event, task.id)"
            @click="open(task.id, $event)"
          >
            <small>{{ taskCode(task) }}</small
            ><span>{{ task.title }}</span>
            <p v-if="display.properties.includes('Description') && task.description">
              {{ task.description }}
            </p>
            <div class="card-metadata">
              <span v-if="display.properties.includes('Priority') && task.priority !== 'none'">{{
                task.priority
              }}</span
              ><span
                v-if="
                  display.properties.includes('Assignee') &&
                  task.assigneeId &&
                  display.swimGroup !== 'Assignee'
                "
                >{{ assignee(task.assigneeId) }}</span
              ><span
                v-if="
                  display.properties.includes('Project') &&
                  task.projectId &&
                  display.swimGroup !== 'Project'
                "
                >{{ project(task.projectId) }}</span
              ><span v-if="display.properties.includes('Start date') && task.startDate"
                >Starts {{ displayDate(task.startDate) }}</span
              ><span v-if="display.properties.includes('Due date') && task.dueDate"
                >Due {{ displayDate(task.dueDate) }}</span
              ><template v-if="display.properties.includes('Labels')"
                ><span v-for="label in task.labels" :key="label">{{ label }}</span></template
              >
            </div></button
          ><span v-if="!lane.tasks.some((t) => status(t) === column)" class="lane-dash">—</span
          ><button class="lane-add" :aria-label="'New task in ' + column" @click="createFromColumn">
            <Icon name="plus" :size="14" />
          </button>
        </div>
      </div>
    </section>
  </section>
  <section v-else-if="display.mode === 'Table'" class="table-workspace">
    <div class="table-actions">
      <label
        ><Icon name="search" :size="13" /><input
          v-model="tableSearch"
          type="search"
          placeholder="Search title or task ID…"
          aria-label="Search title or task ID" /></label
      ><small v-if="selectedRows.length">{{ selectedRows.length }} selected</small>
      <div class="menu-anchor push-right">
        <button class="export-button" @click="menu('export', $event)">
          <Icon name="download" :size="14" />Export<Icon name="down" :size="10" />
        </button>
        <div v-if="popup === 'export'" class="board-menu">
          <button @click="exportTasks('csv')">Export CSV</button
          ><button @click="exportTasks('json')">Export JSON</button>
        </div>
      </div>
    </div>
    <div class="table-scroll">
      <table class="tasks-table">
        <colgroup>
          <col style="width: 58px" />
          <col
            v-for="name in tablePrefs.columns"
            :key="name"
            :style="{ width: columnWidth(name) + 'px' }"
          />
          <col style="width: 64px" />
        </colgroup>
        <thead>
          <tr>
            <th>
              <input
                type="checkbox"
                aria-label="Select all visible tasks"
                :checked="
                  tableTasks.length > 0 && tableTasks.every((t) => selectedRows.includes(t.id))
                "
                @change="
                  selectedRows = tableTasks.every((t) => selectedRows.includes(t.id))
                    ? []
                    : tableTasks.map((t) => t.id)
                "
              />
            </th>
            <th
              v-for="name in tablePrefs.columns"
              :key="name"
              :draggable="name !== 'Task'"
              @dragstart="draggedColumn = name"
              @dragover.prevent
              @drop="reorderColumn(name)"
            >
              <button
                @click="
                  display.order =
                    name === 'Task'
                      ? 'Title'
                      : name === 'Created'
                        ? 'Created date'
                        : name === 'Updated'
                          ? 'Updated date'
                          : name === 'Due date'
                            ? 'Due date'
                            : name === 'Priority'
                              ? 'Priority'
                              : display.order;
                  display.descending = !display.descending;
                "
              >
                {{ name }}</button
              ><span
                class="column-resizer"
                role="separator"
                :aria-label="'Resize ' + name + ' column'"
                aria-orientation="vertical"
                tabindex="0"
                :aria-valuenow="columnWidth(name)"
                @pointerdown="resizeColumn($event, name)"
                @keydown.left.prevent="
                  tablePrefs.widths[name] = Math.max(110, columnWidth(name) - 16)
                "
                @keydown.right.prevent="tablePrefs.widths[name] = columnWidth(name) + 16"
              />
            </th>
            <th>
              <button
                aria-label="Add column"
                @click="menu('table-columns', $event)"
                @keydown.down.prevent="menu('table-columns', $event)"
              >
                <Icon name="plus" :size="14" />
              </button>
            </th>
          </tr>
        </thead>
        <tbody v-for="group in tableGroups" :key="group.name">
          <tr v-if="group.name" class="table-group-row">
            <td :colspan="tablePrefs.columns.length + 2">
              {{ group.name }} <small>{{ group.tasks.length }}</small>
            </td>
          </tr>
          <tr
            v-for="task in group.tasks"
            :key="task.id"
            :class="{ selected: selectedRows.includes(task.id) }"
            @contextmenu="openTaskContext(task, $event)"
          >
            <td>
              <input
                type="checkbox"
                :aria-label="'Select ' + taskCode(task)"
                :checked="selectedRows.includes(task.id)"
                @change="toggleRow(task.id)"
              />
            </td>
            <td v-for="name in tablePrefs.columns" :key="name">
              <form
                v-if="editingCell === task.id + ':' + name"
                @submit.prevent="updateCell(task, name, cellDraft)"
              >
                <input
                  v-model="cellDraft"
                  class="table-cell-editor"
                  :aria-label="'Edit ' + name"
                  :type="name.includes('date') ? 'date' : 'text'"
                  @keydown.esc="editingCell = ''"
                  @blur="updateCell(task, name, cellDraft)"
                />
              </form>
              <div
                v-else-if="name === 'Task'"
                class="table-title"
                :class="{ 'nested-task': display.hierarchy && task.parentId }"
              >
                <small>{{ taskCode(task) }}</small
                ><button @click="open(task.id, $event)">{{ task.title }}</button
                ><button class="rename-task" aria-label="Rename task" @click="editCell(task, name)">
                  <Icon name="edit" :size="13" />
                </button>
              </div>
              <select
                v-else-if="name === 'Status'"
                :value="status(task)"
                :aria-label="'Status for ' + taskCode(task)"
                :disabled="savingCell"
                @change="stageCellChange(task, $event)"
              >
                <option v-for="value in columns" :key="value">{{ value }}</option></select
              ><select
                v-else-if="name === 'Priority'"
                :value="task.priority"
                :aria-label="'Priority for ' + taskCode(task)"
                :disabled="savingCell"
                @change="updateCell(task, name, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="value in priorities" :key="value" :value="value">
                  {{ value === 'none' ? 'No priority' : value }}
                </option></select
              ><select
                v-else-if="name === 'Assignee'"
                :value="task.assigneeId ?? ''"
                :aria-label="'Assignee for ' + taskCode(task)"
                :disabled="savingCell"
                @change="updateCell(task, name, ($event.target as HTMLSelectElement).value)"
              >
                <option value="">Unassigned</option>
                <option
                  v-for="option in options('Assignee').filter((x) => x.value !== 'none')"
                  :key="option.value"
                  :value="option.value"
                >
                  {{ option.label }}
                </option></select
              ><select
                v-else-if="name === 'Project'"
                :value="task.projectId ?? ''"
                :aria-label="'Project for ' + taskCode(task)"
                :disabled="savingCell"
                @change="updateCell(task, name, ($event.target as HTMLSelectElement).value)"
              >
                <option value="">No project</option>
                <option v-for="item in state.data?.projects" :key="item.id" :value="item.id">
                  {{ item.name }}
                </option></select
              ><button
                v-else-if="['Labels', 'Start date', 'Due date'].includes(name)"
                class="table-edit-button"
                @click="editCell(task, name)"
              >
                {{ tableValue(task, name) }}</button
              ><span v-else>{{ tableValue(task, name) }}</span>
            </td>
            <td />
          </tr>
        </tbody>
      </table>
      <p v-if="!tableTasks.length" class="table-empty">No tasks found.</p>
    </div>
  </section>
  <div v-else-if="tasks.length" class="task-list">
    <button
      v-for="task in tasks"
      :key="task.id"
      @contextmenu="openTaskContext(task, $event)"
      @keydown.shift.f10="openTaskContext(task, $event)"
      @click="open(task.id, $event)"
    >
      <span class="stage-dot" /><small>{{ taskCode(task) }}</small
      ><span>{{ task.title }}</span
      ><span class="push-right muted">{{ status(task) }}</span>
    </button>
  </div>
  <div v-else class="center-empty board-empty">
    <FunnelX
      v-if="activeFilterCount || workingOnly"
      :size="40"
      :stroke-width="1.7"
      aria-hidden="true"
    /><Icon v-else name="tasks" :size="40" /><span>{{
      activeFilterCount || workingOnly ? 'No tasks match these filters' : 'No tasks yet'
    }}</span
    ><button
      v-if="activeFilterCount || workingOnly"
      class="button compact"
      @click="
        filters = {};
        workingOnly = false;
      "
    >
      Clear filters
    </button>
  </div>
  <TaskContextMenu
    v-if="taskContext && contextTask"
    :key="taskContext.id"
    :task="contextTask"
    :x="taskContext.x"
    :y="taskContext.y"
    @close="taskContext = null"
  />
  <Modal
    v-if="viewDialog"
    :title="viewDialog === 'new' ? 'Save as view' : 'Manage views'"
    class="view-dialog"
    @close="viewDialog = ''"
    ><form v-if="viewDialog === 'new'" @submit.prevent="saveView">
      <header>
        <span>Save as view<small>Based on all workspace tasks</small></span
        ><button
          type="button"
          class="icon-button push-right"
          aria-label="Close"
          @click="viewDialog = ''"
        >
          <Icon name="close" />
        </button>
      </header>
      <div class="view-fields">
        <label
          ><span>Name</span
          ><input v-model="viewName" required autofocus placeholder="e.g. Needs review" /></label
        ><label
          ><span>Visibility</span
          ><select disabled>
            <option>Only visible to me</option>
          </select></label
        ><label
          ><span>Assignee type</span
          ><select v-model="viewTab">
            <option value="All">Any assignee</option>
            <option value="Members">Members</option>
            <option value="Agents">Agents</option>
          </select></label
        >
        <div class="view-filter-row">
          <span>Filters</span>
          <div>
            <button
              type="button"
              class="button compact"
              @click="viewChoice = viewChoice ? '' : 'add'"
            >
              <Icon name="plus" :size="12" />Add filter
            </button>
            <div v-if="viewChoice" class="view-filter-controls">
              <select v-model="viewCategory">
                <option v-for="category in categories" :key="category">
                  {{ category }}
                </option></select
              ><select
                @change="
                  viewFilters = {
                    ...viewFilters,
                    [viewCategory]: [($event.target as HTMLSelectElement).value],
                  }
                "
              >
                <option value="">Select value</option>
                <option
                  v-for="option in options(viewCategory)"
                  :key="option.value"
                  :value="option.value"
                >
                  {{ option.label }}
                </option>
              </select>
            </div>
            <small v-for="(selected, category) in viewFilters" :key="category"
              >{{ category }}: {{ selected?.join(', ') }}</small
            >
          </div>
        </div>
        <details>
          <summary>
            Default display
            <span
              >{{ display.mode }} · {{ display.grouping }} · {{ display.order }} ·
              {{ display.descending ? 'Newest first' : 'Oldest first' }}</span
            >
          </summary>
          <p>The current display is saved with this view on this device.</p>
        </details>
      </div>
      <p v-if="viewError" class="form-error" role="alert">{{ viewError }}</p>
      <footer>
        <button type="button" class="text-button push-right" @click="viewDialog = ''">Cancel</button
        ><button class="button primary compact" :disabled="!viewName.trim()">Create view</button>
      </footer>
    </form>
    <div v-else class="manage-views">
      <header>
        Manage views<button
          class="icon-button push-right"
          aria-label="Close"
          @click="viewDialog = ''"
        >
          <Icon name="close" />
        </button>
      </header>
      <p v-if="!views.length">No saved views on this device.</p>
      <div v-for="view in views" :key="view.id">
        <button @click="applyView(view)">{{ view.name }}</button
        ><button class="text-button" @click="removeView(view.id)">Delete</button>
      </div>
      <p v-if="viewError" role="alert">{{ viewError }}</p>
    </div></Modal
  >
</template>
<style scoped>
.board-menu {
  position: absolute;
  z-index: 40;
  top: calc(100% + 4px);
  right: 0;
  background: var(--menu-bg, #e1e1e6);
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
  left: 100%;
  right: auto;
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
.status-reporting,
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
  width: 288px;
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
  background: #aaa;
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
.property-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.property-chips > button {
  font-size: 12px;
  line-height: 18px;
  padding: 2px 8px;
  border: 1px solid #ffffff30;
  border-radius: 5px;
  color: var(--muted);
}
.property-chips > button[aria-pressed='true'] {
  background: #00000005;
  color: var(--text);
  border-color: #00000002;
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
.view-dialog {
  width: 578px;
  background: #e1e1e6;
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
.tasks-table {
  table-layout: fixed;
  border-collapse: collapse;
  font-size: 14px;
  min-width: 100%;
}
.tasks-table th,
.tasks-table td {
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
.tasks-table th {
  background: #f5f5f7;
  font-weight: 400;
  font-size: 12px;
  letter-spacing: 0.3px;
  color: var(--muted);
  overflow: visible;
}
.tasks-table th > button {
  font: inherit;
  padding: 0;
}
.tasks-table th:first-child,
.tasks-table td:first-child {
  text-align: center;
  padding: 0;
  width: 58px;
}
.tasks-table input[type='checkbox'] {
  width: 14px;
  height: 14px;
  accent-color: var(--primary);
}
.tasks-table td > select {
  appearance: none;
  border: 0;
  background: transparent;
  font: inherit;
  width: 100%;
  outline-offset: 4px;
  padding: 4px 0;
  cursor: pointer;
}
.tasks-table tbody tr.selected {
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
.table-title > button:not(.rename-task) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
}
.table-title.nested-task {
  padding-left: 20px;
}
.table-title .rename-task {
  margin-left: auto;
  opacity: 0;
  flex: none;
}
.tasks-table tr:hover .rename-task,
.rename-task:focus-visible {
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
  outline: 2px solid #4183b0;
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
  background: #fdfbf4;
}
.swimlane-headings strong {
  font-weight: 500;
}
.swimlane-headings small {
  color: var(--muted);
  font-size: 12px;
}
.lane-status-grounding {
  background: #f3f3f5 !important;
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
  background: #fdfbf4;
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
  color: white !important;
  border-color: var(--primary) !important;
}
.working-tooltip {
  width: 288px;
  display: none;
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  background: #e1e1e6;
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
</style>
