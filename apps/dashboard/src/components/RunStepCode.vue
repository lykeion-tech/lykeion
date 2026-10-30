<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import Icon from './Icon.vue';
const props = defineProps<{
  code: string;
  language: string;
  notebook?: boolean;
  executionCount?: number;
}>();
type Token = { content: string; color?: string };
const tokens = ref<Token[][]>();
const lines = computed<Token[][]>(
  () => tokens.value ?? props.code.split('\n').map((content) => [{ content }]),
);
const copied = ref(false);
const copyError = ref('');
let revision = 0;
let copyTimer: ReturnType<typeof setTimeout> | undefined;
onUnmounted(() => {
  revision++;
  clearTimeout(copyTimer);
});
async function highlight() {
  const current = ++revision;
  tokens.value = undefined;
  try {
    const { highlightToolCode } = await import('../lib/tool-code');
    const result = await highlightToolCode(props.code, props.language);
    if (current === revision) tokens.value = result;
  } catch {
    // Recorded code remains readable when highlighting is unavailable.
  }
}
onMounted(() => {
  void highlight();
});
watch(
  () => [props.code, props.language],
  () => {
    void highlight();
    copied.value = false;
  },
);
async function copy() {
  try {
    await navigator.clipboard.writeText(props.code);
    copied.value = true;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied.value = false;
    }, 1500);
    copyError.value = '';
  } catch {
    copyError.value = 'Could not copy. Select the code to copy it.';
  }
}
</script>
<template>
  <div class="step-code" :class="{ 'notebook-cell': notebook }">
    <div class="step-code-head">
      <div class="cell-meta">
        <span v-if="notebook" class="cell-count">In [{{ executionCount ?? ' ' }}]</span
        ><span class="cell-language">{{ language }}</span>
      </div>
      <button type="button" class="step-copy" aria-label="Copy tool code" @click="copy">
        <Icon :name="copied ? 'check' : 'copy'" :size="12" />{{ copied ? 'Copied' : 'Copy' }}
      </button>
    </div>
    <pre
      class="step-code-source"
      :class="{ highlighted: tokens }"
      tabindex="0"
    ><code><template v-for="(line, i) in lines" :key="i"><span class="code-line"><span v-if="notebook" class="line-number" aria-hidden="true">{{ i + 1 }}</span><span v-for="(token, j) in line" :key="j" :style="{ color: token.color }">{{ token.content }}</span></span>{{ i < lines.length - 1 ? '\n' : '' }}</template></code></pre>
    <span v-if="copyError" role="status" class="copy-error">{{ copyError }}</span>
  </div>
</template>
<style scoped>
.step-code {
  min-width: 0;
  border: 1px solid var(--step-border, var(--border));
  border-radius: 8px;
  overflow: hidden;
  background: var(--step-surface, var(--input));
}
.step-code-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--step-border, var(--border));
}
.notebook-cell .step-code-head {
  padding: 4px 8px;
}
.cell-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--icon-faint);
  font:
    12px/1.4 'JetBrains Mono Variable',
    monospace;
}
.cell-language {
  font:
    500 12px/1.4 'Inter Variable',
    sans-serif;
}
.notebook-cell .cell-language {
  padding: 1px 5px;
  background: var(--fill-active);
  border-radius: 4px;
}
.step-copy {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--muted);
  font:
    13px/1.45 'Inter Variable',
    sans-serif;
  cursor: pointer;
}
.step-copy:hover {
  background: var(--hover);
  color: var(--text);
}
.step-code-source {
  margin: 0;
  padding: 8px 10px;
  overflow: auto;
  background: transparent;
  color: var(--step-muted, var(--muted));
  font:
    12px/1.5 'JetBrains Mono Variable',
    ui-monospace,
    monospace;
  tab-size: 4;
}
.step-code-source code {
  display: block;
  width: max-content;
  min-width: 100%;
}
.line-number {
  display: inline-block;
  width: 2.25em;
  margin-right: 0.85em;
  text-align: right;
  color: #959da5;
  opacity: 0.65;
  user-select: none;
}
.copy-error {
  display: block;
  padding: 6px 10px;
  color: var(--error-text);
}
button:focus-visible,
pre:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: -2px;
}

.step-code-source.highlighted {
  background: #24292e;
  color: #e1e4e8;
}
</style>
