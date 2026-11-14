<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import type { DicomPreview } from './dicom-preview';
const props = defineProps<{ blob: Blob }>();
const canvas = ref<HTMLCanvasElement>(),
  frame = ref(0),
  frames = ref(1),
  pending = ref(true),
  failed = ref(false);
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
function load(index: number) {
  stop();
  const own = generation;
  pending.value = true;
  failed.value = false;
  try {
    worker = new Worker(new URL('./dicom-worker.ts', import.meta.url), { type: 'module' });
    timer = setTimeout(() => {
      if (own !== generation) return;
      stop();
      pending.value = false;
      failed.value = true;
    }, 5000);
    worker.onmessage = async (event: MessageEvent<{ result?: DicomPreview }>) => {
      if (own !== generation) return;
      const result = event.data.result;
      stop();
      const ticket = generation;
      if (!result) {
        pending.value = false;
        failed.value = true;
        return;
      }
      frames.value = result.frames;
      pending.value = false;
      await nextTick();
      if (ticket !== generation) return;
      try {
        if (!canvas.value) throw new Error();
        canvas.value.width = result.width;
        canvas.value.height = result.height;
        const context = canvas.value.getContext('2d');
        if (!context) throw new Error();
        context.putImageData(new ImageData(result.rgba, result.width, result.height), 0, 0);
      } catch {
        failed.value = true;
      }
    };
    worker.onerror = () => {
      if (own !== generation) return;
      stop();
      pending.value = false;
      failed.value = true;
    };
    worker.postMessage({ blob: props.blob, frame: index });
  } catch {
    stop();
    pending.value = false;
    failed.value = true;
  }
}
watch(
  () => props.blob,
  (_, __, cleanup) => {
    frame.value = 0;
    frames.value = 1;
    load(0);
    cleanup(stop);
  },
  { immediate: true },
);
function select(event: Event) {
  const input = event.target as HTMLInputElement,
    next = Number(input.value) - 1;
  if (!Number.isSafeInteger(next) || next < 0 || next >= frames.value) {
    input.value = String(frame.value + 1);
    return;
  }
  frame.value = next;
  load(next);
}
</script>
<template>
  <section aria-label="DICOM image preview" class="dicom-preview">
    <p class="note">
      Auto-scaled image preview; not for diagnosis. Image pixels may contain identifying
      information.
    </p>
    <label v-if="frames > 1"
      >Frame <input type="number" :value="frame + 1" min="1" :max="frames" @change="select" /> of
      {{ frames }}</label
    >
    <p v-if="pending" role="status">Reading image…</p>
    <p v-else-if="failed">
      Image preview unavailable. This file may be malformed or use an unsupported DICOM format.
    </p>
    <canvas v-else ref="canvas" aria-label="Auto-scaled DICOM image"></canvas>
  </section>
</template>
<style scoped>
.note {
  color: var(--muted);
  font-size: 13px;
}
canvas {
  display: block;
  max-width: 100%;
  height: auto;
  margin: 12px auto;
  background: black;
}
input {
  width: 5em;
}
</style>
