<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Icon from '../../../../ui/Icon.vue';
import RecordedMarkdown from '../../../../ui/RecordedMarkdown.vue';
import type { highlightToolCode } from '../../../../lib/tool-code';

const props = defineProps<{ path: string; content: string }>();
const markdown = computed(() => /\.(md|markdown)$/i.test(props.path));
const python = computed(() => /\.(py|pyw)$/i.test(props.path));
const mode = ref<'preview' | 'source'>('preview');
const renderedContent = computed(() =>
  props.path.toLowerCase().endsWith('skill.md')
    ? props.content.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, '')
    : props.content,
);
const tokens = ref<Awaited<ReturnType<typeof highlightToolCode>>>();
watch(
  () => [props.path, props.content] as const,
  async ([, content], _, onCleanup) => {
    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });
    tokens.value = undefined;
    if (!python.value) return;
    try {
      const { highlightToolCode } = await import('../../../../lib/tool-code');
      const result = await highlightToolCode(content, 'python');
      if (!cancelled) tokens.value = result;
    } catch {
      // Keep the original text visible if highlighting is unavailable.
    }
  },
  { immediate: true },
);
</script>

<template>
  <section class="file-viewer" aria-label="File preview">
    <header class="file-toolbar" :class="{ 'python-toolbar': python && tokens }">
      <span class="file-path">/{{ path }}</span>
      <div v-if="markdown" class="view-modes" aria-label="Markdown display mode">
        <button
          type="button"
          aria-label="Rendered Markdown"
          :aria-pressed="mode === 'preview'"
          title="Rendered Markdown"
          @click="mode = 'preview'"
        >
          <Icon name="eye" :size="16" />
        </button>
        <button
          type="button"
          aria-label="Markdown source"
          :aria-pressed="mode === 'source'"
          title="Markdown source"
          @click="mode = 'source'"
        >
          <Icon name="code" :size="16" />
        </button>
      </div>
    </header>
    <RecordedMarkdown
      v-if="markdown && mode === 'preview'"
      class="file-markdown"
      :content="renderedContent"
    />
    <pre
      v-else-if="python && tokens"
      class="file-code"
    ><code><template v-for="(line, i) in tokens" :key="i"><span class="code-line"><span class="line-number" aria-hidden="true">{{ i + 1 }}</span><span v-for="(token, j) in line" :key="j" :style="{ color: token.color }">{{ token.content }}</span></span>{{ i < tokens.length - 1 ? '\n' : '' }}</template></code></pre>
    <pre v-else class="file-source">{{ content }}</pre>
  </section>
</template>

<style scoped>
.file-viewer {
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 7px;
  overflow: hidden;
}
.file-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 40px;
  padding: 5px 10px;
}
.file-path {
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--muted);
  font:
    12px/1.5 ui-monospace,
    monospace;
}
.python-toolbar {
  background: #24292e;
}
.python-toolbar .file-path {
  color: #b1bac4;
}
.view-modes {
  display: flex;
  flex: none;
  background: var(--input);
  border-radius: 6px;
  padding: 2px;
}
.view-modes button {
  display: grid;
  place-items: center;
  border: 0;
  border-radius: 4px;
  padding: 5px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.view-modes button[aria-pressed='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 0 0 1px var(--border);
}
.view-modes button:focus-visible {
  outline: 2px solid var(--primary-text);
}
.file-markdown {
  padding: 18px;
}
.file-source,
.file-code {
  margin: 0;
  padding: 14px;
  overflow: auto;
  font:
    12px/1.7 ui-monospace,
    monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  tab-size: 4;
}
.file-code {
  background: #24292e;
  color: #e1e4e8;
}
.code-line {
  display: inline;
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
