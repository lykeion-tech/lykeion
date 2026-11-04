<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useMe } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import ManualDecisionForm from './ManualDecisionForm.vue';
import { manualDeadlineLabel, type ManualDecisionDraft } from './manualDecisionDraft';
const props = defineProps<{ item: DecisionItem }>();
const me = useMe(),
  client = useQueryClient();
const owner = computed(
  () => props.item.workspace_id === workspaceId.value && props.item.user_id === me.data.value?.id,
);
const editing = ref(false),
  busy = ref(false),
  error = ref(''),
  snooze = ref('');
const form = ref<ManualDecisionDraft>({ title: '', context: '', deadline: null, card_id: null });
const deadline = computed(() =>
  manualDeadlineLabel(props.item.deadline, me.data.value?.timezone ?? 'UTC'),
);
let generation = 0;
watch(
  [() => props.item.id, workspaceId, () => me.data.value?.id],
  () => {
    generation++;
    editing.value = false;
    error.value = '';
    busy.value = false;
    snooze.value = '';
  },
  { flush: 'sync' },
);
onUnmounted(() => generation++);
function edit() {
  form.value = {
    title: props.item.title ?? '',
    context: props.item.context ?? '',
    deadline: props.item.deadline ?? null,
    card_id: props.item.card_id,
  };
  editing.value = true;
}
async function act(action: string, method = 'POST', body?: unknown) {
  if (!owner.value || busy.value) return;
  const visit = generation,
    item = props.item;
  busy.value = true;
  error.value = '';
  try {
    await api(method, `/api/decisions/${item.id}/${action}`, body, undefined, {
      workspace: item.workspace_id,
    });
    if (visit !== generation) return;
    editing.value = false;
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.decisionsAll(item.workspace_id) }),
      client.invalidateQueries({ queryKey: keys.decisionCount(item.workspace_id) }),
    ]);
  } catch (e) {
    if (visit === generation) error.value = (e as Error).message;
  } finally {
    if (visit === generation) busy.value = false;
  }
}
function triage(clear = false) {
  const date = clear ? null : new Date(snooze.value);
  if (date && !Number.isFinite(date.getTime())) return;
  return act('triage', 'PUT', {
    decide_by: props.item.decide_by ?? '',
    decide_by_date: props.item.decide_by_date ?? '',
    snoozed_until: date?.toISOString() ?? null,
  });
}
</script>
<template>
  <div v-if="owner" class="manual-controls">
    <p v-if="deadline" class="private-label">{{ deadline }}</p>
    <ManualDecisionForm
      v-if="editing && item.can_edit"
      v-model="form"
      :busy="busy"
      submit-label="Save changes"
      @submit="act('manual', 'PATCH', form)"
      ><button type="button" class="button" :disabled="busy" @click="editing = false">
        Cancel edit
      </button></ManualDecisionForm
    >
    <div v-else class="actions" role="group" aria-label="Private note actions">
      <button v-if="item.can_edit" class="button" :disabled="busy" @click="edit">Edit</button>
      <button
        v-if="item.can_resolve"
        class="button primary"
        :disabled="busy"
        @click="act('resolve')"
      >
        Resolve
      </button>
      <button v-if="item.can_dismiss" class="button" :disabled="busy" @click="act('dismiss')">
        Dismiss
      </button>
      <button class="button" :disabled="busy || item.kept" @click="act('keep')">
        {{ item.kept ? 'Kept' : 'Keep' }}
      </button>
      <button class="button" :disabled="busy" @click="act(item.archived_at ? 'revive' : 'archive')">
        {{ item.archived_at ? 'Restore' : 'Archive' }}
      </button>
      <label v-if="item.status === 'open'" class="snooze"
        >Snooze until<input
          :value="snooze"
          @input="snooze = ($event.target as HTMLInputElement).value"
          type="datetime-local"
          aria-label="Snooze until"
          :disabled="busy"
      /></label>
      <button
        v-if="item.status === 'open'"
        class="button"
        :disabled="busy || !snooze"
        @click="triage()"
      >
        Snooze
      </button>
      <button v-if="item.snoozed_until" class="button" :disabled="busy" @click="triage(true)">
        Clear snooze
      </button>
    </div>
    <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
  </div>
</template>
<style scoped>
.manual-controls {
  display: grid;
  gap: 16px;
}
.private-label {
  color: var(--muted);
  font-size: 12px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 8px;
}
.snooze {
  display: flex;
  flex-direction: column;
  gap: 5px;
  color: var(--muted);
  font-size: 12px;
}
.snooze input {
  padding: 6px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--surface);
  color: var(--text);
  font: inherit;
}
button:focus-visible,
input:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
