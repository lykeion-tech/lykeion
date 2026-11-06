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
      worker = new Worker(new URL('./fits-gzip-worker.ts', import.meta.url), { type: 'module' });
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
  <p v-if="pending" role="status">Reading compressed FITS file…</p>
  <FitsViewer v-else-if="expanded" :blob="expanded" />
  <p v-else>
    Compressed FITS preview unavailable. The file may be invalid or exceed the 32 MiB preview limit.
  </p>
</template>
