<script setup lang="ts">
import { computed } from 'vue';
import { useDecisions } from '../../api/queries';
import { decisionFilters, isQuestion, type DecisionDateFilter } from './decisionFilters';

const open = useDecisions(false);
const history = useDecisions(true);
const questions = computed(() => new Map([...(open.data.value ?? []), ...(history.data.value ?? [])].filter(isQuestion).map((item) => [item.id, item])).size);
const dates: { value: DecisionDateFilter; label: string }[] = [
  { value: 'all', label: 'All' }, { value: 'today', label: 'Today' },
  { value: 'yesterday', label: 'Yesterday' }, { value: 'week', label: 'Last 7 days' },
  { value: 'month', label: 'This month' }, { value: 'custom', label: 'Custom' },
];
</script>
<template>
  <div class="decision-filters" aria-label="Decision filters">
    <div class="filter-row" role="group" aria-label="Decision type">
      <button :aria-pressed="decisionFilters.type === 'all'" @click="decisionFilters.type = 'all'">All</button>
      <button :aria-pressed="decisionFilters.type === 'questions'" @click="decisionFilters.type = 'questions'"><span aria-hidden="true">●</span> Questions <span class="count">{{ questions }}</span></button>
    </div>
    <div class="filter-row" role="group" aria-label="Decision date">
      <button v-for="date in dates" :key="date.value" :aria-pressed="decisionFilters.date === date.value" @click="decisionFilters.date = date.value">{{ date.label }}</button>
    </div>
    <div v-if="decisionFilters.date === 'custom'" class="custom-dates">
      <label>From <input v-model="decisionFilters.from" type="date" :max="decisionFilters.to || undefined" /></label>
      <label>To <input v-model="decisionFilters.to" type="date" :min="decisionFilters.from || undefined" /></label>
    </div>
  </div>
</template>
<style scoped>
.decision-filters { display: flex; flex-direction: column; gap: 8px; padding: 4px 0 18px; }
.filter-row, .custom-dates { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; }
.filter-row button { border: 1px solid var(--border); border-radius: 999px; padding: 5px 13px; color: var(--muted); background: var(--surface); font-size: 12px; font-weight: 500; }
.filter-row button:hover { background: var(--hover); color: var(--text); }
.filter-row button[aria-pressed='true'] { border-color: var(--muted); background: var(--hover); color: var(--text); }
.count { color: var(--muted); margin-left: 3px; }
.custom-dates { font-size: 12px; color: var(--muted); }
.custom-dates label { display: flex; align-items: center; gap: 6px; }
.custom-dates input { color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 5px; padding: 4px; }
</style>
