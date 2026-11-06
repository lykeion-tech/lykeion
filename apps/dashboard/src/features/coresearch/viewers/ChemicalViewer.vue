<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import library from '3dmol/build/3Dmol.es6-min.js?raw';
import { chemicalInput } from './chemical-guard';
import { chemicalFrame } from './chemical-frame';
const props = defineProps<{ text: string; extension: string; partial: boolean }>();
const frame = ref<HTMLIFrameElement>(),
  state = ref<'loading' | 'ready' | 'unavailable'>('loading');
const src = computed(() => {
  const input = chemicalInput(props.text, props.extension, props.partial);
  return input ? chemicalFrame(library, input.text, input.format, input.atoms) : undefined;
});
watch(
  src,
  (value, _, cleanup) => {
    state.value = value ? 'loading' : 'unavailable';
    if (!value) return;
    const timer = setTimeout(() => {
      if (state.value === 'loading') state.value = 'unavailable';
    }, 10000);
    cleanup(() => clearTimeout(timer));
  },
  { immediate: true },
);
function onMessage(event: MessageEvent) {
  if (event.source !== frame.value?.contentWindow) return;
  if (event.data?.lykeionChemical === 'ready') state.value = 'ready';
  else if (event.data?.lykeionChemical === 'unavailable') state.value = 'unavailable';
}
onMounted(() => window.addEventListener('message', onMessage));
onBeforeUnmount(() => window.removeEventListener('message', onMessage));
</script>
<template>
  <section class="chemical-preview" aria-label="Molecular structure preview">
    <p v-if="!src || state === 'unavailable'">
      Preview unavailable for this unsupported structure or graphics environment.
    </p>
    <template v-else>
      <p v-if="state === 'loading'">Loading structure…</p>
      <iframe
        ref="frame"
        :key="src"
        :srcdoc="src"
        sandbox="allow-scripts"
        title="Interactive molecular structure"
        class="chemical-canvas"
      ></iframe>
      <p>Drag to rotate · scroll to zoom</p>
    </template>
  </section>
</template>
<style scoped>
.chemical-canvas {
  width: 100%;
  min-height: 300px;
  height: min(55vh, 500px);
  border: 0;
}
p {
  color: var(--muted);
  font-size: 12px;
}
</style>
