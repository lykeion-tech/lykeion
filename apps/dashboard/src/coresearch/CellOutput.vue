<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { CellOutput } from './types';
const props = defineProps<{ output: CellOutput }>();
const svgUrl = ref(''),
  imageError = ref(false);
watch(
  () => [props.output.mime, props.output.data],
  (_, __, onCleanup) => {
    svgUrl.value = '';
    imageError.value = false;
    if (
      props.output.type !== 'image' ||
      props.output.mime !== 'image/svg+xml' ||
      !props.output.data
    )
      return;
    try {
      const bytes = Uint8Array.from(atob(props.output.data.replace(/\s+/g, '')), (c) =>
        c.charCodeAt(0),
      );
      const url = URL.createObjectURL(new Blob([bytes], { type: 'image/svg+xml' }));
      svgUrl.value = url;
      onCleanup(() => URL.revokeObjectURL(url));
    } catch {
      imageError.value = true;
    }
  },
  { immediate: true },
);
const image = computed(() =>
  props.output.mime === 'image/png' && props.output.data
    ? `data:image/png;base64,${props.output.data.replace(/\s+/g, '')}`
    : svgUrl.value,
);
const traceback = computed(() =>
  Array.isArray(props.output.traceback) ? props.output.traceback.join('\n') : '',
);
</script>
<template>
  <pre v-if="output.type === 'stream'" class="nbp-out nbp-stream">{{ output.text }}</pre>
  <pre v-else-if="output.type === 'error'" class="nbp-out nbp-error">{{
    traceback || `${output.ename ?? 'Error'}: ${output.evalue ?? ''}`
  }}</pre>
  <div v-else-if="output.type === 'image' && image" class="nbp-out nbp-image">
    <img :src="image" alt="cell output" />
  </div>
  <pre v-else-if="output.type === 'image'" class="nbp-out nbp-result">{{
    imageError
      ? 'Image output could not be decoded.'
      : `Unsupported image output: ${output.mime ?? 'unknown'}`
  }}</pre>
  <pre v-else class="nbp-out nbp-result">{{ output.text }}</pre>
</template>
