<script setup lang="ts">
import { computed, defineAsyncComponent, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { lazyRecordedMarkdown } from '../../ui/lazy-recorded-markdown';
import ManualDecisionControls from './ManualDecisionControls.vue';
import DecisionOptions from './DecisionOptions.vue';
import DecisionMeta from './DecisionMeta.vue';
import DecisionFilters from './DecisionFilters.vue';
import { matchesDecisionFilters } from './decisionFilters';
import MachineWait from './MachineWait.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useCards, useDecisions, useMe } from '../../api/queries';
import { agentDecisionParts, decisionKindOf, inboxTitle, isMachineWait, timeAgo } from '../../lib/studies';

const RecordedMarkdown = lazyRecordedMarkdown();
const StudyReport = defineAsyncComponent(() => import('../reports/StudyReport.vue'));
const props = defineProps<{ itemId?: string }>();
const client = useQueryClient();
const open = useDecisions(false);
const resolved = useDecisions(true);
const me = useMe();
const cards = useCards();
const selected = computed(() =>
  [...(open.data.value ?? []), ...(resolved.data.value ?? [])].find((i) => i.id === props.itemId && i.workspace_id === workspaceId.value && (i.kind !== 'manual_decision' || (i.user_id === me.data.value?.id && !open.error.value && !resolved.error.value))),
);
const display = selected;
const filteredOut = computed(() => !!display.value && !matchesDecisionFilters(display.value));
const study = computed(() => cards.data.value?.find((card) => card.id === display.value?.card_id));
const question = computed(() => {
  const item = display.value;
  if (!item) return { title: '', body: '' };
  if (item.kind === 'manual_decision') return { title: item.title ?? '', body: item.context ?? '' };
  if (item.kind === 'agent_decision') return agentDecisionParts(item.text);
  return { title: study.value?.title || (item.card_id ? inboxTitle(item) : decisionKindOf(item.kind).label), body: item.text };
});
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
  <StudyReport v-if="display?.kind === 'study_report' && display.card_id" :card-id="display.card_id" />
  <section v-else class="resources resource-decisions">
    <main class="decision-activity">
      <DecisionFilters />
      <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
      <article v-if="display && !filteredOut" class="activity-card">
        <div class="card-body">
          <div class="question-panel">
            <div class="status-line">
              <span class="status-pill">{{ display.status === 'open' ? (display.kind === 'manual_decision' ? 'Open' : 'Pending') : display.status }}</span>
              <div class="status-actions">
                <span class="card-time">{{ timeAgo(display.created_at) }}</span>
                <RouterLink v-if="display.card_id" class="study-link" :to="'/studies/' + display.card_id">Open study →</RouterLink>
              </div>
            </div>
            <h2>{{ question.title }}</h2>
            <div v-if="question.body && !isMachineWait(display)" class="question-body"><RecordedMarkdown :content="question.body" /></div>
            <div v-if="study?.question && study.question !== question.body" class="study-context"><RecordedMarkdown :content="study.question" /></div>
            <ManualDecisionControls v-if="display.kind === 'manual_decision'" :key="display.id" :item="display" />
            <DecisionOptions v-else-if="display.options?.length || display.question_set?.questions?.length" :key="display.id" :item="display" />
            <MachineWait v-if="isMachineWait(display)" :key="display.id" :item="display" />
          </div>
          <DecisionMeta v-if="display.kind !== 'manual_decision' && (display.options?.length || display.question_set?.questions?.length)" :item="display" />
        </div>
      </article>
      <div v-else class="conversation-empty">
        <Icon name="inbox" :size="38" />
        <p v-if="filteredOut">This decision does not match the current filters.</p>
        <p v-else-if="!itemId">Select a decision to open its study</p>
        <p v-else-if="open.error.value || resolved.error.value" role="alert">Unable to load decisions. Please try again.</p>
        <p v-else-if="open.isLoading.value || resolved.isLoading.value" role="status">Loading decision…</p>
        <p v-else>Decision not found or no longer available.</p>
      </div>
    </main>
  </section>
</template>
<style scoped src="../../ui/resources.css"></style>
<style scoped>
.decision-activity { padding:4px 24px 32px; width:100%; }
.activity-card { border:1px solid var(--border); border-radius:10px; background:var(--surface); color:var(--text); overflow:hidden; }
.card-time { color:var(--muted); font-size: 13px; }
.card-body { display:flex; flex-direction:column; gap:14px; padding:20px; }
.question-panel { display:flex; flex-direction:column; gap:14px; padding:20px; border:1px solid color-mix(in srgb, var(--primary) 45%, var(--border)); border-radius:8px; background:color-mix(in srgb, var(--surface) 94%, var(--primary)); }
.card-body h2 { font-size:20px; line-height:1.35; font-weight:650; margin:0; }
.question-body, .study-context { font-size:14px; line-height:1.65; overflow-wrap:anywhere; }
.study-context { color:var(--muted); }
.status-line { display:flex; align-items:center; justify-content:space-between; gap:12px; flex-wrap:wrap; }
.status-actions { display:flex; align-items:center; gap:16px; margin-left:auto; }
.status-pill { display:inline-flex; padding:4px 8px; border:1px solid var(--primary); border-radius:5px; color:var(--primary-text); font-size:10px; font-weight:700; letter-spacing:.08em; text-transform:uppercase; }
.study-link { font-size:13px; color:var(--primary-text); }
.study-link:hover { text-decoration:underline; }
.conversation-empty { min-height:300px; }
@media (max-width:700px) { .decision-activity { padding:16px 12px 40px; } .card-body { padding:12px; } .question-panel { padding:16px; } }
</style>
