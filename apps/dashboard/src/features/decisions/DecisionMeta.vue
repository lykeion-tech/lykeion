<script setup lang="ts">
import { computed } from 'vue';
import type { DecisionItem } from '../../api/types';
import { decisionKindLabel, decisionKindOf, decisionRaiser } from '../../lib/studies';

const props = defineProps<{ item: DecisionItem }>();
const expires = computed(() => {
  if (!props.item.expires_at) return '';
  const ms = Date.parse(props.item.expires_at) - Date.now();
  if (ms <= 0) return 'Expires now';
  const hours = Math.ceil(ms / 3_600_000);
  return `Expires in ${hours < 48 ? `${hours}h` : `${Math.ceil(hours / 24)}d`}`;
});
const defaultLabel = computed(() =>
  props.item.options?.find((option) => option.id === props.item.default_option)?.label ?? props.item.default_option,
);
const kindLabel = computed(() => decisionKindLabel[props.item.decision_kind] ?? decisionKindOf(props.item.kind).label);
const parts = computed(() => [decisionRaiser(props.item), kindLabel.value].filter((part) => part !== 'Lykeion' && part !== 'Needs a decision'));
</script>
<template>
  <p class="decision-meta type-caption">
    <template v-if="parts.length">{{ parts.join(' · ') }}</template>
    <template v-if="expires && item.status === 'open'">{{ parts.length ? ' · ' : '' }}{{ expires }}</template>
    <template v-if="item.status === 'open' && !item.question_set?.questions?.length && item.default_option && expires"> · If no one answers, the default is {{ defaultLabel }}.</template>
  </p>
</template>
<style scoped>
.decision-meta { color: var(--muted); }
</style>
