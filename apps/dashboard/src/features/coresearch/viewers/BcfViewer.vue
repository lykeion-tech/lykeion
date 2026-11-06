<script setup lang="ts">
import { ref, watch } from 'vue';
import type { BcfPreview } from './bcf-preview';
const props = defineProps<{ blob: Blob }>();
const preview = ref<BcfPreview>(),
  pending = ref(true);
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
    cleanup(stop);
    try {
      worker = new Worker(new URL('./bcf-worker.ts', import.meta.url), { type: 'module' });
      timer = setTimeout(() => {
        if (own === generation) {
          stop();
          pending.value = false;
        }
      }, 5000);
      worker.onmessage = (e: MessageEvent<{ preview?: BcfPreview }>) => {
        if (own !== generation) return;
        stop();
        preview.value = e.data.preview;
        pending.value = false;
      };
      worker.onerror = () => {
        if (own === generation) {
          stop();
          pending.value = false;
        }
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
  <p v-if="pending" role="status">Reading BCF file…</p>
  <section v-else-if="preview" aria-label="BCF preview">
    <p>
      Variant site sample only.<template v-if="preview.samples.length">
        Sample columns show GT for VCF 4.5 and earlier; — means GT was not provided. Other FORMAT
        fields are omitted.</template
      ><template v-if="preview.samplesTruncated"> Only the first 16 samples are shown.</template
      ><template v-if="preview.truncated"> Additional records are not shown.</template>
    </p>
    <p v-if="!preview.rows.length">No variant records in this sample.</p>
    <div v-else class="scroll">
      <table>
        <thead>
          <tr>
            <th
              v-for="col in ['CHROM', 'POS', 'ID', 'REF', 'ALT', 'QUAL', 'FILTER', 'INFO']"
              :key="col"
            >
              {{ col }}
            </th>
            <th v-for="sample in preview.samples" :key="sample">{{ sample }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in preview.rows" :key="i">
            <td v-for="(cell, j) in row" :key="j">{{ cell }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
  <p v-else>
    BCF preview unavailable. This file may be malformed, too large, or use an unsupported format.
  </p>
</template>
<style scoped>
p {
  color: var(--muted);
  font-size: 12px;
}
.scroll {
  overflow: auto;
  max-width: 100%;
}
table {
  border-collapse: collapse;
  font-size: 13px;
}
th,
td {
  border: 1px solid var(--border);
  padding: 6px 8px;
  text-align: left;
  white-space: nowrap;
}
</style>
