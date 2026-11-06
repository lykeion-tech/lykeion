<script setup lang="ts">
import { computed } from 'vue';
import { parseMatrixMarket } from './matrix-market';
const props = defineProps<{ text: string; partial: boolean }>();
const matrix = computed(() => parseMatrixMarket(props.text, props.partial));
const scale = computed(() =>
  matrix.value ? Math.max(1, ...matrix.value.values.map((v) => Math.abs(v))) : 1,
);
</script>
<template>
  <section v-if="matrix" aria-label="Matrix Market preview">
    <p>
      {{ matrix.shape[0] }} rows × {{ matrix.shape[1] }} columns · {{ matrix.entries }} stored
      entries
    </p>
    <p v-if="matrix.sampled">Sampled rows and columns are shown; other cells are not displayed.</p>
    <div class="matrix-scroll">
      <table>
        <thead>
          <tr>
            <th scope="col">Row / column</th>
            <th v-for="col in matrix.columns" :key="col" scope="col">{{ col }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, r) in matrix.rows" :key="row">
            <th scope="row">{{ row }}</th>
            <td
              v-for="(col, c) in matrix.columns"
              :key="col"
              :style="{
                backgroundColor: `rgba(40, 107, 178, ${(Math.abs(matrix.values[r * matrix.columns.length + c]!) / scale) * 0.3})`,
              }"
            >
              {{ matrix.values[r * matrix.columns.length + c] }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
  <p v-else>Preview unavailable for this incomplete or unsupported Matrix Market file.</p>
</template>
<style scoped>
.matrix-scroll {
  overflow: auto;
  max-width: 100%;
}
table {
  border-collapse: collapse;
  font:
    12px ui-monospace,
    monospace;
}
th,
td {
  border: 1px solid var(--border);
  padding: 6px;
  text-align: right;
  white-space: nowrap;
}
th {
  font-weight: 500;
}
</style>
