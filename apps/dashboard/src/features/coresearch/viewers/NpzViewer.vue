<script setup lang="ts">
import { ref, watch } from 'vue';
import NpyViewer from './NpyViewer.vue';
const props = defineProps<{ blob: Blob }>();
const names = ref<string[]>([]),
  selected = ref(0),
  array = ref<Blob>(),
  pending = ref(true),
  failed = ref(false);
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
function load(index: number) {
  stop();
  const own = generation;
  pending.value = true;
  failed.value = false;
  array.value = undefined;
  try {
    worker = new Worker(new URL('./npz-worker.ts', import.meta.url), { type: 'module' });
    timer = setTimeout(() => {
      if (own !== generation) return;
      stop();
      pending.value = false;
      failed.value = true;
    }, 5000);
    worker.onmessage = (
      event: MessageEvent<{ names?: string[]; blob?: Blob; error?: boolean }>,
    ) => {
      if (own !== generation) return;
      stop();
      names.value = event.data.names ?? [];
      array.value = event.data.blob;
      failed.value = !!event.data.error || !event.data.blob;
      pending.value = false;
    };
    worker.onerror = () => {
      if (own !== generation) return;
      stop();
      pending.value = false;
      failed.value = true;
    };
    worker.postMessage({ blob: props.blob, index });
  } catch {
    stop();
    pending.value = false;
    failed.value = true;
  }
}
watch(
  () => props.blob,
  (_, __, cleanup) => {
    names.value = [];
    selected.value = 0;
    load(0);
    cleanup(stop);
  },
  { immediate: true },
);
function select(event: Event) {
  const index = Number((event.target as HTMLSelectElement).value);
  if (!Number.isSafeInteger(index) || index < 0 || index >= names.value.length) return;
  selected.value = index;
  load(index);
}
</script>
<template>
  <section aria-label="NumPy archive preview">
    <label v-if="names.length > 1"
      >Array
      <select :value="selected" @change="select">
        <option v-for="(name, i) in names" :key="name" :value="i">{{ name }}</option>
      </select></label
    >
    <p v-if="pending" role="status">Reading archive…</p>
    <p v-else-if="failed">
      Archive preview unavailable. The file may be malformed, too large, or use an unsupported
      format.
    </p>
    <NpyViewer v-else-if="array" :blob="array" />
  </section>
</template>
