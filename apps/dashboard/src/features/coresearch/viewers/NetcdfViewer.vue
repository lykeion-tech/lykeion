<script setup lang="ts">
import { ref, watch } from 'vue';
import type { NetcdfPreview, NetcdfVariable } from './netcdf-classic';
function dimensions(v: NetcdfVariable) {
  return v.dimensions.length
    ? v.dimensions.map((d, i) => `${d}: ${v.shape[i]}`).join(' × ')
    : 'scalar';
}
const props = defineProps<{ blob: Blob }>();
const preview = ref<NetcdfPreview>(),
  pending = ref(true),
  selected = ref(0);
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
    pending.value = true;
    preview.value = undefined;
    selected.value = 0;
    cleanup(stop);
    try {
      worker = new Worker(new URL('./netcdf-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (e: MessageEvent<{ preview?: NetcdfPreview }>) => {
        if (own !== generation) return;
        stop();
        preview.value = e.data.preview;
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
  <p v-if="pending" role="status">Reading NetCDF file…</p>
  <section v-else-if="preview" aria-label="NetCDF preview">
    <p class="note">
      Read-only stored values; scale, offset, units and fill attributes are not applied. Up to 200
      values per variable are shown in row-major order.
    </p>
    <details v-if="Object.keys(preview.globalAttributes).length">
      <summary>Global attributes</summary>
      <dl>
        <template v-for="(value, key) in preview.globalAttributes" :key="key"
          ><dt>
            {{ key }} <span>{{ preview.globalAttributeTypes[key] }}</span>
          </dt>
          <dd>{{ value }}</dd></template
        >
      </dl>
    </details>
    <label v-if="preview.variables.length"
      >Variable
      <select v-model.number="selected">
        <option v-for="(v, i) in preview.variables" :key="v.name" :value="i">{{ v.name }}</option>
      </select></label
    >
    <p v-else>No variables.</p>
    <template v-if="preview.variables[selected]">
      <p>
        {{ preview.variables[selected]!.type }} ·
        {{ dimensions(preview.variables[selected]!) }}
      </p>
      <details v-if="Object.keys(preview.variables[selected]!.attributes).length">
        <summary>Variable attributes</summary>
        <dl>
          <template v-for="(value, key) in preview.variables[selected]!.attributes" :key="key"
            ><dt>
              {{ key }} <span>{{ preview.variables[selected]!.attributeTypes[key] }}</span>
            </dt>
            <dd>{{ value }}</dd></template
          >
        </dl>
      </details>
      <p v-if="preview.variables[selected]!.truncated" class="note">
        Showing the first 200 stored values.
      </p>
      <ol>
        <li v-for="(value, i) in preview.variables[selected]!.values" :key="i">
          <code>{{ value }}</code>
        </li>
      </ol>
    </template>
  </section>
  <p v-else>
    NetCDF preview unavailable. This preview currently supports classic CDF-1, CDF-2 and CDF-5 files
    only.
  </p>
</template>
<style scoped>
.note,
dt span {
  color: var(--muted);
  font-size: 12px;
}
select {
  margin: 8px;
}
dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 12px;
  overflow-wrap: anywhere;
}
dd {
  margin: 0;
}
ol {
  max-height: 400px;
  overflow: auto;
  padding-left: 3em;
}
li {
  padding: 2px 8px;
}
</style>
