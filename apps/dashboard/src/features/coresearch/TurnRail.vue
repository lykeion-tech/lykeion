<script setup lang="ts">
defineProps<{ nested?: boolean }>();
</script>
<template>
  <div
    class="turn-rail"
    :class="{ 'turn-rail--nested': nested }"
    :data-testid="nested ? 'turn-rail-nested' : 'turn-rail'"
  >
    <slot />
  </div>
</template>
