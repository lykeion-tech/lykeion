<script setup lang="ts">
import { computed } from 'vue';
import { useDecisionCount } from '../../api/queries';

const count = useDecisionCount();
const open = computed(() => count.data.value?.open ?? 0);
// Both question expiries and manual note reminders contribute to this count.
const dueSoon = computed(() => count.data.value?.due_soon ?? 0);
</script>
<template>
  <span
    v-if="open"
    class="decision-badge type-caption"
    :class="{ due: dueSoon > 0 }"
    role="status"
    :aria-label="dueSoon > 0 ? `Open decisions, ${dueSoon} due soon` : 'Open decisions'"
    >{{ open }}</span
  >
</template>
<style scoped>
.decision-badge {
  margin-left: auto;
  min-width: 15px;
  padding: 0 4px;
  border-radius: 7px;
  background: color-mix(in srgb, var(--primary) 20%, transparent);
  color: var(--primary-text);
  font-size: 10px;
  text-align: center;
}
.decision-badge.due {
  background: var(--danger-bg);
  color: var(--danger-text);
}
</style>
