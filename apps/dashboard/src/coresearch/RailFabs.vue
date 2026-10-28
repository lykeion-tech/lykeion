<script setup lang="ts">
// Ported from Lykeion-L2 packages/ui/src/shell/RailFabs.tsx; see L2-LICENSE.
import Icon from '../components/Icon.vue';
defineProps<{ view: 'nav' | 'context' }>();
const emit = defineEmits<{ change: ['nav' | 'context'] }>();
</script>
<template>
  <div class="rail-fabs" data-testid="rail-fabs">
    <button
      class="rail-fab"
      :class="{ 'is-active': view === 'nav' }"
      aria-label="Show navigation"
      :aria-pressed="view === 'nav'"
      @click="emit('change', 'nav')"
    >
      <Icon name="sidebar" :size="20" /></button
    ><button
      class="rail-fab"
      :class="{ 'is-active': view === 'context' }"
      aria-label="Show conversations"
      :aria-pressed="view === 'context'"
      @click="emit('change', 'context')"
    >
      <Icon name="chat" :size="20" />
    </button>
  </div>
</template>
<style scoped>
.rail-fabs {
  position: fixed;
  left: 16px;
  bottom: 46px;
  z-index: 30;
  display: flex;
  gap: 8px;
}
.rail-fab {
  width: 40px;
  height: 40px;
  border-radius: 999px;
  display: grid;
  place-items: center;
  background: var(--surface);
  border: 1px solid var(--border);
  color: var(--muted);
  box-shadow: 0 0 0 1px var(--border);
}
.rail-fab:hover {
  color: var(--text);
}
.rail-fab.is-active {
  background: var(--text);
  border-color: transparent;
  color: var(--surface);
}
@media (prefers-reduced-motion: no-preference) {
  .rail-fab {
    transition:
      background-color 120ms ease,
      color 120ms ease,
      border-color 120ms ease;
  }
}
</style>
