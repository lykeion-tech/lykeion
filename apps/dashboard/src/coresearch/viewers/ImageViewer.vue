<script setup lang="ts">
import { computed, ref, watch } from 'vue';
const props = defineProps<{ src: string; path: string }>();
const steps = [0.25, 0.5, 0.75, 1];
const zoom = ref<number | 'fit'>('fit'),
  dims = ref<{ w: number; h: number }>(),
  img = ref<HTMLImageElement>();
watch(
  () => props.src,
  () => {
    zoom.value = 'fit';
    dims.value = undefined;
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
    <div class="image-controls" role="group" aria-label="Zoom">
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
    <div class="image-stage">
      <img
        ref="img"
        class="image-el"
        :class="{ 'is-zoomed': !!sized }"
        :src="src"
        :alt="path"
        :style="sized"
        @load="loaded"
      />
    </div>
  </figure>
</template>
