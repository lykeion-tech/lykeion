<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { decisionFilters, matchesDecisionFilters } from './decisionFilters';
import { workspaceId } from '../../api/session';
import { manualDeadlineLabel } from './manualDecisionDraft';
import Icon from '../../ui/Icon.vue';
import RailActions from '../../ui/RailActions.vue';
import { matchesWords } from '../../lib/search';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { useCards, useDecisions, useMe } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import {
  agentDecisionParts,
  decideByLabel,
  decisionGroupOf,
  decisionGroups,
  decisionKindOf,
  inboxText,
  inboxTitle,
  timeAgo,
} from '../../lib/studies';

const route = useRoute();
const client = useQueryClient();
const open = useDecisions(false);
const past = useDecisions(true);
const cards = useCards();
const me = useMe();
const error = ref('');
const archivedExpanded = ref(false);
const query = ref('');
const shown = computed(() => {
  const items = new Map([...(open.data.value ?? []), ...(past.data.value ?? [])].map((item) => [item.id, item]));
  return [...items.values()].filter((item) => item.workspace_id === workspaceId.value && (item.kind !== 'manual_decision' || (item.user_id === me.data.value?.id && !open.error.value && !past.error.value)) && matchesDecisionFilters(item)).sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
});
const searched = computed(() =>
  query.value.trim()
    ? shown.value.filter((item) =>
        matchesWords([titleOf(item), line(item), item.text, item.context].filter(Boolean).join(' '), query.value),
      )
    : shown.value,
);
const groups = computed(() => [
  ...decisionGroups.map((group) => ({
    ...group,
    items: searched.value.filter((item) => !item.archived_at && decisionGroupOf(item.kind) === group.id),
  })),
  { id: 'archived', label: 'Archived', items: searched.value.filter((item) => !!item.archived_at) },
].filter((group) => group.items.length));
const listError = computed(() => open.error.value ?? past.error.value);
const kindOf = (item: DecisionItem) => decisionKindOf(item.kind);
const titleOf = (item: DecisionItem) =>
  item.kind === 'manual_decision' ? item.title ?? '' : item.kind === 'agent_decision'
    ? agentDecisionParts(item.text).title
    : inboxTitle(item, cards.data.value?.find((card) => card.id === item.card_id)?.title);
const line = (item: DecisionItem) =>
  item.kind === 'manual_decision' ? item.context || 'Private note' : item.kind === 'agent_decision'
    ? agentDecisionParts(item.text).body || kindOf(item).label
    : (inboxText(item, titleOf(item)) ?? `${kindOf(item).label}: ${item.text}`);
const decideByText = (item: DecisionItem) =>
  item.decide_by === 'date' ? (item.decide_by_date ?? '') : (decideByLabel[item.decide_by] ?? '');
// Only the item's addressee has it unread; others who can see it never do.
const unread = (item: DecisionItem) => !item.read_at && item.user_id === me.data.value?.id;
// An open decision is answered, not archived.
const archivable = (item: DecisionItem) => item.status !== 'open' || (!item.options?.length && !item.question_set?.questions?.length);
// Manual reminders do not expire the note.
const expiresSoon = (item: DecisionItem) => item.status === 'open' && !!item.reminded_at;
const snoozed = (item: DecisionItem) =>
  !!item.snoozed_until && Date.parse(item.snoozed_until) > Date.now();

async function act(item: DecisionItem, action: () => Promise<unknown>) {
  error.value = '';
  try {
    await action();
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.decisionsAll(item.workspace_id) }),
      client.invalidateQueries({ queryKey: keys.decisionCount(item.workspace_id) }),
    ]);
  } catch (e) {
    error.value = (e as Error).message;
  }
}
const post = (item: DecisionItem, verb: 'archive' | 'revive') =>
  act(item, () => api('POST', `/api/decisions/${item.id}/${verb}`));
</script>
<template>
  <aside class="sidebar decision-rail">
    <slot name="brand" />
    <RailActions
      v-model="query"
      new-to="/decisions/new"
      new-label="Add decision"
      search-label="Search decisions"
    />
    <nav aria-label="Decisions">
      <p v-if="listError || error" role="alert" class="inline-error">
        {{ (listError ?? { message: error }).message }}
      </p>
      <template v-for="group in groups" :key="group.id">
      <h2 class="decision-group-title">
        <template v-if="group.id !== 'archived'">{{ group.label }}</template>
        <button v-else class="archive-toggle" :aria-expanded="archivedExpanded" @click="archivedExpanded = !archivedExpanded">
          <Icon :name="archivedExpanded ? 'down' : 'right'" :size="12" />{{ group.label }}
        </button>
      </h2>
      <div v-for="item in group.id === 'archived' && !archivedExpanded && !query.trim() ? [] : group.items" :key="item.id" class="decision-row-wrap">
        <RouterLink
          :to="'/decisions/' + item.id"
          class="decision-row"
          :class="{ selected: item.id === route.params.itemId, snoozed: snoozed(item) }"
        >
          <span class="workspace-avatar">{{ kindOf(item).mark }}</span>
          <span class="decision-text"
            ><b>{{ titleOf(item) }}</b
            ><small>{{ line(item) }}</small></span
          >
        </RouterLink>
        <span class="decision-side">
          <span class="decision-time-slot">
            <small class="decision-time">{{ timeAgo(item.created_at) }}</small>
            <button
              class="icon-button row-archive"
              :aria-label="item.archived_at ? 'Restore decision' : 'Archive decision'"
              :title="item.archived_at ? 'Restore decision' : archivable(item) ? 'Archive decision' : 'Answer this decision before archiving'"
              :disabled="!item.archived_at && !archivable(item)"
              @click="post(item, item.archived_at ? 'revive' : 'archive')"
            >
              <Icon :name="item.archived_at ? 'refresh' : 'archive'" :size="14" />
            </button>
          </span>
          <small v-if="decideByText(item)" class="decide-by">{{ decideByText(item) }}</small>
          <small v-if="snoozed(item)" class="snoozed-mark">Snoozed</small>
          <small v-if="item.kind === 'manual_decision' && item.deadline" class="decide-by">{{ manualDeadlineLabel(item.deadline, me.data.value?.timezone ?? 'UTC') }}</small>
          <small v-if="expiresSoon(item)" class="expires-soon">{{ item.kind === 'manual_decision' ? 'Reminder sent' : 'Expires soon' }}</small>
          <i v-if="unread(item)" class="unread" />
        </span>
      </div>
      </template>
      <p v-if="!searched.length" class="decision-empty">
        {{ query.trim() ? 'No decisions match your search' : decisionFilters.type !== 'all' || decisionFilters.date !== 'all' ? 'No decisions match these filters' : 'No decisions yet' }}
      </p>
    </nav>
    <slot name="footer" />
  </aside>
</template>
<style scoped>
.decision-rail {
  padding: 0 10px;
  overflow: hidden;
}
.decision-rail nav {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.decision-row {
  display: flex;
  align-items: center;
  gap: 9px;
  flex: 1;
  min-width: 0;
  font-size: 13px;
}
.decision-row-wrap:hover,
.decision-row-wrap:has(.decision-row.selected) {
  background: var(--hover);
}
.decision-text {
  flex: 1;
  min-width: 0;
}
.decision-text b,
.decision-text small {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.decision-text b {
  font-weight: 500;
}
.decision-text small,
.decision-side small {
  color: var(--muted);
  font-size: 13px;
}
.decision-text small {
  margin-top: 5px;
}
.decision-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
}
.unread {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--primary);
}
.decision-empty {
  font-size: 13px;
  color: var(--muted);
  padding: 8px;
}
.decision-group-title {
  margin: 0 0 4px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}
.decision-group-title:not(:has(.archive-toggle)) { padding: 4px 10px; }
.archive-toggle {
  display: flex;
  align-items: center;
  gap: 5px;
  width: 100%;
  padding: 4px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}
.archive-toggle:hover {
  background: var(--hover);
}
.decision-row-wrap {
  position: relative;
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 10px;
  border-radius: 6px;
}
.decision-row.snoozed {
  opacity: 0.55;
}
.decide-by,
.snoozed-mark {
  color: var(--primary-text);
}
.decision-side .expires-soon {
  color: var(--danger-text);
}
.decision-time-slot {
  display: grid;
  place-items: center;
}
.decision-time-slot > * {
  grid-area: 1 / 1;
}
.row-archive {
  width: 20px;
  height: 16px;
  opacity: 0;
}
.row-archive:disabled { display: none; }
.decision-row-wrap:hover .row-archive,
.row-archive:focus-visible { opacity: 1; }
.decision-row-wrap:has(.row-archive:not(:disabled)):hover .decision-time,
.decision-row-wrap:has(.row-archive:focus-visible) .decision-time { visibility: hidden; }
</style>
