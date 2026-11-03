<script setup lang="ts">
import { useDecisionCount } from '../../api/queries';

const count = useDecisionCount();
</script>
<template>
  <span
    v-if="count.data.value"
    class="decision-badge type-caption"
    role="status"
    aria-label="Open decisions"
    >{{ count.data.value }}</span
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
</style>
