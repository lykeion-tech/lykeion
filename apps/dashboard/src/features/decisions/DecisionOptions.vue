<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { useMembers } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import {
  decisionKindLabel,
  decisionKindOf,
  decisionOutcome,
  decisionRaiser,
  timeAgo,
} from '../../lib/studies';

// Every way to answer a decision, who raised it, when it lapses and, once
// closed, what was chosen.
const props = defineProps<{ item: DecisionItem }>();
const client = useQueryClient();
const busy = ref(false);
const error = ref('');
const guidance = ref('');
const redoing = ref(false);
const options = computed(() => props.item.options ?? []);
const members = useMembers();
// Who answered a decided item, by name, or by email when they set none.
const decider = computed(() => {
  const id = props.item.decided_by;
  if (!id) return '';
  const m = members.data.value?.find((x) => x.user_id === id);
  return m ? m.name || m.email : '';
});
const answerable = computed(
  () => props.item.can_answer && props.item.status === 'open' && options.value.length > 0,
);
const guidanceMissing = computed(() => !guidance.value.trim());
const expires = computed(() => {
  const at = props.item.expires_at;
  if (!at) return '';
  const ms = Date.parse(at) - Date.now();
  if (ms <= 0) return 'Expires now';
  const hours = Math.ceil(ms / 3_600_000);
  return `Expires in ${hours < 48 ? `${hours}h` : `${Math.ceil(hours / 24)}d`}`;
});

async function decide(option: string) {
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/decisions/${props.item.id}/decide`, {
      option,
      ...(option === 'redo' ? { guidance: guidance.value.trim() } : {}),
    });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.decisionsAll(props.item.workspace_id) }),
      client.invalidateQueries({ queryKey: keys.decisionCount(props.item.workspace_id) }),
    ]);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function choose(option: string) {
  if (option !== 'redo') void decide(option);
  else if (!redoing.value) redoing.value = true;
  else if (!guidanceMissing.value) void decide(option);
}
</script>
<template>
  <div class="decision-options type-body">
    <p class="decision-meta type-caption">
      {{ decisionRaiser(item) }} ·
      {{ decisionKindLabel[item.decision_kind] ?? decisionKindOf(item.kind).label
      }}<template v-if="expires && item.status === 'open'"> · {{ expires }}</template>
    </p>
    <template v-if="answerable">
      <div class="decision-buttons">
        <button
          v-for="option in options"
          :key="option.id"
          class="button"
          :disabled="busy || (option.id === 'redo' && redoing && guidanceMissing)"
          @click="choose(option.id)"
        >
          <b>{{ option.label }}</b
          ><small class="type-caption">{{ option.consequence }}</small>
        </button>
      </div>
      <textarea
        v-if="redoing"
        class="decision-guidance"
        aria-label="Guidance"
        placeholder="Guidance for the next round (required)"
        :value="guidance"
        @input="guidance = ($event.target as HTMLTextAreaElement).value"
      />
    </template>
    <p v-else-if="item.status !== 'open'" class="decision-outcome">
      {{ decisionOutcome(item) }}<template v-if="decider"> · by {{ decider }}</template
      ><template v-if="item.decided_at"> · {{ timeAgo(item.decided_at) }}</template
      ><template v-if="item.guidance"> · “{{ item.guidance }}”</template>
    </p>
    <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
  </div>
</template>
<style scoped>
.decision-options {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
}
.decision-meta {
  color: var(--muted);
}
.decision-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.decision-buttons .button {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  height: auto;
  padding: 8px 12px;
  text-align: left;
}
.decision-buttons small {
  color: var(--muted);
  font-weight: 400;
}
.decision-guidance {
  min-height: 72px;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font: inherit;
}
</style>
