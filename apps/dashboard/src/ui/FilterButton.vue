<script setup lang="ts">
import Icon from './Icon.vue';

defineProps<{ expanded: boolean; count?: number }>();
</script>

<template>
  <button
    type="button"
    class="button compact"
    :class="{ active: count }"
    aria-label="Filter"
    title="Filter"
    :aria-expanded="expanded"
    aria-haspopup="menu"
  >
    <Icon name="filter" :size="14" /><span v-if="count">{{ count }}</span>
  </button>
</template>
