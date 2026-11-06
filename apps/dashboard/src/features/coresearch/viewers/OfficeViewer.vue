<script setup lang="ts">
import { ref, watch } from 'vue';
import { officeText } from './office-text';
import type { OfficeParts } from './office-preview';
const props = defineProps<{ blob: Blob; format: 'docx' | 'pptx' }>();
const pages = ref<string[][]>();
const pending = ref(true);
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
  () => [props.blob, props.format] as const,
  ([blob, format], _, cleanup) => {
    stop();
    const own = generation;
    pending.value = true;
    pages.value = undefined;
    cleanup(stop);
    try {
      worker = new Worker(new URL('./office-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      }, 5000);
      worker.onmessage = (event: MessageEvent<{ parts?: OfficeParts }>) => {
        if (own !== generation) return;
        stop();
        pages.value = event.data.parts ? officeText(event.data.parts, format) : undefined;
        pending.value = false;
      };
      worker.onerror = () => {
        if (own !== generation) return;
        stop();
        pending.value = false;
      };
      worker.postMessage({ blob, format });
    } catch {
      stop();
      pending.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="pending" role="status">Reading document…</p>
  <section v-else-if="pages" class="office-preview" aria-label="Document preview">
    <p class="office-note">
      Text preview only; headers, footers and notes may be omitted. Download the file to see its
      original layout and images.
    </p>
    <article v-for="(page, i) in pages" :key="i">
      <h3 v-if="format === 'pptx'">Slide {{ i + 1 }}</h3>
      <p v-for="(paragraph, j) in page" :key="j">{{ paragraph }}</p>
      <p v-if="!page.length" class="office-note">
        No text on this {{ format === 'pptx' ? 'slide' : 'document' }}.
      </p>
    </article>
    <p v-if="!pages.length" class="office-note">This presentation has no slides.</p>
  </section>
  <p v-else>
    Document preview unavailable. The file may be malformed, too large, or use an unsupported
    format.
  </p>
</template>
<style scoped>
.office-note {
  color: var(--muted);
  font-size: 12px;
}
article {
  margin: 12px 0;
  padding: 18px;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow-wrap: anywhere;
}
article p {
  white-space: pre-wrap;
}
</style>
