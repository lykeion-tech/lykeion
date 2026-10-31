<script setup lang="ts">
import { RingLoader } from 'vue-spinner';
import 'vue-spinner/style.css';

defineProps<{ text: string; working?: boolean }>();
</script>

<template>
  <div class="run-status">
    <RingLoader
      v-if="working"
      class="run-status-spinner"
      size="16px"
      color="var(--primary-text)"
      aria-hidden="true"
    />
    <span>{{ text }}</span>
  </div>
</template>

<style scoped>
.run-status {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.run-status-spinner {
  flex: none;
  width: 16px;
  height: 16px;
}
@media (prefers-reduced-motion: reduce) {
  .run-status-spinner :deep(.v-ring) {
    animation: none !important;
  }
}
</style>
