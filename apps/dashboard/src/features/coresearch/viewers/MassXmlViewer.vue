<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { spectrumPlot, type MassXmlPreview } from './mass-xml';
const props = defineProps<{ text: string; extension: string; partial: boolean }>();
const result = ref<MassXmlPreview>(),
  selected = ref(0),
  pending = ref(true);
watch(
  () => [props.text, props.extension, props.partial] as const,
  ([text, ext, partial], _, cleanup) => {
    pending.value = true;
    result.value = undefined;
    selected.value = 0;
    if (partial || text.length > 2 * 1024 * 1024 || typeof Worker === 'undefined') {
      pending.value = false;
      return;
    }
    let worker: Worker;
    try {
      worker = new Worker(new URL('./mass-xml.worker.ts', import.meta.url), { type: 'module' });
    } catch {
      pending.value = false;
      return;
    }
    let live = true;
    const timeout = setTimeout(() => {
      if (live) {
        live = false;
        worker.terminate();
        pending.value = false;
      }
    }, 10000);
    cleanup(() => {
      live = false;
      clearTimeout(timeout);
      worker.terminate();
    });
    worker.onmessage = (event: MessageEvent<MassXmlPreview | null>) => {
      if (!live) return;
      live = false;
      clearTimeout(timeout);
      worker.terminate();
      result.value = event.data ?? undefined;
      pending.value = false;
    };
    worker.onerror = () => {
      if (!live) return;
      live = false;
      clearTimeout(timeout);
      worker.terminate();
      pending.value = false;
    };
    worker.postMessage({ text, extension: ext });
  },
  { immediate: true },
);
const current = computed(() => result.value?.spectra[selected.value]);
const plotted = computed(() => {
  const spectrum = current.value;
  return spectrum && spectrum.minMz !== null && spectrum.maxMz !== null
    ? spectrumPlot(spectrum.peaks, spectrum.minMz, spectrum.maxMz)
    : [];
});
</script>
<template>
  <p v-if="pending">Reading spectra…</p>
  <section v-else-if="result && current" aria-label="Mass spectrometry preview">
    <label
      >Spectrum
      <select v-model.number="selected">
        <option v-for="(s, i) in result.spectra" :key="i" :value="i">
          {{ s.id || `Scan ${i + 1}` }}
        </option>
      </select></label
    >
    <p>{{ current.count }} peaks · summed decoded peak intensity {{ current.tic }}</p>
    <p v-if="current.count > current.peaks.length">
      Peak-preserving sample of {{ current.peaks.length }} points.
    </p>
    <svg
      v-if="plotted.length"
      viewBox="0 0 300 100"
      role="img"
      aria-label="Mass spectrum: m/z versus intensity"
    >
      <line
        v-for="(p, i) in plotted"
        :key="i"
        :x1="p.x"
        :x2="p.x"
        y1="90"
        :y2="p.y"
        stroke="currentColor"
      />
    </svg>
    <p>m/z {{ current.minMz }}–{{ current.maxMz }} · intensity</p>
    <details v-if="result.spectra.length > 1">
      <summary>Summed intensity by scan</summary>
      <ol>
        <li v-for="(s, i) in result.spectra" :key="i">
          {{ s.id || `Scan ${i + 1}` }}: {{ s.tic }}
        </li>
      </ol>
    </details>
  </section>
  <p v-else>Preview unavailable for this incomplete or unsupported spectrum file.</p>
</template>
<style scoped>
svg {
  width: min(100%, 700px);
  height: 180px;
  display: block;
}
p {
  font-size: 12px;
}
select {
  max-width: 75%;
}
</style>
