<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { workbookSample, workbookSheets, type WorkbookSheet } from './xlsx-text';
import type { WorkbookParts } from './xlsx-preview';
const props = defineProps<{ blob: Blob }>();
const parts = ref<WorkbookParts>(),
  sheets = ref<WorkbookSheet[]>(),
  pending = ref(true),
  selected = ref(0);
const sample = computed(() =>
  parts.value && sheets.value?.[selected.value]
    ? workbookSample(parts.value, sheets.value[selected.value]!)
    : undefined,
);
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
    selected.value = 0;
    parts.value = undefined;
    sheets.value = undefined;
    cleanup(stop);
    try {
      worker = new Worker(new URL('./xlsx-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (event: MessageEvent<{ parts?: WorkbookParts }>) => {
        if (own !== generation) return;
        stop();
        parts.value = event.data.parts;
        sheets.value = parts.value ? workbookSheets(parts.value) : undefined;
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
function columnName(index: number) {
  let n = index + 1,
    name = '';
  while (n) {
    n--;
    name = String.fromCharCode(65 + (n % 26)) + name;
    n = Math.floor(n / 26);
  }
  return name;
}
</script>
<template>
  <p v-if="pending" role="status">Reading workbook…</p>
  <section v-else-if="sheets" class="workbook" aria-label="Workbook preview">
    <p class="note">
      Read-only sample. Numbers are stored values; dates may appear as serial numbers. Formulas are
      not calculated.
    </p>
    <label v-if="sheets.length"
      >Sheet
      <select v-model.number="selected">
        <option v-for="(sheet, i) in sheets" :key="sheet.path" :value="i">
          {{ sheet.name }}{{ sheet.hidden ? ' (hidden)' : '' }}
        </option>
      </select></label
    >
    <p v-if="!sheets.length">This workbook has no sheets.</p>
    <p v-else-if="!sample">This sheet cannot be previewed safely.</p>
    <template v-else>
      <p v-if="sample.truncated" class="note">
        Showing up to the first 50 rows and 30 columns; some content is omitted.
      </p>
      <div class="table-scroll">
        <table v-if="sample.rows.length">
          <thead>
            <tr>
              <th></th>
              <th v-for="(_, i) in sample.rows[0]" :key="i">{{ columnName(i) }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in sample.rows" :key="i">
              <th>{{ i + 1 }}</th>
              <td v-for="(cell, j) in row" :key="j">
                <template v-if="cell"
                  >{{ cell.missing ? 'No cached value' : cell.text
                  }}<span v-if="cell.formula" class="cached"> · cached formula</span></template
                >
              </td>
            </tr>
          </tbody>
        </table>
        <p v-else>This sheet has no displayed values.</p>
      </div>
    </template>
  </section>
  <p v-else>
    Workbook preview unavailable. The file may be malformed, too large, or use an unsupported
    format.
  </p>
</template>
<style scoped>
.note {
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
  min-width: 30px;
  background: var(--surface-raised, #f1f2f4);
}
.cached {
  color: var(--muted);
  font-size: 10px;
}
</style>
