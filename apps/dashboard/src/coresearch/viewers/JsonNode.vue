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
  <li v-if="!object" role="treeitem" :aria-label="name" class="json-leaf" @click.stop>
    <span class="json-key">{{ name }}</span
    >:
    <span class="json-value">{{
      typeof value === 'string' ? JSON.stringify(value) : String(value)
    }}</span>
  </li>
  <li
    v-else
    role="treeitem"
    :aria-label="`${name} ${summary}`"
    :aria-expanded="expanded"
    tabindex="0"
    @click.stop="emit('toggle', path)"
    @keydown.enter.stop.prevent="emit('toggle', path)"
    @keydown.space.stop.prevent="emit('toggle', path)"
  >
    <span class="json-key">{{ name }}</span> <span class="viz-meta">{{ summary }}</span>
    <ul v-if="expanded" role="group">
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
