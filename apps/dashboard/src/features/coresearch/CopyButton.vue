<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue';
const props = defineProps<{ text: string; label?: string; code?: boolean }>();
const copied = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;
async function copy() {
  try {
    if (!navigator.clipboard) return;
    await navigator.clipboard.writeText(props.text);
    copied.value = true;
    clearTimeout(timer);
    timer = setTimeout(() => (copied.value = false), props.code ? 1500 : 1200);
  } catch {
    /* The recorded text remains selectable. */
  }
}
onBeforeUnmount(() => clearTimeout(timer));
</script>
<template>
  <button
    type="button"
    :class="code ? 'code-block-copy' : 'quiet-action'"
    :aria-label="label"
    @click="copy"
  >
    <svg :width="code ? 12 : 13" :height="code ? 12 : 13" viewBox="0 0 16 16" fill="none">
      <path v-if="copied" d="m3 8 3 3 7-7" stroke="currentColor" stroke-width="1.2" />
      <g v-else stroke="currentColor" stroke-width="1.2">
        <rect x="5.5" y="5.5" width="7" height="8" rx="1.5" />
        <path d="M10.5 5.5v-2A1 1 0 0 0 9.5 2.5h-6a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h2" />
      </g></svg
    >{{ copied ? 'Copied' : 'Copy' }}
  </button>
</template>
