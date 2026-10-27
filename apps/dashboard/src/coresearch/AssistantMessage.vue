<script setup lang="ts">
import { computed } from 'vue';
import { renderMarkdown } from '../lib/markdown';
import CodeBlock from './CodeBlock.vue';
const props = defineProps<{ text: string; prose?: boolean; live?: boolean }>();
// Split only the trusted fence markup produced by the shared html:false renderer.
// Raw HTML from the message remains escaped in every prose fragment.
const parts = computed(() => {
  const rendered = renderMarkdown(props.text),
    parts: { html?: string; code?: string; lang?: string }[] = [];
  const fence =
    /<div class="markdown-code"><div class="code-toolbar"><span>(.*?)<\/span><button type="button" data-copy-code="(\d+)">Copy code<\/button><\/div><pre tabindex="0"><code>[\s\S]*?<\/code><\/pre><\/div>\n/g;
  let start = 0;
  for (const match of rendered.html.matchAll(fence)) {
    parts.push({ html: rendered.html.slice(start, match.index) });
    parts.push({
      code: rendered.codes[Number(match[2])],
      lang: match[1] === 'Code' ? undefined : match[1],
    });
    start = match.index! + match[0].length;
  }
  parts.push({ html: rendered.html.slice(start) });
  return parts;
});
</script>
<template>
  <div :class="prose ? 'md-prose' : 'msg msg--assistant'">
    <template v-for="(part, i) in parts" :key="i"
      ><CodeBlock
        v-if="part.code !== undefined"
        :code="part.code.replace(/\n$/, '')"
        :lang="part.lang"
        :live="live" /><!-- Shared Markdown renderer escapes untrusted HTML. -->
      <!-- eslint-disable-next-line vue/no-v-html -- shared html:false renderer escapes all untrusted content -->
      <div v-else class="coresearch-prose-fragment" v-html="part.html"
    /></template>
  </div>
</template>
<style scoped>
.coresearch-prose-fragment:empty {
  display: none;
}
.coresearch-prose-fragment {
  display: contents;
}
:deep(.markdown-table) {
  overflow-x: auto;
  max-width: 100%;
}
/* Splitting fences into Vue components must not turn each prose fragment into
   a separate first/last paragraph for the source spacing rules. */
.coresearch-prose-fragment:not(:last-child) :deep(> :is(p, ul, ol, blockquote):last-child) {
  margin-bottom: 6px;
}
.coresearch-prose-fragment:not(:first-child) :deep(> :is(p, ul, ol, blockquote):first-child) {
  margin-top: 6px;
}
.coresearch-prose-fragment:not(:first-child) :deep(> :is(h1, h2, h3):first-child) {
  margin-top: 12px;
}
</style>
