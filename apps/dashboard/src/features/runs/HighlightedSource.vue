<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { highlightToolCode } from '../../lib/tool-code';
const props = defineProps<{ code: string; language: string }>();
const tokens = ref<Awaited<ReturnType<typeof highlightToolCode>>>();
const lines = computed<{ content: string; color?: string }[][]>(() =>
  tokens.value ?? props.code.split('\n').map((content) => [{ content }]),
);
watch(
  () => [props.code, props.language] as const,
  async ([code, language], _, onCleanup) => {
    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });
    tokens.value = undefined;
    try {
      const { highlightToolCode } = await import('../../lib/tool-code');
      const result = await highlightToolCode(code, language);
      if (!cancelled) tokens.value = result;
    } catch {
      /* Keep recorded source readable. */
    }
  },
  { immediate: true },
);
</script>
<template>
  <pre
    tabindex="0"
    :class="{ highlighted: tokens }"
  ><code><template v-for="(line, i) in lines" :key="i"><span class="source-line"><span class="line-number" aria-hidden="true">{{ i + 1 }}</span><span v-for="(token, j) in line" :key="j" :style="{ color: token.color }">{{ token.content }}</span></span>{{ i < lines.length - 1 ? '\n' : '' }}</template></code></pre>
</template>
<style scoped>
pre {
  margin: 0;
  overflow: auto;
  padding: 12px;
  font:
    12px/1.6 ui-monospace,
    monospace;
}
pre.highlighted {
  background: #24292e;
  color: #e1e4e8;
}
.line-number {
  display: inline-block;
  width: 2.5em;
  margin-right: 1em;
  text-align: right;
  color: #959da5;
  user-select: none;
}
</style>
