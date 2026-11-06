<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { readNpy, type NpyPreview } from './npy-preview';
const props = defineProps<{ blob: Blob }>();
const preview = ref<NpyPreview>();
const pending = ref(true);
const selection = ref<number[]>([]);
let request = 0;
const numeric = computed(() => {
  if (!preview.value || preview.value.shape.length < 1 || preview.value.sampleShape.length > 2)
    return [];
  const isInteger = /[iu]/.test(preview.value.dtype);
  const numbers = preview.value.values.map(Number);
  return numbers.every((n) => Number.isFinite(n) && (!isInteger || Number.isSafeInteger(n)))
    ? numbers
    : [];
});
const chart = computed(() => {
  const nums = numeric.value;
  if (!nums.length) return undefined;
  const min = Math.min(...nums),
    max = Math.max(...nums),
    scale = Math.max(Math.abs(min), Math.abs(max)) || 1,
    low = min / scale,
    range = max / scale - low || 1;
  return {
    points: nums
      .map(
        (n, i) =>
          `${10 + (preview.value!.sampleIndices[0]![i]! * 280) / Math.max(1, preview.value!.shape.at(-1)! - 1)},${90 - ((n / scale - low) * 80) / range}`,
      )
      .join(' '),
    min,
    max,
    scale,
    low,
    range,
  };
});
async function selectSlice(dim: number, event: Event) {
  const input = event.target as HTMLInputElement;
  const dimension = preview.value?.shape[dim];
  const value = Number(input.value);
  if (!dimension || !Number.isSafeInteger(value) || value < 0 || value >= dimension) {
    input.value = String(selection.value[dim]);
    return;
  }
  const next = [...selection.value];
  next[dim] = value;
  selection.value = next;
  const ticket = ++request;
  pending.value = true;
  const result = await readNpy(props.blob, next);
  if (ticket === request) {
    preview.value = result;
    pending.value = false;
  }
}
watch(
  () => props.blob,
  async (blob, _, onCleanup) => {
    const ticket = ++request;
    onCleanup(() => {
      request++;
    });
    preview.value = undefined;
    selection.value = [];
    pending.value = true;
    const result = await readNpy(blob);
    if (ticket === request) {
      preview.value = result;
      selection.value =
        result?.shape.slice(0, Math.max(0, result.shape.length - 2)).map(() => 0) ?? [];
      pending.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="pending">Reading array…</p>
  <section v-else-if="preview" class="array-preview" aria-label="NumPy array preview">
    <dl>
      <dt>Shape</dt>
      <dd>{{ preview.shape.length ? preview.shape.join(' × ') : 'Scalar' }}</dd>
      <dt>Data type</dt>
      <dd>{{ preview.dtype }}</dd>
      <dt>Storage order</dt>
      <dd>{{ preview.order }}</dd>
    </dl>
    <div v-if="selection.length" class="slice-controls">
      <label v-for="(value, dim) in selection" :key="dim"
        >Axis {{ dim + 1 }} index
        <input
          type="number"
          :value="value"
          min="0"
          :max="Math.max(0, preview.shape[dim]! - 1)"
          :disabled="preview.shape[dim] === 0"
          @change="selectSlice(dim, $event)"
        />
        <span>of {{ preview.shape[dim] }}</span>
      </label>
    </div>
    <p v-if="!preview.total">Empty array</p>
    <p v-else-if="preview.sampled">
      Showing {{ preview.values.length }} sampled values from the selected slice.
    </p>
    <p v-else>Values in row-major order</p>
    <figure v-if="chart && preview.sampleShape.length === 1" aria-label="Sample values plot">
      <svg viewBox="0 0 300 100" role="img" aria-label="Line plot of displayed array values">
        <polyline :points="chart.points" fill="none" stroke="currentColor" />
      </svg>
      <figcaption>
        Array indices {{ preview.sampleIndices[0]?.join(', ') }} · min {{ chart.min }} · max
        {{ chart.max }}
      </figcaption>
    </figure>
    <figure
      v-else-if="chart && preview.sampleShape.length === 2"
      aria-label="Sample values heatmap"
    >
      <table class="heatmap">
        <thead>
          <tr>
            <th scope="col">Row / column</th>
            <th v-for="col in preview.sampleIndices[1]" :key="col" scope="col">{{ col }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, r) in preview.sampleIndices[0]" :key="row">
            <th scope="row">{{ row }}</th>
            <td
              v-for="(col, c) in preview.sampleIndices[1]"
              :key="col"
              :title="`Row ${row}, column ${col}: ${preview.values[r * preview.sampleShape[1]! + c]}`"
            >
              <span
                :style="{
                  opacity:
                    0.2 +
                    (0.8 * (numeric[r * preview.sampleShape[1]! + c]! / chart.scale - chart.low)) /
                      chart.range,
                }"
              ></span>
            </td>
          </tr>
        </tbody>
      </table>
      <figcaption>Displayed values · min {{ chart.min }} · max {{ chart.max }}</figcaption>
    </figure>
    <div class="values">
      <code v-for="(value, i) in preview.values" :key="i">{{ value }}</code>
    </div>
  </section>
  <p v-else>Preview unavailable for this NumPy array. You can download the original file.</p>
</template>
<style scoped>
dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 16px;
}
dt {
  color: var(--muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.slice-controls {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}
.slice-controls input {
  width: 5em;
  margin: 0 4px;
}
.values {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
figure {
  margin: 12px 0;
}
svg {
  display: block;
  width: min(100%, 500px);
  height: 120px;
}
figcaption {
  color: var(--muted);
  font-size: 12px;
}
.heatmap {
  border-collapse: separate;
  border-spacing: 2px;
  font-size: 10px;
}
.heatmap th {
  font-weight: normal;
  text-align: center;
}
.heatmap span {
  display: block;
  width: 14px;
  height: 14px;
  background: #286bb2;
}
code {
  background: var(--surface-raised, #f1f2f4);
  border-radius: 4px;
  padding: 3px 6px;
  overflow-wrap: anywhere;
}
</style>
