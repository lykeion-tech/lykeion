<script setup lang="ts">
import type { SessionMode } from '../types';
defineProps<{ modelValue: SessionMode }>();
const emit = defineEmits<{ 'update:modelValue': [SessionMode] }>();
</script>
<template>
  <div class="composer-mode" role="group" aria-label="Mode">
    <button
      v-for="mode in ['experiment', 'cowrite'] as const"
      :key="mode"
      type="button"
      class="composer-mode-tab"
      :class="{ 'is-on': modelValue === mode }"
      :aria-pressed="modelValue === mode"
      @click="emit('update:modelValue', mode)"
    >
      {{ mode === 'experiment' ? 'Experiment' : 'Cowrite' }}
    </button>
  </div>
</template>
