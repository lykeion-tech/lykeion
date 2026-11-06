<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { readNpy, type NpyPreview } from './npy-preview';
const props = defineProps<{ blob: Blob }>();
const preview = ref<NpyPreview>();
const pending = ref(true);
const numeric = computed(() => {
  if (!preview.value || preview.value.shape.length < 1 || preview.value.shape.length > 2) return [];
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
          `${10 + (i * 280) / Math.max(1, nums.length - 1)},${90 - ((n / scale - low) * 80) / range}`,
      )
      .join(' '),
    min,
    max,
    scale,
    low,
    range,
  };
});
watch(
  () => props.blob,
  async (blob, _, onCleanup) => {
    let live = true;
    onCleanup(() => {
      live = false;
    });
    preview.value = undefined;
    pending.value = true;
    const result = await readNpy(blob);
    if (live) {
      preview.value = result;
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
    <p v-if="preview.total > preview.values.length">
      First {{ preview.values.length }} of {{ preview.total }} values in row-major order
    </p>
    <p v-else-if="preview.total">Values in row-major order</p>
    <p v-else>Empty array</p>
    <figure v-if="chart && preview.shape.length === 1" aria-label="Sample values plot">
      <svg viewBox="0 0 300 100" role="img" aria-label="Line plot of displayed array values">
        <polyline :points="chart.points" fill="none" stroke="currentColor" />
      </svg>
      <figcaption>Displayed values · min {{ chart.min }} · max {{ chart.max }}</figcaption>
    </figure>
    <figure v-else-if="chart && preview.shape.length === 2" aria-label="Sample values heatmap">
      <div
        class="heatmap"
        :style="{
          gridTemplateColumns: `repeat(${Math.min(preview.shape[1]!, 100)}, minmax(5px, 14px))`,
        }"
      >
        <span
          v-for="(value, i) in numeric"
          :key="i"
          :title="preview.values[i]"
          :style="{ opacity: 0.2 + (0.8 * (value / chart.scale - chart.low)) / chart.range }"
        ></span>
      </div>
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
  display: grid;
  gap: 2px;
  overflow: auto;
}
.heatmap span {
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
