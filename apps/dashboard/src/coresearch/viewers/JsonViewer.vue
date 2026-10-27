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
    <div v-else class="viz-caption">
      <input
        v-model="query"
        type="search"
        placeholder="Search keys and values"
        aria-label="Search keys and values"
      /><button type="button" class="quiet-action" :aria-pressed="raw" @click="raw = !raw">
        Raw
      </button>
    </div>
    <CodeBlock v-if="raw || parsed.error" :code="text" lang="json" />
    <ul v-else role="tree" class="json-tree">
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
