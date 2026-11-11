<script lang="ts">
// Coresearch and Decisions sit in the toggle at the top of every rail.
export const brandSwitches = [
  { id: 'coresearch', name: 'Coresearch', icon: 'chat' },
  { id: 'decisions', name: 'Decisions', icon: 'bell' },
];
</script>
<script setup lang="ts">
import { computed } from 'vue';
import Icon from './Icon.vue';
import LykeionWordmark from './LykeionWordmark.vue';
import { useDecisionCount } from '../api/queries';

defineProps<{ current?: string }>();
// Clicking the selected item again swaps that rail and the main navigation.
const emit = defineEmits<{ reselect: [] }>();
const count = useDecisionCount();
const waiting = computed(() => (count.data.value?.open ?? 0) > 0);
</script>
<template>
  <div class="sidebar-brand">
    <RouterLink to="/studies" class="brand-home" title="Go to Studies" aria-label="Go to Studies"
      ><LykeionWordmark
    /></RouterLink>
    <div class="brand-switch">
      <!-- The selected item stays on the open conversation or decision. -->
      <RouterLink
        v-for="item in brandSwitches"
        :key="item.id"
        v-slot="{ href, navigate }"
        :to="'/' + item.id"
        custom
        ><a
          :href="href"
          class="brand-switch-item"
          :class="{ selected: current === item.id }"
          :aria-label="item.name"
          :title="item.name"
          :aria-current="current === item.id ? 'page' : undefined"
          @click="
            item.id === current ? ($event.preventDefault(), emit('reselect')) : navigate($event)
          "
          ><Icon :name="item.icon" :size="16" /><span
            v-if="item.id === 'decisions' && waiting"
            class="brand-switch-dot"
            role="img"
            aria-label="Open decisions" /></a
      ></RouterLink>
    </div>
  </div>
</template>
<style scoped>
.sidebar-brand {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  height: 32px;
  /* Room for the sidebar toggle, which App keeps outside the rails. */
  padding: 0 0 0 32px;
  margin-bottom: 12px;
  flex: none;
}
.brand-home {
  display: flex;
  min-width: 0;
  border-radius: 6px;
}
.brand-switch {
  display: flex;
  gap: 5px;
  padding: 2px;
  border-radius: 8px;
  background: var(--hover);
}
.brand-switch-item {
  position: relative;
  display: grid;
  place-items: center;
  width: 28px;
  height: 24px;
  border-radius: 6px;
  color: var(--muted);
}
.brand-switch-item + .brand-switch-item::before {
  content: '';
  position: absolute;
  left: -3px;
  top: 5px;
  bottom: 5px;
  width: 1px;
  background: var(--border);
}
.brand-switch-item:hover {
  color: var(--text);
}
.brand-switch-item.selected {
  background: var(--surface);
  color: var(--text);
  box-shadow: inset 0 0 0 1px var(--border);
}
.brand-switch-dot {
  position: absolute;
  top: 3px;
  right: 5px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--primary);
}
</style>
