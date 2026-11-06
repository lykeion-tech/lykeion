<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import type { TiffPreview } from './tiff-preview';
const props = defineProps<{ blob: Blob }>();
const canvas = ref<HTMLCanvasElement>(),
  page = ref(0),
  preview = ref<TiffPreview>(),
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
    worker = new Worker(new URL('./tiff-worker.ts', import.meta.url), { type: 'module' });
    timer = setTimeout(() => {
      if (own !== generation) return;
      stop();
      pending.value = false;
      failed.value = true;
    }, 5000);
    worker.onmessage = async (event: MessageEvent<{ result?: TiffPreview }>) => {
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
    worker.postMessage({ blob: props.blob, page: index });
  } catch {
    stop();
    pending.value = false;
    failed.value = true;
  }
}
watch(
  () => props.blob,
  (_, __, cleanup) => {
    page.value = 0;
    preview.value = undefined;
    load(0);
    cleanup(stop);
  },
  { immediate: true },
);
function select(event: Event) {
  if (pending.value) return;
  const input = event.target as HTMLInputElement,
    n = Number(input.value) - 1;
  if (!Number.isSafeInteger(n) || n < 0 || n >= (preview.value?.pages ?? 0)) {
    input.value = String(page.value + 1);
    return;
  }
  page.value = n;
  load(n);
}
</script>
<template>
  <section aria-label="TIFF image preview" class="tiff-preview">
    <p class="note">
      Image preview; not for diagnosis. Image pixels may contain identifying information.
    </p>
    <label v-if="preview && preview.pages > 1"
      >Page
      <input
        type="number"
        :value="page + 1"
        min="1"
        :max="preview.pages"
        :disabled="pending"
        @change="select"
      />
      of {{ preview.pages }}</label
    >
    <p v-if="pending" role="status">Reading image…</p>
    <p v-else-if="failed">
      Image preview unavailable. This TIFF may be malformed, too large, or use an unsupported
      format.
    </p>
    <template v-else-if="preview"
      ><p v-if="preview.displayReducedPrecision" class="note">
        This 16-bit image is displayed with 8-bit precision. Download the original for full
        precision.
      </p>
      <p v-if="!preview.spacingKnown" class="note">
        Pixel spacing not specified; square pixels shown.
      </p>
      <div class="image" :style="{ aspectRatio: String(preview.aspect) }">
        <canvas ref="canvas" aria-label="TIFF image"></canvas></div
    ></template>
  </section>
</template>
<style scoped>
.note {
  color: var(--muted);
  font-size: 12px;
}
input {
  width: 5em;
}
.image {
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
