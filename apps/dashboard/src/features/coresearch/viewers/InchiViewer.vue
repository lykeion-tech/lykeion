<script setup lang="ts">
import { ref, watch } from 'vue';
import { safeMoleculeSvg } from './smiles';
import { inchiRecord } from './inchi';
const createInchiWorker = () =>
  new Worker(new URL('./inchi.worker.ts', import.meta.url), { type: 'module' });
const props = defineProps<{ text: string; partial: boolean }>();
const state = ref<'loading' | 'ready' | 'unavailable'>('loading'),
  url = ref('');
watch(
  () => [props.text, props.partial] as const,
  ([text, partial], _, cleanup) => {
    state.value = 'loading';
    url.value = '';
    const input = partial ? undefined : inchiRecord(text);
    if (!input || typeof Worker === 'undefined') {
      state.value = 'unavailable';
      return;
    }
    let worker: Worker;
    try {
      worker = createInchiWorker();
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
  <section v-else-if="state === 'ready'" aria-label="InChI molecule preview">
    <p>Structure reconstructed from the InChI; original coordinates are not retained.</p>
    <img :src="url" alt="2D molecular structure" @error="state = 'unavailable'" />
  </section>
  <p v-else>
    Preview unavailable: this InChI is invalid or its structure, including stereochemistry, could
    not be faithfully reconstructed.
  </p>
</template>
<style scoped>
img {
  display: block;
  max-width: 100%;
  width: 400px;
  height: auto;
}
</style>
