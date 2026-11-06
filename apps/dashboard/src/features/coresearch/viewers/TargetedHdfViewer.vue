<script setup lang="ts">
import { ref, watch } from 'vue';
import type { TargetedHdfPreview } from './targeted-hdf/targeted-reader';
const props = defineProps<{ blob: Blob; mode: 'netcdf' | 'matlab' }>();
const preview = ref<TargetedHdfPreview>(),
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
  () => [props.blob, props.mode] as const,
  ([blob, mode], _, cleanup) => {
    stop();
    const own = generation;
    preview.value = undefined;
    pending.value = true;
    selected.value = 0;
    cleanup(stop);
    try {
      worker = new Worker(new URL('./targeted-hdf/targeted-worker.ts', import.meta.url), {
        type: 'module',
      });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (e: MessageEvent<{ preview?: TargetedHdfPreview }>) => {
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
      worker.postMessage({ blob, mode });
    } catch {
      stop();
      pending.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="pending" role="status">Reading file…</p>
  <section v-else-if="preview" aria-label="Scientific data preview">
    <p class="note">
      Read-only sample of supported top-level numeric datasets; attributes and nested groups are not
      shown. Values are not scaled.
      <template v-if="mode === 'matlab'">Values follow MATLAB column-major order.</template>
    </p>
    <p v-if="preview.partial" class="note">Some data is omitted or unsupported.</p>
    <p v-if="!preview.items.length">No supported numeric datasets available.</p>
    <template v-else
      ><label
        >Dataset
        <select v-model.number="selected">
          <option v-for="(v, i) in preview.items" :key="v.name" :value="i">{{ v.name }}</option>
        </select></label
      >
      <template v-if="preview.items[selected]"
        ><p>
          {{ preview.items[selected]!.type }} ·
          {{
            preview.items[selected]!.shape.length
              ? preview.items[selected]!.shape.join(' × ')
              : 'scalar'
          }}
        </p>
        <ol>
          <li v-for="(value, i) in preview.items[selected]!.values" :key="i">
            <code>{{ value }}</code>
          </li>
        </ol></template
      >
    </template>
  </section>
  <p v-else>Preview unavailable for this file or its storage features.</p>
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
