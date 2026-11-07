<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { TableData } from '../csv-head';
import { sortRows } from './delimited-sort';
const props = defineProps<{ table: TableData }>();
const column = ref<number>(), direction = ref<'ascending' | 'descending'>('ascending');
watch(() => props.table, () => { column.value = undefined; direction.value = 'ascending'; });
const header = computed(() => props.table.header.slice(0, 100));
const sample = computed(() => props.table.rows.slice(0, 1000));
const rows = computed(() => column.value === undefined ? sample.value : sortRows(sample.value, column.value, direction.value));
function toggle(index: number) {
  if (column.value === index) direction.value = direction.value === 'ascending' ? 'descending' : 'ascending';
  else { column.value = index; direction.value = 'ascending'; }
}
</script>
<template>
  <figure class="artifact-table">
    <div v-if="table.partial || table.rows.length > 1000 || table.header.length > 100" class="viz-caption">
      <span v-if="table.partial">First part only. </span><span v-if="table.rows.length > 1000">First 1,000 rows only. </span><span v-if="table.header.length > 100">First 100 columns only. </span>Sorting applies to the displayed rows.
    </div>
    <div class="table-scroll"><table class="data-table">
      <thead><tr><th v-for="(cell, i) in header" :key="i" scope="col" :aria-sort="column === i ? direction : 'none'">
        <button type="button" @click="toggle(i)"><span v-if="table.types" class="col-type" aria-hidden="true">{{ table.types[i] === 'number' ? '123' : 'abc' }}</span><span>{{ cell }}</span><span aria-hidden="true">{{ column === i ? (direction === 'ascending' ? ' ↑' : ' ↓') : '' }}</span></button>
      </th></tr></thead>
      <tbody><tr v-for="(row, i) in rows" :key="i"><td v-for="(_, j) in header" :key="j">{{ row[j] ?? '' }}</td></tr></tbody>
    </table></div>
  </figure>
</template>
<style scoped>
.artifact-table { margin: 0; min-width: 0; }
.table-scroll { overflow: auto; max-width: 100%; }
.data-table { border-collapse: collapse; width: 100%; font-size: 13px; }
.data-table th, .data-table td { border: 1px solid var(--border); padding: 6px 8px; text-align: left; white-space: nowrap; }
.data-table th button { border: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; padding: 0; text-align: left; }
.data-table th button:focus-visible { outline: 2px solid currentColor; outline-offset: 2px; }
.col-type { color: var(--muted); margin-right: 8px; font-size: 11px; }
.viz-caption { color: var(--muted); padding: 6px 0; font-size: 13px; }
</style>
