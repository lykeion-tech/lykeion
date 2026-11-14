<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
const props = defineProps<{ blob: Blob }>();
const state = ref<'loading' | 'ready' | 'unavailable'>('loading');
const canvas = ref<HTMLCanvasElement>();
watch(
  () => props.blob,
  (blob, _, cleanup) => {
    state.value = 'loading';
    let worker: Worker;
    try {
      worker = new Worker(new URL('./heic-worker.ts', import.meta.url), { type: 'module' });
    } catch {
      state.value = 'unavailable';
      return;
    }
    let active = true;
    const stop = () => {
      active = false;
      clearTimeout(timer);
      worker.terminate();
    };
    const timer = setTimeout(() => {
      if (active) {
        stop();
        state.value = 'unavailable';
      }
    }, 10000);
    cleanup(stop);
    worker.onmessage = async (
      event: MessageEvent<{ width: number; height: number; pixels: Uint8Array } | null>,
    ) => {
      if (!active) return;
      const value = event.data;
      if (
        !value ||
        !Number.isInteger(value.width) ||
        !Number.isInteger(value.height) ||
        value.width < 1 ||
        value.height < 1 ||
        value.width > 4096 ||
        value.height > 4096 ||
        !(value.pixels instanceof Uint8Array) ||
        value.pixels.length !== value.width * value.height * 4 ||
        value.pixels.length > 16777216
      ) {
        stop();
        state.value = 'unavailable';
        return;
      }
      state.value = 'ready';
      await nextTick();
      if (!active) return;
      try {
        const element = canvas.value;
        if (!element) throw Error();
        element.width = value.width;
        element.height = value.height;
        const context = element.getContext('2d');
        if (!context) throw Error();
        context.putImageData(
          new ImageData(new Uint8ClampedArray(value.pixels), value.width, value.height),
          0,
          0,
        );
        stop();
      } catch {
        stop();
        state.value = 'unavailable';
      }
    };
    worker.onerror = () => {
      if (active) {
        stop();
        state.value = 'unavailable';
      }
    };
    worker.postMessage({ blob });
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="state === 'loading'" role="status">Reading image…</p>
  <div v-else-if="state === 'ready'">
    <p>
      Colors are shown in 8-bit and may differ from the original, especially for HDR or wide-gamut
      images.
    </p>
    <canvas ref="canvas" aria-label="HEIC image preview" />
  </div>
  <p v-else>
    HEIC preview unavailable. The file may be invalid, too large, or use an unsupported format.
  </p>
</template>
<style scoped>
canvas {
  display: block;
  max-width: 100%;
  height: auto;
}
</style>
