<script setup lang="ts">
import { computed } from 'vue';
import { useDecisionCount } from '../../api/queries';

const count = useDecisionCount();
const open = computed(() => count.data.value?.open ?? 0);
// Decisions whose reminder went out expire within a day.
const dueSoon = computed(() => count.data.value?.due_soon ?? 0);
</script>
<template>
  <span
    v-if="open"
    class="decision-badge type-caption"
    :class="{ due: dueSoon > 0 }"
    role="status"
    :aria-label="dueSoon > 0 ? `Open decisions, ${dueSoon} expiring soon` : 'Open decisions'"
    >{{ open }}</span
  >
</template>
<style scoped>
.decision-badge {
  margin-left: auto;
  min-width: 18px;
  padding: 0 6px;
  border-radius: 9px;
  background: var(--primary);
  color: var(--on-primary);
  text-align: center;
}
.decision-badge.due {
  background: var(--danger-bg);
  color: var(--danger-text);
}
</style>
