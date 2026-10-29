<script setup lang="ts">
import { computed } from 'vue';
import type { OutlineEntry } from './lib/manuscript';
const props = defineProps<{ entries: OutlineEntry[]; at?: number }>();
const emit = defineEmits<{ jump: [number] }>();
const current = computed(() =>
  props.at === undefined
    ? undefined
    : [...props.entries].reverse().find((e) => e.line <= props.at!),
);
</script>
<template>
  <div v-if="entries.length" class="ms-outline">
    <div class="mt-2 px-2.5 pb-[3px] pt-1.5 type-label text-fg-tertiary">Outline</div>
    <button
      v-for="entry in entries"
      :key="`${entry.line}:${entry.title}`"
      type="button"
      class="ms-outline-row"
      :class="{ 'is-current': entry === current }"
      :aria-current="entry === current ? 'true' : undefined"
      :data-level="entry.level"
      @click="emit('jump', entry.line)"
    >
      {{ entry.title }}
    </button>
  </div>
</template>
