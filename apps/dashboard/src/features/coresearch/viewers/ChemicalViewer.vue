<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import library from '3dmol/build/3Dmol.es6-min.js?raw';
import { chemicalInput } from './chemical-guard';
import { chemicalFrame } from './chemical-frame';
import { sdfGallery } from './sdf-gallery';
const props = defineProps<{ text: string; extension: string; partial: boolean }>();
const frame = ref<HTMLIFrameElement>(),
  state = ref<'loading' | 'ready' | 'unavailable'>('loading'),
  selected = ref(0);
const gallery = computed(() =>
  props.extension === 'sdf' ? sdfGallery(props.text, props.partial) : undefined,
);
watch(
  () => [props.text, props.extension, props.partial],
  () => {
    selected.value = 0;
  },
  { flush: 'sync' },
);
const src = computed(() => {
  const input = gallery.value
    ? gallery.value.items[selected.value]?.input
    : props.extension === 'sdf' && /^\s*\$\$\$\$\s*$/m.test(props.text)
      ? undefined
      : chemicalInput(props.text, props.extension, props.partial);
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
      <div
        v-if="gallery && gallery.total > 1"
        class="molecule-gallery"
        role="group"
        aria-label="Molecules"
      >
        <button
          v-for="(item, index) in gallery.items"
          :key="index"
          type="button"
          class="molecule-card"
          :aria-pressed="selected === index"
          @click="selected = index"
        >
          <strong>{{ item.name }}</strong
          ><span>{{ item.input.atoms }} atoms</span>
        </button>
      </div>
      <p v-if="gallery && gallery.total > gallery.items.length">
        Showing the first {{ gallery.items.length }} of {{ gallery.total }} molecules.
      </p>
      <p v-if="gallery">SDF properties are not shown.</p>
      <p v-if="state === 'loading'">Loading structure…</p>
      <iframe
        ref="frame"
        :key="src"
        :srcdoc="src"
        sandbox="allow-scripts"
        title="Interactive molecular structure"
        class="chemical-canvas"
      ></iframe>
      <p>
        Drag to rotate · scroll to zoom<span
          v-if="['cif', 'mmcif', 'xyz', 'gro', 'pdbqt'].includes(extension)"
        >
          · bonds inferred from coordinates</span
        >
      </p>
      <p v-if="extension === 'pdbqt'">PDBQT charges and torsion data are not shown.</p>
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
.molecule-gallery {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
  gap: 8px;
}
.molecule-card {
  min-width: 0;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  color: inherit;
  text-align: left;
  cursor: pointer;
}
.molecule-card[aria-pressed='true'] {
  border-color: var(--accent, var(--text));
}
.molecule-card strong,
.molecule-card span {
  display: block;
  overflow-wrap: anywhere;
}
.molecule-card span {
  margin-top: 4px;
  color: var(--muted);
  font-size: 12px;
}
p {
  color: var(--muted);
  font-size: 12px;
}
</style>
