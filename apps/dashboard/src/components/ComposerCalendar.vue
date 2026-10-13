<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import Icon from './Icon.vue';
const props = defineProps<{ modelValue: string; kind: 'due' | 'start' }>();
const emit = defineEmits<{ 'update:modelValue': [value: string]; close: [] }>();
const parse = (s: string) => {
  const [y, m, d] = s.split('-').map(Number);
  return y && m && d ? new Date(y, m - 1, d) : new Date();
};
const initial = parse(props.modelValue),
  month = ref(new Date(initial.getFullYear(), initial.getMonth(), 1)),
  focusDate = ref(initial),
  root = ref<HTMLElement>();
function key(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
const today = key(new Date());
const heading = computed(() =>
  month.value.toLocaleDateString('en-US', { month: 'long', year: 'numeric' }),
);
const days = computed(() => {
  const first = new Date(month.value);
  first.setDate(1 - first.getDay());
  const count =
    Math.ceil(
      (month.value.getDay() +
        new Date(month.value.getFullYear(), month.value.getMonth() + 1, 0).getDate()) /
        7,
    ) * 7;
  return Array.from(
    { length: count },
    (_, i) => new Date(first.getFullYear(), first.getMonth(), first.getDate() + i),
  );
});
const weeks = computed(() =>
  Array.from({ length: days.value.length / 7 }, (_, i) => days.value.slice(i * 7, i * 7 + 7)),
);
const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
function label(d: Date) {
  const n = d.getDate(),
    suffix = n % 100 >= 11 && n % 100 <= 13 ? 'th' : (['th', 'st', 'nd', 'rd'][n % 10] ?? 'th');
  return `${key(d) === today ? 'Today, ' : ''}${weekdays[d.getDay()]}, ${d.toLocaleDateString('en-US', { month: 'long' })} ${n}${suffix}, ${d.getFullYear()}`;
}
function choose(value: string) {
  emit('update:modelValue', value);
  emit('close');
}
function changeMonth(n: number) {
  month.value = new Date(month.value.getFullYear(), month.value.getMonth() + n, 1);
}
async function keys(e: KeyboardEvent) {
  e.stopPropagation();
  if (e.key === 'Escape') {
    e.preventDefault();
    emit('close');
    return;
  }
  const target = (e.target as HTMLElement).dataset.date;
  if (!target) return;
  let d = parse(target),
    handled = true;
  switch (e.key) {
    case 'ArrowLeft':
      d.setDate(d.getDate() - 1);
      break;
    case 'ArrowRight':
      d.setDate(d.getDate() + 1);
      break;
    case 'ArrowUp':
      d.setDate(d.getDate() - 7);
      break;
    case 'ArrowDown':
      d.setDate(d.getDate() + 7);
      break;
    case 'Home':
      d.setDate(d.getDate() - d.getDay());
      break;
    case 'End':
      d.setDate(d.getDate() + 6 - d.getDay());
      break;
    case 'PageUp':
    case 'PageDown': {
      const delta = e.key === 'PageUp' ? -1 : 1;
      const max = new Date(d.getFullYear(), d.getMonth() + delta + 1, 0).getDate();
      d = new Date(d.getFullYear(), d.getMonth() + delta, Math.min(d.getDate(), max));
      break;
    }
    default:
      handled = false;
  }
  if (handled) {
    e.preventDefault();
    focusDate.value = d;
    month.value = new Date(d.getFullYear(), d.getMonth(), 1);
    await nextTick();
    root.value?.querySelector<HTMLButtonElement>(`[data-date="${key(d)}"]`)?.focus();
  }
}
onMounted(() => root.value?.querySelector<HTMLButtonElement>('.clear-date')?.focus());
</script>
<template>
  <div
    ref="root"
    class="composer-calendar"
    role="dialog"
    :aria-label="kind === 'due' ? 'Choose due date' : 'Choose start date'"
    @keydown="keys"
  >
    <button type="button" class="clear-date" @click="choose('')">
      No {{ kind }} date<Icon v-if="!modelValue" name="check" :size="14" />
    </button>
    <div class="calendar-month">
      <button type="button" aria-label="Previous month" @click="changeMonth(-1)">
        <Icon name="left" :size="14" /></button
      ><span aria-live="polite">{{ heading }}</span
      ><button type="button" aria-label="Next month" @click="changeMonth(1)">
        <Icon name="right" :size="14" />
      </button>
    </div>
    <table role="grid" :aria-label="heading">
      <thead>
        <tr>
          <th v-for="day in weekdays" :key="day" scope="col" :aria-label="day">
            {{ day.slice(0, 2) }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(week, i) in weeks" :key="i">
          <td v-for="day in week" :key="key(day)" :aria-selected="key(day) === modelValue">
            <button
              type="button"
              :data-date="key(day)"
              :aria-label="label(day)"
              :aria-current="key(day) === today ? 'date' : undefined"
              :tabindex="key(day) === key(focusDate) ? 0 : -1"
              :class="{
                outside: day.getMonth() !== month.getMonth(),
                selected: key(day) === modelValue,
                today: key(day) === today,
              }"
              @click="choose(key(day))"
            >
              {{ day.getDate() }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
<style scoped>
.composer-calendar {
  position: absolute;
  top: calc(100% + 5px);
  left: 0;
  z-index: 80;
  width: 214px;
  background: #e1e1e6;
  border: 1px solid #ffffff70;
  border-radius: 9px;
  box-shadow: 0 5px 16px #0002;
  font-size: 12px;
  padding: 0 7px 6px;
  color: var(--text);
}
.clear-date {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: calc(100% + 14px);
  margin: 0 -7px;
  height: 38px;
  padding: 0 13px;
  border-bottom: 1px solid #ffffff60;
  text-align: left;
  border-radius: 9px 9px 0 0;
  font-size: 12px;
}
.calendar-month {
  display: flex;
  height: 55px;
  align-items: center;
  justify-content: space-between;
  padding: 0 3px;
}
.calendar-month > button {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 4px;
}
table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}
th {
  height: 26px;
  font-weight: 400;
  color: var(--muted);
}
td {
  padding: 0;
  text-align: center;
  height: 36px;
}
td > button {
  width: 28px;
  height: 28px;
  border-radius: 5px;
  font-size: 12px;
  padding: 0;
}
.outside {
  color: #a1a1ac;
}
.selected {
  background: var(--primary);
  color: white;
}
.today:not(.selected) {
  font-weight: 600;
}
.composer-calendar button:hover {
  background: #ffffff50;
}
.composer-calendar button:focus-visible {
  outline: 1px solid var(--primary);
  outline-offset: -2px;
}
</style>
