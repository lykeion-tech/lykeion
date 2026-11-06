<script setup lang="ts">
import { ref, watch } from 'vue';
import type { ParquetPreview } from './parquet-preview';
const props = defineProps<{ blob: Blob }>();
const preview = ref<ParquetPreview>(),
  pending = ref(true);
let worker: Worker | undefined,
  timer: ReturnType<typeof setTimeout> | undefined,
  generation = 0;
function stop() {
  generation++;
  worker?.terminate();
  worker = undefined;
  if (timer) clearTimeout(timer);
  timer = undefined;
}
watch(
  () => props.blob,
  (blob, _, cleanup) => {
    stop();
    const own = generation;
    pending.value = true;
    preview.value = undefined;
    cleanup(stop);
    try {
      worker = new Worker(new URL('./parquet-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (event: MessageEvent<{ preview?: ParquetPreview }>) => {
        if (own !== generation) return;
        stop();
        preview.value = event.data.preview;
        pending.value = false;
      };
      worker.onerror = () => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      };
      worker.postMessage({ blob });
    } catch {
      stop();
      pending.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="pending" role="status">Reading Parquet file…</p>
  <section v-else-if="preview" aria-label="Parquet preview">
    <p class="note">
      Read-only physical values. Showing {{ preview.rows.length }} of {{ preview.totalRows }} rows;
      binary values show their byte length.
    </p>
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th v-for="column in preview.columns" :key="column.name">
              {{ column.name }} <span>{{ column.type }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in preview.rows" :key="i">
            <td v-for="(cell, j) in row" :key="j">
              <span v-if="cell === null" class="null-value">NULL</span
              ><template v-else>{{ cell }}</template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
  <p v-else>
    Parquet preview unavailable. Only uncompressed, flat columns with plain encoding are supported.
  </p>
</template>
<style scoped>
.null-value {
  color: var(--muted);
  font-style: italic;
}
.note,
th span {
  color: var(--muted);
  font-size: 12px;
}
.table-scroll {
  max-width: 100%;
  overflow: auto;
  margin-top: 12px;
}
table {
  border-collapse: collapse;
  font-size: 13px;
}
th,
td {
  border: 1px solid var(--border);
  padding: 6px 8px;
  text-align: left;
  min-width: 80px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
th {
  background: var(--surface-raised, #f1f2f4);
}
</style>
