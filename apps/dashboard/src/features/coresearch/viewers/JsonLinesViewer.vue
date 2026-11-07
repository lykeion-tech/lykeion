<script setup lang="ts">
import { computed, ref } from 'vue';
import { parseJsonLines, plotFraction } from './json-lines';
const props = defineProps<{ text: string; partial: boolean }>();
const preview = computed(() => parseJsonLines(props.text, props.partial));
const search = ref('');
const sort = ref(-1);
const descending = ref(false);
const numericColumns = computed(() =>
  preview.value.columns
    .map((name, i) => ({ name, i }))
    .filter(({ i }) => preview.value.numbers.some((row) => row[i] !== null)),
);
const filtered = computed(() => {
  const query = search.value.trim().toLocaleLowerCase();
  const rows = preview.value.rows
    .map((cells, i) => ({ cells, numbers: preview.value.numbers[i] }))
    .filter((row) => row.cells.some((cell) => cell.toLocaleLowerCase().includes(query)));
  if (sort.value < 0) return rows;
  const numeric = preview.value.rows.every(
    (row, i) => row[sort.value] === '' || preview.value.numbers[i][sort.value] !== null,
  );
  return rows.sort((a, b) => {
    const left = a.cells[sort.value] ?? '',
      right = b.cells[sort.value] ?? '';
    const leftNumber = a.numbers[sort.value],
      rightNumber = b.numbers[sort.value];
    const comparison =
      numeric && leftNumber !== null && rightNumber !== null
        ? leftNumber - rightNumber
        : left.localeCompare(right);
    return descending.value ? -comparison : comparison;
  });
});
function changeSort(index: number) {
  descending.value = sort.value === index ? !descending.value : false;
  sort.value = index;
}
const plot = computed(() => {
  const [x, y] = numericColumns.value;
  if (!x || !y) return undefined;
  const values = filtered.value
    .filter((row) => row.numbers[x.i] !== null && row.numbers[y.i] !== null)
    .map((row) => ({ x: row.numbers[x.i]!, y: row.numbers[y.i]! }));
  if (!values.length) return undefined;
  const xs = values.map((p) => p.x),
    ys = values.map((p) => p.y);
  const minX = Math.min(...xs),
    maxX = Math.max(...xs),
    minY = Math.min(...ys),
    maxY = Math.max(...ys);
  return {
    minX,
    maxX,
    minY,
    maxY,
    points: values.map((p) => ({
    x: 42 + 246 * plotFraction(p.x, minX, maxX),
    y: 138 - 120 * plotFraction(p.y, minY, maxY),
    })),
  };
});
</script>
<template>
  <section aria-label="JSON Lines preview">
    <p v-if="preview.error" role="alert">{{ preview.error }}</p>
    <template v-else>
      <p v-if="preview.partial" class="note">Showing a partial preview.</p>
      <p v-if="preview.precisionWarning" class="note" role="status">
        Some numbers exceed the preview's precision or range and may be inaccurate.
      </p>
      <p v-if="!preview.rows.length" class="note">No records to preview.</p>
      <template v-else>
        <input
          :value="search"
          type="search"
          aria-label="Search records"
          placeholder="Search records…"
          @input="search = ($event.target as HTMLInputElement).value"
        />
        <figure v-if="plot" class="scatter">
          <figcaption>{{ numericColumns[0].name }} × {{ numericColumns[1].name }}</figcaption>
          <p class="note">{{ plot.points.length }} of {{ filtered.length }} records plotted</p>
          <svg viewBox="0 0 300 160" role="img" aria-label="Scatter plot">
            <path d="M42 18V138H288" fill="none" stroke="currentColor" />
            <text x="42" y="156">{{ plot.minX }}</text>
            <text x="288" y="156" text-anchor="end">{{ plot.maxX }}</text>
            <text x="38" y="138" text-anchor="end">{{ plot.minY }}</text>
            <text x="38" y="22" text-anchor="end">{{ plot.maxY }}</text>
            <circle v-for="(point, i) in plot.points" :key="i" :cx="point.x" :cy="point.y" r="3" />
          </svg>
        </figure>
        <div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th
                  v-for="(column, i) in preview.columns"
                  :key="i"
                  :aria-sort="sort === i ? (descending ? 'descending' : 'ascending') : 'none'"
                >
                  <button type="button" @click="changeSort(i)">{{ column }}</button>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, i) in filtered" :key="i">
                <td v-for="(cell, j) in row.cells" :key="j">{{ cell }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!filtered.length" class="note">No records match.</p>
      </template>
    </template>
  </section>
</template>
<style scoped>
.note {
  color: var(--muted);
  font-size: 13px;
}
.scatter {
  margin: 12px 0;
}
.scatter figcaption {
  color: var(--muted);
  font-size: 13px;
}
.scatter svg {
  width: min(100%, 300px);
  display: block;
  background: var(--canvas);
  fill: var(--primary-text);
}
th button {
  color: inherit;
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}
.table-scroll {
  overflow: auto;
  max-width: 100%;
}
table {
  border-collapse: collapse;
  width: 100%;
  font-size: 13px;
}
th,
td {
  border: 1px solid var(--border);
  padding: 6px 8px;
  text-align: left;
  overflow-wrap: anywhere;
}
</style>
