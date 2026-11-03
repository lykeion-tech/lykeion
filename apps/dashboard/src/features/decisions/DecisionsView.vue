<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import StudyDetail from '../studies/StudyDetail.vue';
import AutoresearchRunDialog from '../autoresearch/AutoresearchRunDialog.vue';
import DecisionOptions from './DecisionOptions.vue';
import MachineWait from './MachineWait.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useDecisions } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import { agentDecisionParts, isMachineWait } from '../../lib/studies';

const props = defineProps<{ itemId?: string }>();
const client = useQueryClient();
const open = useDecisions(false);
const resolved = useDecisions(true);
const selected = computed(() =>
  [...(open.data.value ?? []), ...(resolved.data.value ?? [])].find((i) => i.id === props.itemId),
);
const selectedReport = ref<DecisionItem>();
const reportOpen = ref(false);
let reportVisit = '';
// A read receipt moves the item between lists without changing the open run.
watch(
  [workspaceId, () => props.itemId, () => selected.value],
  ([workspace, itemId, item]) => {
    const visit = `${workspace}:${itemId}`;
    if (visit !== reportVisit) {
      reportVisit = visit;
      selectedReport.value = undefined;
      reportOpen.value = false;
    }
    if (
      !item ||
      item.workspace_id !== workspace ||
      item.id !== itemId ||
      item.kind !== 'autoresearch_report' ||
      !item.autoresearch_run_id
    )
      return;
    const previous = selectedReport.value;
    selectedReport.value = item;
    if (previous?.id !== item.id || previous.autoresearch_run_id !== item.autoresearch_run_id)
      reportOpen.value = true;
  },
  { immediate: true, flush: 'sync' },
);
const error = ref('');
let generation = 0;
watch(
  [workspaceId, () => props.itemId],
  () => {
    generation++;
    error.value = '';
  },
  { flush: 'sync' },
);
onUnmounted(() => generation++);

// Read receipts belong to the workspace and item visit that opened them.
watch(
  [workspaceId, () => props.itemId, () => selected.value?.id, () => selected.value?.read_at],
  async () => {
    const item = selected.value;
    if (!item || item.read_at || item.workspace_id !== workspaceId.value) return;
    const workspace = workspaceId.value;
    const id = item.id;
    const visit = generation;
    const current = () =>
      generation === visit && workspaceId.value === workspace && props.itemId === id;
    try {
      await api('POST', `/api/decisions/${id}/read`);
      if (!current()) return;
      await client.invalidateQueries({ queryKey: keys.decisionsAll(workspace) });
    } catch (e) {
      if (current()) error.value = (e as Error).message;
    }
  },
  { immediate: true },
);
</script>
<template>
  <section class="resources resource-decisions">
    <div class="split-workspace">
      <div class="conversation-content">
        <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
        <DecisionOptions v-if="selected?.options?.length" :key="selected.id" :item="selected" />
        <MachineWait
          v-if="selected && isMachineWait(selected)"
          :key="selected.id"
          :item="selected"
        />
        <StudyDetail
          v-else-if="selected?.card_id && selected.kind !== 'autoresearch_report'"
          :id="selected.card_id"
          :key="selected.card_id"
          embedded
        />
        <div v-else-if="selected?.kind === 'agent_decision'" class="conversation-empty">
          <Icon name="inbox" :size="38" />
          <p class="type-body">{{ agentDecisionParts(selected.text).title }}</p>
          <p class="type-body">{{ agentDecisionParts(selected.text).body }}</p>
        </div>
        <div v-else-if="selectedReport" class="conversation-empty">
          <Icon name="zap" :size="38" />
          <p>{{ selectedReport.text }}</p>
          <button class="button" @click="reportOpen = true">Open report</button>
        </div>
        <div v-else class="conversation-empty">
          <Icon name="inbox" :size="38" />
          <p>Select a decision to open its study</p>
        </div>
      </div>
    </div>
    <AutoresearchRunDialog
      v-if="reportOpen && selectedReport?.autoresearch_run_id"
      :key="workspaceId + ':' + selectedReport.autoresearch_run_id"
      :run-id="selectedReport.autoresearch_run_id"
      @close="reportOpen = false"
    />
  </section>
</template>
<style scoped src="../../ui/resources.css"></style>
