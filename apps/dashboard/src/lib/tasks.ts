import {
  stages,
  type Inquiry,
  type WorkspaceSnapshot,
} from '../../../../packages/contracts/src/research';
import { parseDateKey, shortDate } from './dates';

// The slice of the workspace that task views look names and relations up in.
export type Directory = Pick<
  WorkspaceSnapshot,
  'workspace' | 'inquiries' | 'agents' | 'colleagues' | 'projects'
>;
type Lookup = Directory | null | undefined;

export const filterCategories = [
  'Status',
  'Priority',
  'Date',
  'Assignee',
  'Creator',
  'Project',
  'Project status',
  'Label',
] as const;
export type FilterCategory = (typeof filterCategories)[number];
export type Filters = Partial<Record<FilterCategory, string[]>>;

export const priorities = ['urgent', 'high', 'medium', 'low', 'none'];
export const statusColumns = [...stages.slice(0, 4), 'Blocked', 'Reporting', 'Done'];

export function taskStatus(t: Inquiry) {
  return t.status === 'active' ? t.stage : t.status === 'done' ? 'Done' : 'Blocked';
}

export function priorityLabel(priority: string) {
  return priority === 'none' ? 'No priority' : priority[0]!.toUpperCase() + priority.slice(1);
}

export function taskCode(t: Inquiry, dir: Lookup) {
  const index = dir?.inquiries.findIndex((x) => x.id === t.id) ?? -1;
  return `${dir?.workspace.taskPrefix || 'LYK'}-${index + 1}`;
}

export function assigneeName(id: string | null, dir: Lookup) {
  return (
    dir?.agents.find((x) => x.id === id)?.name ??
    dir?.colleagues.find((x) => x.id === id)?.name ??
    (id ? 'Unknown assignee' : 'Unassigned')
  );
}

export function projectName(id: string | null, dir: Lookup) {
  return dir?.projects.find((x) => x.id === id)?.name ?? 'No project';
}

// "Due today" is the 24 hours after local midnight; earlier is overdue.
export function dueBucket(dueDate: string | null | undefined, now = new Date()) {
  if (!dueDate) return 'No due date';
  // A bare date names a local calendar day; Date would read it as UTC midnight.
  const due = /^\d{4}-\d{2}-\d{2}$/.test(dueDate) ? parseDateKey(dueDate) : new Date(dueDate);
  const day = (d: Date) => d.getFullYear() * 10000 + d.getMonth() * 100 + d.getDate();
  const diff = day(due) - day(now);
  return diff < 0 ? 'Overdue' : diff === 0 ? 'Due today' : 'Upcoming';
}

// The values a task carries in a filter category; a filter option matches any of them.
export function filterValues(t: Inquiry, category: FilterCategory, dir: Lookup): string[] {
  switch (category) {
    case 'Status':
      return [taskStatus(t)];
    case 'Priority':
      return [t.priority];
    case 'Assignee':
      return [t.assigneeId ?? 'none'];
    case 'Creator':
      return [t.creatorId ?? 'unknown'];
    case 'Project':
      return [t.projectId ?? 'none'];
    case 'Project status':
      return [dir?.projects.find((p) => p.id === t.projectId)?.status ?? 'unknown'];
    case 'Label':
      return t.labels?.length ? t.labels : ['none'];
    case 'Date':
      return [dueBucket(t.dueDate)];
  }
}

export function filterOptions(
  category: FilterCategory,
  dir: Lookup,
): { value: string; label: string }[] {
  switch (category) {
    case 'Status':
      return [...statusColumns, 'Cancelled'].map((x) => ({ value: x, label: x }));
    case 'Priority':
      return priorities.map((x) => ({ value: x, label: priorityLabel(x) }));
    case 'Date':
      return ['Overdue', 'Due today', 'Upcoming', 'No due date'].map((x) => ({
        value: x,
        label: x,
      }));
    case 'Assignee':
      return [
        { value: 'none', label: 'Unassigned' },
        ...[...(dir?.agents ?? []), ...(dir?.colleagues ?? [])].map((x) => ({
          value: x.id,
          label: x.name,
        })),
      ];
    case 'Creator':
      return [
        { value: 'unknown', label: 'Not recorded' },
        ...(dir?.colleagues ?? []).map((x) => ({ value: x.id, label: x.name })),
      ];
    case 'Project':
      return [
        { value: 'none', label: 'No project' },
        ...(dir?.projects ?? []).map((x) => ({ value: x.id, label: x.name })),
      ];
    case 'Project status':
      return [
        { value: 'unknown', label: 'Not recorded' },
        ...[
          ...new Set((dir?.projects ?? []).map((x) => x.status).filter((x): x is string => !!x)),
        ].map((x) => ({ value: x, label: x })),
      ];
    case 'Label':
      return [
        { value: 'none', label: 'No labels' },
        ...[...new Set((dir?.inquiries ?? []).flatMap((x) => x.labels ?? []))].map((x) => ({
          value: x,
          label: x,
        })),
      ];
  }
}

// Categories combine with AND, the values selected within one category with OR.
export function matchesFilters(t: Inquiry, filters: Filters, dir: Lookup) {
  return Object.entries(filters).every(
    ([key, selected]) =>
      !selected?.length ||
      selected.some((value) => filterValues(t, key as FilterCategory, dir).includes(value)),
  );
}

export function sortTasks(rows: Inquiry[], order: string, descending: boolean) {
  return [...rows].sort((a, b) => {
    const comparison =
      order === 'Title'
        ? a.title.localeCompare(b.title)
        : order === 'Priority'
          ? priorities.indexOf(a.priority) - priorities.indexOf(b.priority)
          : order === 'Due date'
            ? (a.dueDate ?? '9999').localeCompare(b.dueDate ?? '9999')
            : order === 'Updated date'
              ? (a.updatedAt ?? a.createdAt).localeCompare(b.updatedAt ?? b.createdAt)
              : a.createdAt.localeCompare(b.createdAt);
    return comparison * (descending ? -1 : 1);
  });
}

// The Done column only appears once some task is done.
export function visibleStatusColumns(tasks: Inquiry[]) {
  return statusColumns.filter((x) => x !== 'Done' || tasks.some((t) => t.status === 'done'));
}

// Board columns keep every group, empty ones included, in a fixed order.
export function boardGroups(tasks: Inquiry[], grouping: string, dir: Lookup) {
  switch (grouping) {
    case 'Status':
      return visibleStatusColumns(tasks).map((name) => ({
        id: name,
        name,
        tasks: tasks.filter((t) => taskStatus(t) === name),
      }));
    case 'Priority':
      return priorities.map((id) => ({
        id,
        name: priorityLabel(id),
        tasks: tasks.filter((t) => t.priority === id),
      }));
    case 'Assignee':
      return [
        { id: 'none', name: 'Unassigned' },
        ...[...(dir?.agents ?? []), ...(dir?.colleagues ?? [])].map((x) => ({
          id: x.id,
          name: x.name,
        })),
      ].map((group) => ({
        ...group,
        tasks: tasks.filter((t) => (t.assigneeId ?? 'none') === group.id),
      }));
    case 'Project':
      return [{ id: 'none', name: 'No project' }, ...(dir?.projects ?? [])].map((group) => ({
        id: group.id,
        name: group.name,
        tasks: tasks.filter((t) => (t.projectId ?? 'none') === group.id),
      }));
    default:
      return [{ id: 'all', name: 'All tasks', tasks }];
  }
}

// Groups by display name in first-seen order; records that share a name share a group.
export function groupByName(tasks: Inquiry[], nameOf: (t: Inquiry) => string) {
  const groups = new Map<string, Inquiry[]>();
  for (const t of tasks) groups.set(nameOf(t), [...(groups.get(nameOf(t)) ?? []), t]);
  return [...groups].map(([name, tasks]) => ({ name, tasks }));
}

// Depth-first: each task is followed by its sub-tasks. Tasks whose parent is not in the list
// start a tree of their own, and anything left over (a cycle) is appended once.
export function hierarchyOrder(rows: Inquiry[]) {
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
}

// The text a table column shows, which is also what CSV export writes.
export function tableValue(t: Inquiry, column: string, dir: Lookup): string {
  switch (column) {
    case 'Task':
      return t.title;
    case 'Identifier':
      return taskCode(t, dir);
    case 'Status':
      return taskStatus(t);
    case 'Priority':
      return t.priority === 'none' ? 'No priority' : t.priority;
    case 'Assignee':
      return assigneeName(t.assigneeId, dir);
    case 'Project':
      return projectName(t.projectId, dir);
    case 'Labels':
      return t.labels?.join(', ') || 'Empty';
    case 'Start date':
      return shortDate(t.startDate) || 'Start date';
    case 'Due date':
      return shortDate(t.dueDate) || 'Due date';
    case 'Created':
      return shortDate(t.createdAt);
    case 'Updated':
      return shortDate(t.updatedAt) || '—';
    case 'Created by':
      return dir?.colleagues.find((x) => x.id === t.creatorId)?.name || 'Not recorded';
    case 'Sub-task progress': {
      const children = (dir?.inquiries ?? []).filter((x) => x.parentId === t.id);
      return children.length
        ? `${children.filter((x) => x.status === 'done').length}/${children.length}`
        : '—';
    }
    default:
      return '';
  }
}

// The update an edited table cell sends; empty text clears a field.
export function cellPatch(
  column: string,
  value: string,
): Partial<
  Pick<
    Inquiry,
    'title' | 'priority' | 'assigneeId' | 'projectId' | 'labels' | 'dueDate' | 'startDate'
  >
> {
  switch (column) {
    case 'Task':
      return { title: value };
    case 'Priority':
      return { priority: value as Inquiry['priority'] };
    case 'Assignee':
      return { assigneeId: value || null };
    case 'Project':
      return { projectId: value || null };
    case 'Labels':
      return {
        labels: value
          .split(',')
          .map((x) => x.trim())
          .filter(Boolean),
      };
    case 'Due date':
      return { dueDate: value || null };
    default:
      return { startDate: value || null };
  }
}
