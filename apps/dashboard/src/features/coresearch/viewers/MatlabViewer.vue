<script setup lang="ts">
import { ref, watch } from 'vue';
import type { MatlabPreview } from './matlab-preview';
const props = defineProps<{ blob: Blob }>();
const preview = ref<MatlabPreview>(),
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
      worker = new Worker(new URL('./matlab-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (e: MessageEvent<{ preview?: MatlabPreview }>) => {
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
  <p v-if="pending" role="status">Reading MATLAB file…</p>
  <section v-else-if="preview" aria-label="MATLAB preview">
    <p class="note">
      Read-only numeric sample in MATLAB column-major order, up to 200 values per variable.
    </p>
    <label
      >Variable
      <select v-model.number="selected">
        <option v-for="(v, i) in preview.variables" :key="v.name" :value="i">{{ v.name }}</option>
      </select></label
    >
    <template v-if="preview.variables[selected]"
      ><p>
        {{ preview.variables[selected]!.type }} ·
        {{ preview.variables[selected]!.shape.join(' × ') }}
      </p>
      <p v-if="preview.variables[selected]!.truncated" class="note">
        Showing the first 200 stored values.
      </p>
      <ol>
        <li v-for="(value, i) in preview.variables[selected]!.values" :key="i">
          <code>{{ value }}</code>
        </li>
      </ol></template
    >
  </section>
  <p v-else>
    MATLAB preview unavailable. Only supported real numeric arrays in MAT v4, v5, v6 and v7 can be
    previewed.
  </p>
</template>
<style scoped>
.note {
  color: var(--muted);
  font-size: 12px;
}
select {
  margin: 8px;
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
