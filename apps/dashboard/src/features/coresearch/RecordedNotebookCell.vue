<script setup lang="ts">
import { ref, watch } from 'vue';
import { useCoresearchContent } from './api';
import type { RecordedRun } from './types';
import { langForPath } from './tool-detail';
import CodeBlock from './CodeBlock.vue';

const props = defineProps<{ projectId: string; run: RecordedRun; path: string }>();
const content = useCoresearchContent(
  () => props.projectId,
  () => props.run.script_file_id,
  () => props.run.script_n,
);
const source = ref('');
watch(
  () => content.data.value,
  async (blob, _, cleanup) => {
    let live = true;
    cleanup(() => {
      live = false;
    });
    source.value = '';
    if (blob) {
      const text = await blob.text();
      if (live) source.value = text;
    }
  },
  { immediate: true },
);
</script>
<template>
  <article class="nbp-cell">
    <p v-if="content.isPending.value">Loading recorded script…</p>
    <p v-else-if="content.error.value" role="alert">Recorded script could not be loaded.</p>
    <CodeBlock v-else :code="source" :lang="langForPath(path)" line-numbers>
      <template #meta>{{ path.split('/').at(-1) }}</template>
    </CodeBlock>
    <p v-if="run.failure_reason" role="alert">{{ run.failure_reason }}</p>
    <div v-if="run.facts?.stdout_tail || run.facts?.stderr_tail" class="recorded-output">
      <span>Output</span>
      <pre v-if="run.facts?.stdout_tail" tabindex="0">{{ run.facts.stdout_tail }}</pre>
      <pre v-if="run.facts?.stderr_tail" tabindex="0">{{ run.facts.stderr_tail }}</pre>
    </div>
  </article>
</template>
<style scoped>
.nbp-cell :deep(.code-block-copy) {
  display: none;
}
.recorded-output {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 8px;
  min-width: 0;
}
.recorded-output span {
  font-size: 12px;
  color: var(--muted);
}
.recorded-output pre {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  overflow: auto;
  padding: 10px;
  background: var(--input);
  border-radius: 6px;
  color: var(--text);
  font:
    12px/1.5 'JetBrains Mono Variable',
    ui-monospace,
    monospace;
}
</style>
