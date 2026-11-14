<script setup lang="ts">
import { ref, watch } from 'vue';
import { safeMoleculeSvg, smilesRecord } from './smiles';
import { createSmilesWorker } from './smiles-worker-factory';
const props = defineProps<{ text: string; partial: boolean }>();
const state = ref<'loading' | 'ready' | 'unavailable'>('loading'),
  url = ref('');
watch(
  () => [props.text, props.partial] as const,
  ([text, partial], _, cleanup) => {
    state.value = 'loading';
    url.value = '';
    const input = partial ? undefined : smilesRecord(text);
    if (!input || typeof Worker === 'undefined') {
      state.value = 'unavailable';
      return;
    }
    let worker: Worker;
    try {
      worker = createSmilesWorker();
    } catch {
      state.value = 'unavailable';
      return;
    }
    let active = true,
      objectUrl = '';
    const done = () => {
      active = false;
      clearTimeout(timer);
      worker.terminate();
    };
    const timer = setTimeout(() => {
      if (active) {
        done();
        state.value = 'unavailable';
      }
    }, 10000);
    cleanup(() => {
      done();
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    });
    worker.onmessage = (event: MessageEvent<string | null>) => {
      if (!active) return;
      done();
      if (typeof event.data !== 'string' || !safeMoleculeSvg(event.data)) {
        state.value = 'unavailable';
        return;
      }
      objectUrl = URL.createObjectURL(new Blob([event.data], { type: 'image/svg+xml' }));
      url.value = objectUrl;
      state.value = 'ready';
    };
    worker.onerror = () => {
      if (active) {
        done();
        state.value = 'unavailable';
      }
    };
    worker.postMessage(input);
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="state === 'loading'">Drawing molecule…</p>
  <section v-else-if="state === 'ready'" aria-label="SMILES molecule preview">
    <img :src="url" alt="2D molecular structure" @error="state = 'unavailable'" />
  </section>
  <p v-else>Preview unavailable for this invalid or unsupported SMILES file.</p>
</template>
<style scoped>
img {
  display: block;
  max-width: 100%;
  width: 400px;
  height: auto;
}
</style>
