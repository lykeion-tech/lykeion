<script setup lang="ts">
import { computed, ref, watch } from 'vue';
const props = defineProps<{ src: string; path: string }>();
const steps = [0.25, 0.5, 0.75, 1];
const zoom = ref<number | 'fit'>('fit'),
  dims = ref<{ w: number; h: number }>(),
  img = ref<HTMLImageElement>();
const failed = ref(false);
watch(
  () => props.src,
  () => {
    zoom.value = 'fit';
    dims.value = undefined;
    failed.value = false;
  },
);
const sized = computed(() =>
  typeof zoom.value === 'number' && dims.value
    ? { width: `${dims.value.w * zoom.value}px`, height: `${dims.value.h * zoom.value}px` }
    : undefined,
);
function zoomIn() {
  if (zoom.value === 1) return;
  const ratio =
    zoom.value === 'fit'
      ? img.value && dims.value?.w
        ? img.value.clientWidth / dims.value.w
        : 0
      : zoom.value;
  zoom.value = steps.find((v) => v > ratio) ?? 1;
}
function zoomOut() {
  if (zoom.value !== 'fit') zoom.value = steps[steps.indexOf(zoom.value) - 1] ?? 'fit';
}
function loaded(event: Event) {
  const image = event.currentTarget as HTMLImageElement;
  dims.value = { w: image.naturalWidth, h: image.naturalHeight };
}
</script>
<template>
  <figure class="viz-figure artifact-image">
    <div v-if="!failed" class="image-controls" role="group" aria-label="Zoom">
      <button
        type="button"
        class="seg-btn"
        :class="{ 'is-active': zoom === 'fit' }"
        :aria-pressed="zoom === 'fit'"
        @click="zoom = 'fit'"
      >
        Fit</button
      ><button type="button" class="seg-btn" :disabled="zoom === 'fit'" @click="zoomOut">
        Zoom out</button
      ><span class="image-zoom-label">{{ zoom === 'fit' ? 'Fit' : `${zoom * 100}%` }}</span
      ><button type="button" class="seg-btn" :disabled="zoom === 1" @click="zoomIn">Zoom in</button
      ><button v-if="zoom !== 1" type="button" class="seg-btn" @click="zoom = 1">100%</button>
    </div>
    <div v-if="!failed" class="image-stage">
      <img
        ref="img"
        class="image-el"
        :class="{ 'is-zoomed': !!sized }"
        :src="src"
        :alt="path"
        :style="sized"
        @load="loaded"
        @error="failed = true"
      />
    </div>
    <p v-else role="status">
      Image preview unavailable. <a :href="src" :download="path.split('/').pop()">Download</a>
    </p>
  </figure>
</template>
<style scoped>
.artifact-image {
  margin: 0;
  min-width: 0;
}
.image-controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}
.seg-btn {
  padding: 4px 8px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
  color: var(--text);
  font: inherit;
  cursor: pointer;
}
.seg-btn.is-active {
  background: var(--input);
}
.seg-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.seg-btn:focus-visible {
  outline: 2px solid var(--primary-text, #2563a6);
  outline-offset: 2px;
}
.image-zoom-label {
  min-width: 3em;
  text-align: center;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}
.image-stage {
  display: flex;
  align-items: safe center;
  justify-content: safe center;
  overflow: auto;
  min-height: 120px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background-color: var(--surface);
  background-image:
    linear-gradient(45deg, var(--input) 25%, transparent 25%),
    linear-gradient(-45deg, var(--input) 25%, transparent 25%),
    linear-gradient(45deg, transparent 75%, var(--input) 75%),
    linear-gradient(-45deg, transparent 75%, var(--input) 75%);
  background-size: 16px 16px;
  background-position:
    0 0,
    0 8px,
    8px -8px,
    -8px 0;
}
.image-el {
  display: block;
  max-width: 100%;
  max-height: 420px;
  image-rendering: pixelated;
  border-radius: 2px;
}
.image-el.is-zoomed {
  max-width: none;
  max-height: none;
}
</style>
