<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { readFits, type FitsPreview } from './fits-preview';
const props = defineProps<{ blob: Blob }>();
const result = ref<FitsPreview>();
const pending = ref(true),
  selection = ref<number[]>([]);
let request = 0;
const scale = computed(() =>
  Math.max(
    1,
    ...(result.value?.values ?? [])
      .map((v) => Number(v))
      .filter(Number.isFinite)
      .map(Math.abs),
  ),
);
async function changeSlice(index: number, event: Event) {
  const input = event.target as HTMLInputElement,
    value = Number(input.value),
    max = result.value?.shape[index + 2];
  if (!max || !Number.isSafeInteger(value) || value < 0 || value >= max) {
    input.value = String(selection.value[index]);
    return;
  }
  const next = [...selection.value];
  next[index] = value;
  selection.value = next;
  const ticket = ++request;
  pending.value = true;
  const preview = await readFits(props.blob, next);
  if (ticket === request) {
    result.value = preview;
    pending.value = false;
  }
}
watch(
  () => props.blob,
  async (blob, _, cleanup) => {
    const ticket = ++request;
    cleanup(() => {
      request++;
    });
    pending.value = true;
    result.value = undefined;
    selection.value = [];
    const preview = await readFits(blob);
    if (ticket === request) {
      result.value = preview;
      selection.value = preview?.shape.slice(2).map(() => 0) ?? [];
      pending.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="pending">Reading FITS image…</p>
  <section v-else-if="result" aria-label="FITS image preview">
    <p>FITS image · axes {{ result.shape.join(' × ') }} · BITPIX {{ result.bitpix }}</p>
    <div class="controls">
      <label v-for="(value, i) in selection" :key="i"
        >Axis {{ i + 3 }} index
        <input
          type="number"
          min="0"
          :max="result.shape[i + 2]! - 1"
          :value="value"
          @change="changeSlice(i, $event)"
      /></label>
    </div>
    <p v-if="result.sampled">Sampled pixel values; displayed indices are zero-based.</p>
    <p v-else>Pixel indices are zero-based.</p>
    <div class="scroll">
      <table>
        <thead>
          <tr>
            <th scope="col">Row / column</th>
            <th v-for="col in result.columns" :key="col" scope="col">{{ col }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, r) in result.rows" :key="row">
            <th scope="row">{{ row }}</th>
            <td
              v-for="(col, c) in result.columns"
              :key="col"
              :style="{
                backgroundColor:
                  result.values[r * result.columns.length + c] === 'blank'
                    ? 'transparent'
                    : `rgba(40, 107, 178, ${(Math.abs(Number(result.values[r * result.columns.length + c])) / scale) * 0.3})`,
              }"
            >
              {{ result.values[r * result.columns.length + c] }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
  <p v-else>Preview unavailable for this incomplete or unsupported FITS image.</p>
</template>
<style scoped>
.controls {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}
.controls input {
  width: 5em;
}
.scroll {
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
