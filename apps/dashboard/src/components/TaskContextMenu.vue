<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { Pin, Trash2, CalendarDays, CalendarClock, CircleSlash, Minus } from '@lucide/vue';
import { stages, type Inquiry, type Stage } from '../../../../packages/contracts/src/research';
import { service, state, refresh, notify } from '../services/workspace';
import Icon from './Icon.vue';
import { usePins } from '../state/pins';
import { useWorkspaceTabs } from '../state/workspace-tabs';
import { useTaskComposer } from '../state/task-composer';
const props = defineProps<{ task: Inquiry; x: number; y: number }>();
const emit = defineEmits<{ close: [] }>();
const menu = ref<HTMLElement>(),
  submenu = ref(''),
  query = ref(''),
  busy = ref(false),
  month = ref(new Date(new Date().getFullYear(), new Date().getMonth(), 1));
const { pins: pinned, togglePin: savePin } = usePins();
const { openTab: addTab } = useWorkspaceTabs();
const { openSubtaskComposer } = useTaskComposer();
const left = computed(() => Math.max(8, Math.min(props.x, window.innerWidth - 212))),
  top = computed(() => Math.max(8, Math.min(props.y, window.innerHeight - 354))),
  opensLeft = computed(() => left.value + 450 > window.innerWidth);
const priorities = [
  { id: 'none', name: 'No priority' },
  { id: 'urgent', name: 'Urgent' },
  { id: 'high', name: 'High' },
  { id: 'medium', name: 'Medium' },
  { id: 'low', name: 'Low' },
] as const;
const assignees = computed(() =>
  [
    { id: '', name: 'Unassigned' },
    ...(state.data?.agents ?? []),
    ...(state.data?.colleagues ?? []),
  ].filter((x) => x.name.toLowerCase().includes(query.value.toLowerCase())),
);
const days = computed(() => {
  const first = new Date(month.value.getFullYear(), month.value.getMonth(), 1);
  const start = new Date(first);
  start.setDate(1 - first.getDay());
  return Array.from(
    {
      length:
        35 +
        (new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate() + first.getDay() > 35
          ? 7
          : 0),
    },
    (_, i) => {
      const date = new Date(start);
      date.setDate(start.getDate() + i);
      return {
        date,
        value: `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`,
        current: date.getMonth() === first.getMonth(),
      };
    },
  );
});
const parent = computed(() => state.data?.inquiries.find((x) => x.id === props.task.parentId));
const children = computed(
  () => state.data?.inquiries.filter((x) => x.parentId === props.task.id) ?? [],
);
async function change(field: string, value: string) {
  if (busy.value) return;
  busy.value = true;
  try {
    if (field === 'Status') {
      if (!stages.includes(value as Stage)) {
        notify('Operational status changes require the control plane.');
        return;
      }
      const result = await service.value.transition(
        props.task.id,
        props.task.revision,
        value as Stage,
      );
      notify(result.accepted ? `Moved to ${value}` : result.reasons.join(' '));
      if (!result.accepted) return;
    } else
      await service.value.updateInquiry(
        props.task.id,
        props.task.revision,
        field === 'Priority'
          ? { priority: value as Inquiry['priority'] }
          : field === 'Assignee'
            ? { assigneeId: value || null }
            : field === 'Start date'
              ? { startDate: value || null }
              : { dueDate: value || null },
      );
    emit('close');
    await refresh();
  } catch (e) {
    notify((e as Error).message);
  } finally {
    busy.value = false;
  }
}
function togglePin() {
  if (savePin(props.task.id)) emit('close');
  else notify('Could not save the sidebar pin.');
}
async function copyLink() {
  try {
    const link = new URL(window.location.href);
    link.hash = '/tasks/' + encodeURIComponent(props.task.id);
    await navigator.clipboard.writeText(link.href);
    notify('Task link copied.');
    emit('close');
  } catch {
    notify('Clipboard access is unavailable. The task link was not copied.');
  }
}
function openTab(id = props.task.id, title = props.task.title) {
  addTab('/tasks/' + id, title, 'tasks');
  emit('close');
}
function child() {
  openSubtaskComposer(props.task.id);
  emit('close');
}
function unsupported(message: string) {
  notify(message);
  emit('close');
}
async function expand(name: string, focus = false) {
  submenu.value = name;
  query.value = '';
  if (focus) {
    await nextTick();
    menu.value
      ?.querySelector<HTMLElement>('.context-submenu button,.context-submenu input')
      ?.focus();
  }
}
function key(e: KeyboardEvent) {
  const day = (e.target as HTMLElement).closest<HTMLElement>('[data-calendar-day]');
  if (day && ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) {
    e.preventDefault();
    const cells = [
      ...(menu.value?.querySelectorAll<HTMLButtonElement>('[data-calendar-day]') ?? []),
    ];
    const index = cells.indexOf(day as HTMLButtonElement),
      delta =
        e.key === 'ArrowLeft' ? -1 : e.key === 'ArrowRight' ? 1 : e.key === 'ArrowUp' ? -7 : 7;
    cells[Math.max(0, Math.min(cells.length - 1, index + delta))]?.focus();
    return;
  }
  if (e.key === 'Escape') {
    e.preventDefault();
    e.stopPropagation();
    if (submenu.value) {
      const name = submenu.value;
      submenu.value = '';
      nextTick(() => menu.value?.querySelector<HTMLElement>(`[data-submenu="${name}"]`)?.focus());
    } else emit('close');
  }
  if (e.key === 'ArrowRight' && (e.target as HTMLElement).dataset.submenu) {
    e.preventDefault();
    expand((e.target as HTMLElement).dataset.submenu!, true);
  }
  if (e.key === 'ArrowLeft' && submenu.value) {
    e.preventDefault();
    const name = submenu.value;
    submenu.value = '';
    nextTick(() => menu.value?.querySelector<HTMLElement>(`[data-submenu="${name}"]`)?.focus());
  }
  if (
    ['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(e.key) &&
    !(e.target instanceof HTMLInputElement)
  ) {
    e.preventDefault();
    const scope = (e.target as HTMLElement).closest('[role=menu]');
    const items = [
      ...(scope?.querySelectorAll<HTMLButtonElement>(
        ':scope>button:not(:disabled),:scope>.context-group>button:not(:disabled)',
      ) ?? []),
    ];
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    items[
      e.key === 'Home'
        ? 0
        : e.key === 'End'
          ? items.length - 1
          : (index + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
    ]?.focus();
  }
  if (e.key === 'Tab') emit('close');
}
function outside(e: PointerEvent) {
  if (!menu.value?.contains(e.target as Node)) emit('close');
}
const previous = document.activeElement as HTMLElement | null;
onMounted(() => {
  menu.value?.querySelector<HTMLButtonElement>('button')?.focus();
  window.addEventListener('pointerdown', outside);
});
onUnmounted(() => {
  window.removeEventListener('pointerdown', outside);
  previous?.focus();
});
</script>
<template>
  <Teleport to="body"
    ><div
      ref="menu"
      class="task-context"
      role="menu"
      aria-label="Task actions"
      :style="{ left: left + 'px', top: top + 'px' }"
      :class="{ 'opens-left': opensLeft }"
      @keydown="key"
    >
      <div
        v-for="name in ['Status', 'Priority', 'Assignee', 'Start date', 'Due date']"
        :key="name"
        class="context-group"
      >
        <button
          role="menuitem"
          :data-submenu="name"
          :aria-expanded="submenu === name"
          aria-haspopup="menu"
          @mouseenter="expand(name)"
          @click="expand(name, true)"
        >
          <CalendarClock v-if="name === 'Start date'" :size="15" /><CalendarDays
            v-else-if="name === 'Due date'"
            :size="15"
          /><CircleSlash
            v-else-if="name === 'Status' && task.status === 'blocked'"
            class="blocked-icon"
            :size="15"
          /><Minus v-else-if="name === 'Priority' && task.priority === 'none'" :size="15" /><Icon
            v-else
            :name="name === 'Status' ? 'circle' : name === 'Priority' ? 'flag' : 'users'"
            :size="15"
          />{{ name }}<Icon v-if="name !== 'Assignee'" name="right" :size="12" class="push-right" />
        </button>
        <div v-if="submenu === name" class="context-submenu" role="menu" :aria-label="name">
          <template v-if="name === 'Status'"
            ><button
              v-for="value in [...stages.slice(0, 4), 'Blocked', 'Reporting', 'Done', 'Cancelled']"
              :key="value"
              role="menuitemradio"
              :aria-checked="
                (task.status === 'active'
                  ? task.stage
                  : task.status === 'done'
                    ? 'Done'
                    : 'Blocked') === value
              "
              :disabled="busy"
              @click="change(name, value)"
            >
              <Icon name="circle" :size="14" />{{ value
              }}<Icon
                v-if="
                  (task.status === 'active'
                    ? task.stage
                    : task.status === 'done'
                      ? 'Done'
                      : 'Blocked') === value
                "
                name="check"
                class="push-right"
                :size="12"
              /></button
          ></template>
          <template v-else-if="name === 'Priority'"
            ><button
              v-for="value in priorities"
              :key="value.id"
              role="menuitemradio"
              :aria-checked="task.priority === value.id"
              :disabled="busy"
              @click="change(name, value.id)"
            >
              <Icon name="flag" :size="14" />{{ value.name
              }}<Icon
                v-if="task.priority === value.id"
                name="check"
                class="push-right"
                :size="12"
              /></button
          ></template>
          <template v-else-if="name === 'Assignee'"
            ><input
              v-model="query"
              placeholder="Search assignees…"
              aria-label="Search assignees"
            /><button
              v-for="value in assignees"
              :key="value.id"
              role="menuitemradio"
              :aria-checked="(task.assigneeId ?? '') === value.id"
              :disabled="busy"
              @click="change(name, value.id)"
            >
              <Icon name="users" :size="14" />{{ value.name
              }}<Icon
                v-if="(task.assigneeId ?? '') === value.id"
                name="check"
                class="push-right"
                :size="12"
              /></button
            ><small v-if="!assignees.length">No matching assignees</small></template
          >
          <template v-else
            ><button role="menuitem" @click="change(name, '')">
              No {{ name.toLowerCase()
              }}<Icon
                v-if="!(name === 'Start date' ? task.startDate : task.dueDate)"
                name="check"
                class="push-right"
                :size="12"
              />
            </button>
            <div class="calendar-head">
              <button
                aria-label="Previous month"
                @click="month = new Date(month.getFullYear(), month.getMonth() - 1, 1)"
              >
                <Icon name="left" :size="14" /></button
              ><span>{{
                month.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })
              }}</span
              ><button
                aria-label="Next month"
                @click="month = new Date(month.getFullYear(), month.getMonth() + 1, 1)"
              >
                <Icon name="right" :size="14" />
              </button>
            </div>
            <div class="calendar-days">
              <small v-for="day in ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa']" :key="day">{{
                day
              }}</small
              ><button
                v-for="day in days"
                :key="day.value"
                data-calendar-day
                :class="{
                  outside: !day.current,
                  selected: (name === 'Start date' ? task.startDate : task.dueDate) === day.value,
                }"
                :aria-label="day.value"
                :disabled="busy"
                @click="change(name, day.value)"
              >
                {{ day.date.getDate() }}
              </button>
            </div></template
          >
        </div>
      </div>
      <div class="context-divider" />
      <button role="menuitem" @mouseenter="submenu = ''" @click="openTab()">
        <Icon name="external" :size="15" />Open in new tab</button
      ><button role="menuitem" @mouseenter="submenu = ''" @click="togglePin">
        <Pin :size="15" />{{
          pinned.includes(task.id) ? 'Unpin from sidebar' : 'Pin to sidebar'
        }}</button
      ><button role="menuitem" @mouseenter="submenu = ''" @click="copyLink">
        <Icon name="link" :size="15" />Copy link</button
      ><button
        role="menuitem"
        @mouseenter="submenu = ''"
        @click="
          unsupported('No local workdir was supplied by an execution adapter. Nothing was copied.')
        "
      >
        <Icon name="folder" :size="15" />Copy local workdir path
      </button>
      <div class="context-divider" />
      <div class="context-group">
        <button
          role="menuitem"
          data-submenu="Relations"
          aria-haspopup="menu"
          :aria-expanded="submenu === 'Relations'"
          @mouseenter="expand('Relations')"
          @click="expand('Relations', true)"
        >
          <Icon name="network" :size="15" />Relations<Icon
            name="right"
            :size="12"
            class="push-right"
          />
        </button>
        <div
          v-if="submenu === 'Relations'"
          class="context-submenu"
          role="menu"
          aria-label="Relations"
        >
          <button role="menuitem" @click="child"><Icon name="plus" :size="14" />Add sub-task</button
          ><button v-if="parent" role="menuitem" @click="openTab(parent.id, parent.title)">
            Open parent task</button
          ><button
            v-for="childTask in children"
            :key="childTask.id"
            role="menuitem"
            @click="openTab(childTask.id, childTask.title)"
          >
            {{ childTask.title }}</button
          ><button
            role="menuitem"
            @click="
              unsupported(
                'Changing parent or dependency relations requires a relationship adapter. No relation was changed.',
              )
            "
          >
            Set parent task…
          </button>
        </div>
      </div>
      <div class="context-divider" />
      <button
        role="menuitem"
        class="delete-action"
        @mouseenter="submenu = ''"
        @click="
          unsupported(
            'Task deletion is not available through the current control-plane contract. Nothing was deleted.',
          )
        "
      >
        <Trash2 :size="15" />Delete task
      </button>
    </div></Teleport
  >
</template>
<style scoped>
.task-context {
  position: fixed;
  z-index: 110;
  width: 200px;
  padding: 4px;
  border-radius: 8px;
  background: var(--fill-menu);
  border: 1px solid #ffffff55;
  box-shadow: 0 6px 18px #00000012;
  font-size: 14px;
  color: var(--text);
}
.task-context > button,
.context-group > button,
.context-submenu > button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  height: 28px;
  padding: 4px 6px;
  white-space: nowrap;
  border-radius: 4px;
  text-align: left;
  font-size: 14px;
}
.task-context button:hover,
.task-context button:focus-visible,
.context-group > button[aria-expanded='true'] {
  background: #00000006;
  outline: 0;
}
.context-divider {
  height: 1px;
  background: #ffffff40;
  margin: 4px -4px;
}
.context-group {
  position: relative;
}
.context-submenu {
  position: absolute;
  left: calc(100% + 4px);
  top: -4px;
  min-width: 210px;
  max-width: 280px;
  max-height: 400px;
  overflow: auto;
  padding: 4px;
  border-radius: 8px;
  border: 1px solid #ffffff55;
  background: var(--fill-menu);
  box-shadow: 0 6px 18px #00000012;
}
.opens-left .context-submenu {
  left: auto;
  right: calc(100% + 4px);
}
.context-submenu input {
  font-size: 13px;
  border: 1px solid #ffffff60;
  background: transparent;
  border-radius: 5px;
  padding: 6px;
  margin-bottom: 4px;
  width: 100%;
}
.context-submenu > small {
  display: block;
  padding: 8px;
  color: var(--muted);
  font-size: 12px;
}
.delete-action,
.blocked-icon {
  color: #d5213d;
}
.calendar-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 6px;
  gap: 8px;
  font-size: 14px;
}
.calendar-head button {
  height: 24px;
  width: 24px;
  display: grid;
  place-items: center;
}
.calendar-days {
  display: grid;
  grid-template-columns: repeat(7, 28px);
  gap: 0;
  padding: 6px 2px;
}
.calendar-days > small {
  font-size: 11px;
  color: var(--muted);
  text-align: center;
  line-height: 24px;
}
.calendar-days > button {
  height: 36px;
  text-align: center;
  font-size: 14px;
  border-radius: 4px;
}
.calendar-days .outside {
  color: var(--muted);
}
.calendar-days .selected {
  background: var(--primary);
  color: white;
}
</style>
