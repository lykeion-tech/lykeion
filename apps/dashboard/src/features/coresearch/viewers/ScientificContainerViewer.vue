<script setup lang="ts">
import { ref, watch } from 'vue';
import NetcdfViewer from './NetcdfViewer.vue';
import MatlabViewer from './MatlabViewer.vue';
import TargetedHdfViewer from './TargetedHdfViewer.vue';
const props = defineProps<{ blob: Blob; format: 'netcdf' | 'matlab' }>();
const target = ref<boolean>();
watch(
  () => [props.blob, props.format] as const,
  async ([blob, format], _, cleanup) => {
    let live = true;
    cleanup(() => {
      live = false;
    });
    target.value = undefined;
    try {
      const bytes = new Uint8Array(await blob.slice(0, 520).arrayBuffer());
      const offset = format === 'matlab' ? 512 : 0;
      const signature = [137, 72, 68, 70, 13, 10, 26, 10];
      if (live) target.value = signature.every((v, i) => bytes[offset + i] === v);
    } catch {
      if (live) target.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="target === undefined" role="status">Reading file…</p>
  <TargetedHdfViewer v-else-if="target" :blob="blob" :mode="format" /><NetcdfViewer
    v-else-if="format === 'netcdf'"
    :blob="blob"
  /><MatlabViewer v-else :blob="blob" />
</template>
