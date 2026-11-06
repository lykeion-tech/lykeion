<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { jsonPathsMatching } from '../json-tree';
import JsonNode from './JsonNode.vue';
import CodeBlock from '../CodeBlock.vue';
const props = defineProps<{ text: string }>();
const query = ref(''),
  raw = ref(false),
  open = ref(new Set(['$'])),
  suppressed = ref(new Set<string>());
const parsed = computed(() => {
  try {
    return { value: JSON.parse(props.text) as unknown };
  } catch (e) {
    return { error: String(e) };
  }
});
const matches = computed(() => jsonPathsMatching(parsed.value.value, query.value));
watch(query, () => (suppressed.value = new Set()));
function toggle(path: string) {
  const visible = open.value.has(path) || (matches.value.has(path) && !suppressed.value.has(path));
  const next = new Set(open.value),
    hidden = new Set(suppressed.value);
  if (visible) {
    next.delete(path);
    if (matches.value.has(path)) hidden.add(path);
  } else {
    next.add(path);
    hidden.delete(path);
  }
  open.value = next;
  suppressed.value = hidden;
}
</script>
<template>
  <div class="json-viewer">
    <p v-if="parsed.error" class="viewer-error">This file is not valid JSON: {{ parsed.error }}</p>
    <div v-else class="json-toolbar">
      <input
        v-model="query"
        class="json-search"
        type="search"
        placeholder="Search keys and values"
        aria-label="Search keys and values"
      /><button type="button" class="quiet-action" :aria-pressed="raw" @click="raw = !raw">
        Raw
      </button>
    </div>
    <CodeBlock v-if="raw || parsed.error" :code="text" lang="json" />
    <ul v-else class="json-tree">
      <JsonNode
        name="$"
        :value="parsed.value"
        path="$"
        :open="open"
        :suppressed="suppressed"
        :matches="matches"
        @toggle="toggle"
      />
    </ul>
  </div>
</template>
<style scoped>
.json-viewer {
  min-width: 0;
  color: var(--text);
}
.json-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.json-viewer .json-search {
  flex: 1;
  min-width: 0;
  padding: 6px 2px;
  border: 0;
  outline: none;
  box-shadow: none;
  background: transparent;
  color: var(--text);
  font: inherit;
}
.json-viewer .json-search:focus-visible {
  outline: 2px solid var(--primary-text, #2563a6);
  outline-offset: 2px;
  border-radius: 4px;
}
.json-toolbar .quiet-action {
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: transparent;
  color: var(--text);
  font: inherit;
  cursor: pointer;
}
.json-toolbar .quiet-action[aria-pressed='true'] {
  background: var(--input);
}
.json-toolbar .quiet-action:focus-visible {
  outline: 2px solid var(--primary-text, #2563a6);
  outline-offset: 2px;
}
.json-tree {
  list-style: none;
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 13px;
  line-height: 1.5;
}
</style>
