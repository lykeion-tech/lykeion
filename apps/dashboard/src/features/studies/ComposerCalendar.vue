<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import Icon from '../../ui/Icon.vue';
import {
  dateKey as key,
  longDateLabel as label,
  monthWeeks,
  moveDate,
  parseDateKey as parse,
  weekdayNames as weekdays,
} from './dates';
const props = defineProps<{ modelValue: string; kind: 'due' | 'start' }>();
const emit = defineEmits<{ 'update:modelValue': [value: string]; close: [] }>();
const initial = parse(props.modelValue),
  month = ref(new Date(initial.getFullYear(), initial.getMonth(), 1)),
  focusDate = ref(initial),
  root = ref<HTMLElement>();
const today = key(new Date());
const heading = computed(() =>
  month.value.toLocaleDateString('en-US', { month: 'long', year: 'numeric' }),
);
const weeks = computed(() => monthWeeks(month.value));
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
  const d = moveDate(parse(target), e.key);
  if (d) {
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
  background: var(--fill-menu);
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
  color: var(--muted);
}
.selected {
  background: var(--primary);
  color: var(--on-primary);
}
.today:not(.selected) {
  font-weight: 600;
}
.composer-calendar button:hover {
  background: #ffffff50;
}
.composer-calendar button:focus-visible {
  outline: 1px solid var(--primary-text);
  outline-offset: -2px;
}
</style>
