<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import type { NiftiPreview } from './nifti-preview';
const props = defineProps<{ blob: Blob; compressed: boolean }>();
const canvas = ref<HTMLCanvasElement>(),
  axis = ref(2),
  index = ref(0),
  preview = ref<NiftiPreview>(),
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
function load() {
  stop();
  const own = generation;
  pending.value = true;
  failed.value = false;
  try {
    worker = new Worker(new URL('./nifti-worker.ts', import.meta.url), { type: 'module' });
    timer = setTimeout(() => {
      if (own !== generation) return;
      stop();
      pending.value = false;
      failed.value = true;
    }, 5000);
    worker.onmessage = async (event: MessageEvent<{ result?: NiftiPreview }>) => {
      if (own !== generation) return;
      const result = event.data.result;
      stop();
      const ticket = generation;
      if (!result) {
        pending.value = false;
        failed.value = true;
        return;
      }
      preview.value = result;
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
    worker.postMessage({
      blob: props.blob,
      axis: axis.value,
      index: index.value,
      compressed: props.compressed,
    });
  } catch {
    stop();
    pending.value = false;
    failed.value = true;
  }
}
watch(
  () => [props.blob, props.compressed] as const,
  (_, __, cleanup) => {
    axis.value = 2;
    index.value = 0;
    preview.value = undefined;
    load();
    cleanup(stop);
  },
  { immediate: true },
);
function chooseAxis(event: Event) {
  if (pending.value) return;
  const n = Number((event.target as HTMLSelectElement).value);
  if (![0, 1, 2].includes(n)) return;
  axis.value = n;
  index.value = 0;
  load();
}
function chooseSlice(event: Event) {
  if (pending.value || failed.value) return;
  const input = event.target as HTMLInputElement,
    n = Number(input.value) - 1;
  if (!Number.isSafeInteger(n) || n < 0 || n >= (preview.value?.sliceCount ?? 0)) {
    input.value = String(index.value + 1);
    return;
  }
  index.value = n;
  load();
}
</script>
<template>
  <section aria-label="NIfTI image preview" class="nifti-preview">
    <p class="note">
      Auto-scaled image preview; not for diagnosis. Image pixels may contain identifying
      information.
    </p>
    <label
      >Plane
      <select :value="axis" :disabled="pending" @change="chooseAxis">
        <option :value="0">{{ preview?.anatomical ? 'Sagittal' : 'Native axis 1' }}</option>
        <option :value="1">{{ preview?.anatomical ? 'Coronal' : 'Native axis 2' }}</option>
        <option :value="2">{{ preview?.anatomical ? 'Axial' : 'Native axis 3' }}</option>
      </select></label
    >
    <label v-if="preview"
      >Slice
      <input
        type="number"
        :value="index + 1"
        min="1"
        :max="preview.sliceCount"
        :disabled="pending || failed"
        @change="chooseSlice"
      />
      of {{ preview.sliceCount }}</label
    >
    <p v-if="pending" role="status">Reading image…</p>
    <p v-else-if="failed">
      Image preview unavailable. This file may be malformed, too large, or use an unsupported NIfTI
      format.
    </p>
    <template v-else-if="preview"
      ><p class="note">
        {{ preview.plane
        }}{{ preview.anatomical ? '' : ' · native voxel plane; anatomical orientation unavailable'
        }}<span v-if="!preview.spacingApplied"> · voxel spacing unavailable</span>
      </p>
      <div class="plane" :style="{ aspectRatio: String(preview.aspect) }">
        <canvas ref="canvas" :aria-label="`${preview.plane} image`"></canvas></div
    ></template>
  </section>
</template>
<style scoped>
.note {
  color: var(--muted);
  font-size: 13px;
}
label {
  margin-right: 12px;
}
input {
  width: 5em;
}
.plane {
  width: min(100%, 600px);
  margin: 12px auto;
  background: black;
}
canvas {
  display: block;
  width: 100%;
  height: 100%;
}
</style>
