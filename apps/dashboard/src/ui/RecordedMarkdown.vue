<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { renderMarkdown } from '../lib/markdown';
const props = withDefaults(defineProps<{ content: string; breaks?: boolean }>(), { breaks: true });
const rendered = computed(() => renderMarkdown(props.content, props.breaks));
const body = ref<HTMLElement>();
const notice = ref('');
const timers = new Map<HTMLButtonElement, ReturnType<typeof setTimeout>>();
let revision = 0;
function clearFeedback() {
  revision++;
  for (const timer of timers.values()) clearTimeout(timer);
  timers.clear();
  notice.value = '';
}
watch(() => props.content, clearFeedback);
watch(
  [rendered, body],
  async ([value, element], _, onCleanup) => {
    let cancelled = false;
    onCleanup(() => (cancelled = true));
    if (!element) return;
    const blocks = [...element.querySelectorAll<HTMLElement>('pre code[data-language]')];
    if (!blocks.some((block) => block.dataset.language)) return;
    try {
      const { highlightToolCode } = await import('../lib/tool-code');
      await Promise.all(
        blocks.map(async (block, index) => {
          const language = block.dataset.language;
          if (!language) return;
          const lines = await highlightToolCode(value.codes[index] ?? '', language);
          if (cancelled || !lines) return;
          const fragment = document.createDocumentFragment();
          lines.forEach((line, lineIndex) => {
            if (lineIndex) fragment.append(document.createTextNode('\n'));
            line.forEach((token) => {
              const span = document.createElement('span');
              span.textContent = token.content;
              if (token.color) span.style.color = token.color;
              fragment.append(span);
            });
          });
          block.replaceChildren(fragment);
          block.parentElement?.classList.add('syntax-highlighted');
        }),
      );
    } catch {
      // The escaped, unhighlighted source remains readable.
    }
  },
  { immediate: true, flush: 'post' },
);
onBeforeUnmount(clearFeedback);
function setCopied(button: HTMLButtonElement, copied: boolean) {
  const label = button.querySelector('span');
  const icon = button.querySelector<SVGElement>('.copy-icon');
  const check = button.querySelector<SVGElement>('.copied-icon');
  if (label) label.textContent = copied ? 'Copied' : 'Copy';
  if (icon) icon.toggleAttribute('hidden', copied);
  if (check) check.toggleAttribute('hidden', !copied);
}
async function copy(event: MouseEvent) {
  const button = (event.target as Element).closest<HTMLButtonElement>('button[data-copy-code]');
  if (!button || !event.currentTarget || !(event.currentTarget as Element).contains(button)) return;
  const code = rendered.value.codes[Number(button.dataset.copyCode)];
  if (code === undefined) return;
  const current = revision;
  try {
    await navigator.clipboard.writeText(code);
    if (current !== revision) return;
    notice.value = '';
    setCopied(button, true);
    clearTimeout(timers.get(button));
    timers.set(
      button,
      setTimeout(() => {
        setCopied(button, false);
        timers.delete(button);
      }, 1500),
    );
  } catch {
    if (current === revision) notice.value = 'Could not copy. Select the code to copy it.';
  }
}
</script>
<template>
  <div class="recorded-markdown">
    <!-- The renderer escapes recorded text and disables raw HTML and active images. -->
    <!-- eslint-disable-next-line vue/no-v-html -->
    <div ref="body" class="markdown-body" @click="copy" v-html="rendered.html" />
    <span class="copy-notice" role="status">{{ notice }}</span>
  </div>
</template>
<style>
.recorded-markdown {
  min-width: 0;
  max-width: 100%;
}
.markdown-body {
  line-height: 1.7;
  overflow-wrap: anywhere;
  min-width: 0;
}
.markdown-body > :first-child {
  margin-top: 0;
}
.markdown-body > :last-child {
  margin-bottom: 0;
}
.markdown-body p {
  margin: 0 0 12px;
}
.markdown-body h1,
.markdown-body h2 {
  font-size: 17px;
  line-height: 1.4;
  margin: 20px 0 10px;
  font-weight: 600;
}
.markdown-body h3,
.markdown-body h4,
.markdown-body h5,
.markdown-body h6 {
  font-size: 14px;
  line-height: 1.5;
  margin: 16px 0 8px;
  font-weight: 600;
}
.markdown-body ul,
.markdown-body ol {
  padding-left: 24px;
  margin: 8px 0 14px;
}
.markdown-body li + li {
  margin-top: 4px;
}
.markdown-body blockquote {
  margin: 12px 0;
  padding-left: 14px;
  border-left: 3px solid var(--control-border);
  color: var(--muted);
}
.markdown-body a {
  color: var(--primary-text);
  text-decoration: underline;
  text-underline-offset: 3px;
}
.markdown-body code {
  font:
    12.5px/1.6 ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
  background: var(--input);
  padding: 1px 4px;
  border-radius: 3px;
}
.markdown-code {
  margin: 14px 0;
  border: 1px solid var(--border);
  border-radius: 7px;
  min-width: 0;
  overflow: hidden;
  background: var(--input);
}
.markdown-code .code-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 12px;
  color: var(--muted);
  font-size: 11.5px;
  border-bottom: 1px solid var(--border);
}
.markdown-code .markdown-copy {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--muted);
  font: inherit;
  font-size: 11.5px;
  line-height: 1.45;
  cursor: pointer;
}
.markdown-code .markdown-copy:hover {
  color: var(--text);
  background: var(--hover);
}
.markdown-copy svg[hidden] {
  display: none;
}
.markdown-code pre {
  margin: 0;
  padding: 12px;
  overflow-x: auto;
  white-space: pre;
  max-width: 100%;
}
.markdown-code pre code {
  padding: 0;
  background: none;
}
.markdown-code pre.syntax-highlighted {
  background: #24292e;
  color: #e1e4e8;
}
.markdown-table {
  max-width: 100%;
  overflow-x: auto;
  margin: 12px 0;
  border: 1px solid var(--border);
  border-radius: 6px;
}
.markdown-table table {
  border-collapse: collapse;
  min-width: 100%;
  font-size: 12.5px;
}
.markdown-table th,
.markdown-table td {
  padding: 7px 11px;
  border-bottom: 1px solid var(--border);
  text-align: left;
  min-width: 80px;
}
.markdown-table th {
  font-weight: 600;
  background: var(--input);
}
.markdown-table tr:last-child td {
  border-bottom: 0;
}
.markdown-image {
  color: var(--muted);
}
.copy-notice {
  font-size: 11px;
  color: var(--muted);
}
.copy-notice:empty {
  display: none;
}
.markdown-body button:focus-visible,
.markdown-body [tabindex]:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: -2px;
}
</style>
