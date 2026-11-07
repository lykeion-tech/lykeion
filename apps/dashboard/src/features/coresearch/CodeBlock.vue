<script setup lang="ts">
import { ref, watch } from 'vue';
import type { highlightToolCode } from '../../lib/tool-code';
import CopyButton from './CopyButton.vue';
const props = defineProps<{ code: string; lang?: string; live?: boolean; lineNumbers?: boolean }>();
const lines = ref<Awaited<ReturnType<typeof highlightToolCode>>>();
watch(
  () => [props.code, props.lang, props.live],
  async (_, __, onCleanup) => {
    let cancelled = false;
    onCleanup(() => (cancelled = true));
    lines.value = undefined;
    if (!props.lang) return;
    try {
      const { highlightToolCode } = await import('../../lib/tool-code');
      const result = await highlightToolCode(
        props.code,
        ({ ts: 'typescript', js: 'javascript', sh: 'bash' } as Record<string, string>)[
          props.lang
        ] ?? props.lang,
      );
      if (!cancelled) lines.value = result;
    } catch {
      /* Unsupported content stays plain. */
    }
  },
  { immediate: true },
);
</script>
<template>
  <div class="code-block" :class="{ 'code-block--numbered': lineNumbers }">
    <div class="code-block-head">
      <slot name="meta"
        ><span v-if="lang" class="code-block-lang">{{ lang }}</span></slot
      ><CopyButton :text="code" code />
    </div>
    <div v-if="lines" class="code-block-pre" data-testid="code-block-pre">
      <pre
        style="background-color: #24292e; color: #e1e4e8"
      ><code><template v-for="(line,i) in lines" :key="i"><span class="line"><span v-for="(token,j) in line" :key="j" :style="{color:token.color}">{{ token.content }}</span></span>{{ i < lines.length - 1 ? '\n' : '' }}</template></code></pre>
    </div>
    <pre
      v-else
      class="code-block-pre"
      data-testid="code-block-pre"
    ><code v-if="lineNumbers"><span v-for="(line,i) in code.replace(/\n$/, '').split('\n')" :key="i" class="line">{{ line }}{{ '\n' }}</span></code><code v-else>{{ code }}</code></pre>
  </div>
</template>
<style scoped>
.code-block {
  min-width: 0;
  max-width: 100%;
  border: 1px solid var(--hairline, var(--border));
  border-radius: 8px;
  overflow: hidden;
  background: var(--surface-1, #fff);
}
.code-block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--hairline, var(--border));
  background: var(--surface-2, #f5f5f5);
}
.code-block-lang {
  font-size: 12px;
  color: var(--ink-tertiary, #666);
}
.code-block-head :deep(.code-block-copy) {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
  padding: 2px 6px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--ink-subtle, #555);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}
.code-block-pre {
  margin: 0;
  overflow-x: auto;
  font-family: var(--font-mono, monospace);
  font-size: 13px;
  line-height: 1.5;
}
pre.code-block-pre {
  padding: 8px 10px;
}
.code-block-pre > pre {
  margin: 0;
  min-width: 100%;
  width: max-content;
  box-sizing: border-box;
  padding: 8px 10px;
}
.code-block-pre code {
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
}
.code-block--numbered .code-block-pre code {
  counter-reset: notebook-line;
}
.code-block--numbered .code-block-pre .line::before {
  counter-increment: notebook-line;
  content: counter(notebook-line);
  display: inline-block;
  width: 2.25em;
  margin-right: 0.85em;
  text-align: right;
  color: #8b949e;
  opacity: 0.8;
  user-select: none;
}
</style>
