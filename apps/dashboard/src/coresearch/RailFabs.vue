<script setup lang="ts">
// Ported from Lykeion-L2 packages/ui/src/shell/RailFabs.tsx; see L2-LICENSE.
import Icon from '../ui/Icon.vue';
// `session`: the rail beside a Cowrite session, which L2 marks with its flask.
defineProps<{ view: 'nav' | 'context'; session?: boolean; inbox?: boolean }>();
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
      <Icon name="sidebar" :size="16" /></button
    ><button
      class="rail-fab"
      :class="{ 'is-active': view === 'context' }"
      :aria-label="inbox ? 'Show inbox' : session ? 'Show session details' : 'Show conversations'"
      :aria-pressed="view === 'context'"
      @click="emit('change', 'context')"
    >
      <Icon :name="inbox ? 'inbox' : session ? 'flask' : 'chat'" :size="16" />
    </button>
  </div>
</template>
<style scoped>
.rail-fabs {
  position: fixed;
  left: 16px;
  bottom: 68px;
  z-index: 30;
  display: flex;
  gap: 8px;
}
.rail-fab {
  width: 32px;
  height: 32px;
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

<style scoped>
@media (max-width: 700px) {
  .rail-fabs {
    left: 0;
    bottom: 0;
    width: 100%;
    height: var(--aristotle-mobile-dock-height, 64px);
    box-sizing: border-box;
    align-items: center;
    padding: 12px 8px;
    background: var(--canvas);
  }
}
</style>
