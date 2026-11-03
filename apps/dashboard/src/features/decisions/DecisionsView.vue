<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import AutoresearchRunDialog from '../autoresearch/AutoresearchRunDialog.vue';
import DecisionOptions from './DecisionOptions.vue';
import MachineWait from './MachineWait.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useCards, useDecisions, useMe } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import { agentDecisionParts, decisionKindOf, inboxTitle, isMachineWait, timeAgo } from '../../lib/studies';

const props = defineProps<{ itemId?: string }>();
const client = useQueryClient();
const open = useDecisions(false);
const resolved = useDecisions(true);
const me = useMe();
const cards = useCards();
const selected = computed(() =>
  [...(open.data.value ?? []), ...(resolved.data.value ?? [])].find((i) => i.id === props.itemId),
);
const selectedReport = ref<DecisionItem>();
const display = computed(() => selected.value ?? selectedReport.value);
const study = computed(() => cards.data.value?.find((card) => card.id === display.value?.card_id));
const question = computed(() => {
  const item = display.value;
  if (!item) return { title: '', body: '' };
  if (item.kind === 'agent_decision') return agentDecisionParts(item.text);
  return { title: study.value?.title || (item.card_id ? inboxTitle(item) : decisionKindOf(item.kind).label), body: item.text };
});
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

// Read receipts belong to the workspace and item visit that opened them, and
// only the item's addressee sends one.
watch(
  [
    workspaceId,
    () => props.itemId,
    () => selected.value?.id,
    () => selected.value?.read_at,
    () => me.data.value?.id,
  ],
  async () => {
    const item = selected.value;
    if (!item || item.read_at || item.workspace_id !== workspaceId.value) return;
    if (!me.data.value || item.user_id !== me.data.value.id) return;
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
    <main class="decision-activity">
      <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
      <article v-if="display" class="activity-card">
        <header class="activity-card-header">
          <span>{{ decisionKindOf(display.kind).label }}</span>
          <span class="card-time">{{ timeAgo(display.created_at) }}</span>
        </header>
        <div class="card-body">
          <div class="question-panel">
            <div class="status-line"><span class="status-pill">{{ display.status === 'open' ? 'Pending' : display.status }}</span></div>
            <h1>{{ question.title }}</h1>
            <p v-if="question.body && !isMachineWait(display)" class="question-body">{{ question.body }}</p>
            <p v-if="study?.question && study.question !== question.body" class="study-context">{{ study.question }}</p>
            <DecisionOptions v-if="display.options?.length || display.question_set?.questions?.length" :key="display.id" :item="display" />
            <MachineWait v-if="isMachineWait(display)" :key="display.id" :item="display" />
          </div>
          <p v-if="display.card_id" class="study-link"><RouterLink :to="'/studies/' + display.card_id">Open {{ study?.title || 'study' }} and activity →</RouterLink></p>
          <button v-if="selectedReport" class="button report-button" @click="reportOpen = true">Open report</button>
        </div>
      </article>
      <div v-else class="conversation-empty">
        <Icon name="inbox" :size="38" />
        <p>Select a decision to open its study</p>
      </div>
    </main>
    <AutoresearchRunDialog v-if="reportOpen && selectedReport?.autoresearch_run_id"
      :key="workspaceId + ':' + selectedReport.autoresearch_run_id" :run-id="selectedReport.autoresearch_run_id" @close="reportOpen = false" />
  </section>
</template>
<style scoped src="../../ui/resources.css"></style>
<style scoped>
.decision-activity { padding:28px 32px 64px; max-width:1000px; width:100%; margin:0 auto; }
.activity-card { border:1px solid var(--border); border-radius:10px; background:var(--surface); color:var(--text); overflow:hidden; }
.activity-card-header { display:flex; align-items:center; gap:12px; padding:16px 20px; border-bottom:1px solid var(--border); color:var(--muted); font-size:12px; }
.card-time { margin-left:auto; }
.card-body { display:flex; flex-direction:column; gap:14px; padding:20px; }
.question-panel { display:flex; flex-direction:column; gap:14px; padding:20px; border:1px solid color-mix(in srgb, var(--primary) 45%, var(--border)); border-radius:8px; background:color-mix(in srgb, var(--surface) 94%, var(--primary)); }
.card-body h1 { font-size:20px; line-height:1.35; font-weight:650; margin:0; }
.question-body, .study-context { font-size:14px; line-height:1.65; white-space:pre-wrap; }
.study-context { color:var(--muted); }
.status-pill { display:inline-flex; padding:4px 8px; border:1px solid var(--primary); border-radius:5px; color:var(--primary-text); font-size:10px; font-weight:700; letter-spacing:.08em; text-transform:uppercase; }
.report-button { align-self:flex-start; }
.study-link { font-size:13px; color:var(--primary-text); }
.study-link a:hover { text-decoration:underline; }
.conversation-empty { min-height:300px; }
@media (max-width:700px) { .decision-activity { padding:16px 12px 40px; } .card-body { padding:12px; } .question-panel { padding:16px; } }
</style>
