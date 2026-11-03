<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import QuestionSetOptions from './QuestionSetOptions.vue';
import { keys } from '../../api/keys';
import { useMembers } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import { decisionKindLabel, decisionKindOf, decisionOutcome, decisionRaiser, timeAgo } from '../../lib/studies';

const props = defineProps<{ item: DecisionItem }>();
const client = useQueryClient();
const busy = ref(false);
const error = ref('');
const guidance = ref('');
const selected = ref('');
const options = computed(() => props.item.options ?? []);
const members = useMembers();
const decider = computed(() => {
  const id = props.item.decided_by;
  if (!id) return '';
  const member = members.data.value?.find((m) => m.user_id === id);
  return member ? member.name || member.email : '';
});
const answerable = computed(() => props.item.can_answer && props.item.status === 'open' && options.value.length > 0);
const systemRedo = computed(() => props.item.kind !== 'agent_decision' && selected.value === 'redo');
const canSubmit = computed(() => answerable.value && !!selected.value && !busy.value && (!systemRedo.value || !!guidance.value.trim()));
const expires = computed(() => {
  const at = props.item.expires_at;
  if (!at) return '';
  const ms = Date.parse(at) - Date.now();
  if (ms <= 0) return 'Expires now';
  const hours = Math.ceil(ms / 3_600_000);
  return `Expires in ${hours < 48 ? `${hours}h` : `${Math.ceil(hours / 24)}d`}`;
});
function choose(option: string) {
  if (!answerable.value || busy.value) return;
  selected.value = option;
  error.value = '';
}
async function submit() {
  if (!canSubmit.value) return;
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/decisions/${props.item.id}/decide`, {
      option: selected.value,
      ...(systemRedo.value ? { guidance: guidance.value.trim() } : {}),
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
</script>
<template>
  <div class="decision-options type-body">
    <p class="decision-meta type-caption">
      {{ decisionRaiser(item) }} · {{ decisionKindLabel[item.decision_kind] ?? decisionKindOf(item.kind).label }}
      <template v-if="expires && item.status === 'open'"> · {{ expires }}</template>
    </p>
    <template v-if="item.question_set?.questions?.length">
      <QuestionSetOptions :item="item" />
      <p v-if="item.status !== 'open'" class="decision-outcome">
        {{ item.status === 'decided' ? 'Answered' : decisionOutcome(item) }}<template v-if="decider"> · by {{ decider }}</template><template v-if="item.decided_at"> · {{ timeAgo(item.decided_at) }}</template>
      </p>
    </template>
    <template v-else-if="item.status === 'open'">
      <p v-if="!item.can_answer" class="permission-note">Only the study owner or a workspace owner or admin can answer this decision.</p>
      <p v-if="item.default_option && expires" class="default-note">If no one answers, the default is {{ options.find((o) => o.id === item.default_option)?.label ?? item.default_option }}.</p>
      <div class="decision-buttons" role="group" aria-label="Decision options">
        <button v-for="option in options" :key="option.id" type="button" class="choice"
          :class="{ selected: selected === option.id }" :disabled="!answerable || busy"
          :aria-pressed="selected === option.id" @click="choose(option.id)">
          <b>{{ option.label }}</b><small>{{ option.consequence }}</small>
        </button>
      </div>
      <textarea v-if="answerable && systemRedo" class="decision-guidance" aria-label="Guidance"
        placeholder="Guidance for the next round (required)" :value="guidance"
        @input="guidance = ($event.target as HTMLTextAreaElement).value" />
      <button v-if="answerable" type="button" class="button primary decision-submit" :disabled="!canSubmit" @click="submit">{{ busy ? 'Submitting…' : 'Submit' }}</button>
    </template>
    <p v-else class="decision-outcome">
      {{ decisionOutcome(item) }}<template v-if="decider"> · by {{ decider }}</template><template v-if="item.decided_at"> · {{ timeAgo(item.decided_at) }}</template><template v-if="item.guidance"> · “{{ item.guidance }}”</template>
    </p>
    <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
  </div>
</template>
<style scoped>
.decision-options { display:flex; flex-direction:column; gap:12px; }
.decision-meta, .permission-note, .default-note { color:var(--muted); }
.permission-note, .default-note { font-size:12px; line-height:1.5; }
.decision-buttons { display:flex; flex-direction:column; gap:8px; }
.choice { display:flex; flex-direction:column; align-items:flex-start; gap:5px; width:100%; padding:13px 16px; border:1px solid var(--border); border-radius:6px; background:var(--surface); color:var(--text); text-align:left; cursor:pointer; }
.choice small { color:var(--muted); font-size:12px; line-height:1.5; font-weight:400; }
.choice:hover:not(:disabled) { border-color:var(--primary); }
.choice.selected { border-color:var(--primary); background:color-mix(in srgb, var(--primary) 14%, var(--surface)); }
.choice:focus-visible, .decision-submit:focus-visible { outline:2px solid var(--primary); outline-offset:2px; }
.choice:disabled { cursor:default; opacity:.75; }
.decision-guidance { min-height:88px; padding:10px; border:1px solid var(--border); border-radius:6px; background:var(--surface); color:inherit; font:inherit; }
.decision-submit { align-self:flex-start; }
</style>
