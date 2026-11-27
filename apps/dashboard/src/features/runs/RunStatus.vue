<script setup lang="ts">
defineProps<{ text: string; working?: boolean }>();
</script>

<template>
  <div class="run-status">
    <span v-if="working" class="run-status-spinner" aria-hidden="true">
      <span class="run-status-ring run-status-ring-right" />
      <span class="run-status-ring run-status-ring-left" />
    </span>
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
  position: relative;
  flex: none;
  width: 16px;
  height: 16px;
}
.run-status-ring {
  position: absolute;
  inset: 0;
  border: 1.6px solid var(--primary-text);
  border-radius: 50%;
  opacity: 0.4;
  animation: 2s linear infinite;
}
.run-status-ring-right {
  animation-name: run-status-ring-right;
}
.run-status-ring-left {
  animation-name: run-status-ring-left;
}
@keyframes run-status-ring-right {
  to {
    transform: rotateX(180deg) rotateY(360deg) rotate(360deg);
  }
}
@keyframes run-status-ring-left {
  to {
    transform: rotateX(360deg) rotateY(180deg) rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .run-status-ring {
    animation: none;
  }
}
</style>
