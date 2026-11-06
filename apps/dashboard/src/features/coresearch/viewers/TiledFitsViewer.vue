<script setup lang="ts">
import { ref, watch } from 'vue';
import FitsViewer from './FitsViewer.vue';
const props = defineProps<{ blob: Blob }>();
const expanded = ref<Blob>(),
  pending = ref(true);
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
    expanded.value = undefined;
    pending.value = true;
    cleanup(stop);
    try {
      worker = new Worker(new URL('./fits-tile-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (e: MessageEvent<{ blob?: Blob }>) => {
        if (own !== generation) return;
        stop();
        expanded.value = e.data.blob;
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
</script>
<template>
  <p v-if="pending" role="status">Reading tiled FITS image…</p>
  <template v-else-if="expanded"
    ><p>
      Showing one image reconstructed from lossless GZIP_1 or RICE_1 tiles; other HDUs are omitted.
    </p>
    <FitsViewer :blob="expanded"
  /></template>
  <p v-else>
    FITS tile preview unavailable. Only bounded, two-dimensional lossless GZIP_1 and integer RICE_1
    images are supported; quantized images are not supported.
  </p>
</template>
