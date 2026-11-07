<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { parseSpectrum, spectrumSticks, spectrumEnvelope, spectrumRatio } from './spectra';
const props = defineProps<{ text: string; extension: string; partial: boolean }>();
const parsed = computed(() => parseSpectrum(props.text, props.extension, props.partial));
const selected = ref(0);
watch(parsed, () => {
  selected.value = 0;
});
const points = computed(() => parsed.value?.scans[selected.value]?.points ?? []);
const bounds = computed(() => {
  let minX = Infinity,
    maxX = -Infinity,
    minY = Infinity,
    maxY = -Infinity;
  for (const [x, y] of points.value) {
    minX = Math.min(minX, x);
    maxX = Math.max(maxX, x);
    minY = Math.min(minY, y);
    maxY = Math.max(maxY, y);
  }
  if (props.extension === 'mgf') {
    minY = Math.min(0, minY);
    maxY = Math.max(0, maxY);
  }
  return { minX, maxX, minY, maxY };
});
const scale = spectrumRatio;
const stickPath = computed(() => {
  const { minX, maxX, minY, maxY } = bounds.value;
  const baseline = 180 - scale(0, minY, maxY) * 160;
  return spectrumSticks(points.value, 1000)
    .map(([x, y]) => {
      const px = 20 + scale(x, minX, maxX) * 560;
      const py = 180 - scale(y, minY, maxY) * 160;
      return `M${px} ${baseline}V${py}`;
    })
    .join(' ');
});
const plotted = computed(() => {
  const source = points.value;
  if (!source.length) return '';
  const { minX, maxX, minY, maxY } = bounds.value;
  return spectrumEnvelope(source, 500)
    .map(([x, y]) => `${20 + scale(x, minX, maxX) * 560},${180 - scale(y, minY, maxY) * 160}`)
    .join(' ');
});
</script>
<template>
  <section v-if="parsed" class="spectrum-viewer" aria-label="Spectrum preview">
    <label v-if="parsed.scans.length > 1"
      >Scan
      <select v-model.number="selected">
        <option v-for="(scan, index) in parsed.scans" :key="index" :value="index">
          {{ scan.name }}
        </option>
      </select>
    </label>
    <h3>{{ parsed.scans[selected]?.name }}</h3>
    <svg viewBox="0 0 600 200" role="img" :aria-label="`Spectrum with ${points.length} points`">
      <path d="M20 20 V180 H580" fill="none" stroke="currentColor" />
      <polyline
        v-if="extension !== 'mgf'"
        :points="plotted"
        fill="none"
        stroke="var(--primary-text, #2563a6)"
        stroke-width="1.5"
      />
      <path
        v-else
        :d="stickPath"
        fill="none"
        stroke="var(--primary-text, #2563a6)"
        stroke-width="1.5"
      />
    </svg>
    <p>
      Horizontal<span v-if="extension !== 'mgf'">
        ({{ parsed.scans[selected]?.xUnit || 'unit unspecified' }})</span
      >: {{ bounds.minX }} to {{ bounds.maxX }} · Vertical<span v-if="extension !== 'mgf'">
        ({{ parsed.scans[selected]?.yUnit || 'unit unspecified' }})</span
      >: {{ bounds.minY }} to {{ bounds.maxY }}
    </p>
    <p>
      {{ points.length }} points<span v-if="points.length > 1000">
        ·
        {{
          extension === 'mgf' ? 'peaks aggregated for display' : 'extrema retained for display'
        }}</span
      >
    </p>
  </section>
  <p v-else role="status">
    Spectrum preview unavailable. The file may be incomplete, too large, malformed, or use an
    unsupported encoding.
  </p>
</template>
<style scoped>
.spectrum-viewer {
  min-width: 0;
}
.spectrum-viewer h3 {
  font-size: 14px;
}
.spectrum-viewer svg {
  display: block;
  width: 100%;
  min-height: 140px;
  max-height: 340px;
  color: var(--muted);
}
.spectrum-viewer p {
  color: var(--muted);
  font-size: 13px;
}
</style>
