<script setup lang="ts">
import { computed } from 'vue';
import { childPath } from '../json-tree';
const props = defineProps<{
  name: string;
  value: unknown;
  path: string;
  open: Set<string>;
  suppressed: Set<string>;
  matches: Set<string>;
}>();
const emit = defineEmits<{ toggle: [path: string] }>();
const object = computed(() => props.value !== null && typeof props.value === 'object');
const entries = computed<[string | number, unknown][]>(() =>
  Array.isArray(props.value)
    ? props.value.map((v, i) => [i, v])
    : Object.entries((props.value ?? {}) as Record<string, unknown>),
);
const expanded = computed(
  () =>
    props.open.has(props.path) ||
    (props.matches.has(props.path) && !props.suppressed.has(props.path)),
);
const summary = computed(() =>
  Array.isArray(props.value) ? `[${entries.value.length}]` : `{${entries.value.length}}`,
);
</script>
<template>
  <li v-if="!object" class="json-leaf">
    <span class="json-key">{{ name }}</span
    >:
    <span class="json-value">{{
      typeof value === 'string' ? JSON.stringify(value) : String(value)
    }}</span>
  </li>
  <li v-else class="json-branch">
    <button
      type="button"
      class="json-toggle"
      :aria-label="`${name} ${summary}`"
      :aria-expanded="expanded"
      @click="emit('toggle', path)"
    >
      <span class="json-chevron" aria-hidden="true">{{ expanded ? '▾' : '▸' }}</span>
      <span class="json-key">{{ name }}</span> <span class="json-summary">{{ summary }}</span>
    </button>
    <ul v-if="expanded" class="json-children">
      <JsonNode
        v-for="[k, v] in entries"
        :key="String(k)"
        :name="String(k)"
        :value="v"
        :path="childPath(path, k)"
        :open="open"
        :suppressed="suppressed"
        :matches="matches"
        @toggle="emit('toggle', $event)"
      />
    </ul>
  </li>
</template>
<style scoped>
.json-branch,
.json-leaf {
  list-style: none;
}
.json-toggle {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  padding: 3px 4px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.json-toggle:focus-visible {
  outline: 2px solid var(--primary-text, #2563a6);
  outline-offset: 2px;
}
.json-chevron,
.json-summary {
  color: var(--text-muted, var(--text));
}
.json-key {
  font-weight: 600;
}
.json-leaf {
  padding: 3px 4px 3px 20px;
  overflow-wrap: anywhere;
}
.json-value {
  color: var(--text);
}
.json-branch > .json-children {
  list-style: none;
  margin: 0 0 0 9px;
  padding-left: 15px;
  border-left: 1px solid var(--border);
}
</style>
