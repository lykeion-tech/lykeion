<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { useCards, useDecisions, useMe } from '../../api/queries';
import type { DecisionItem } from '../../api/types';
import {
  agentDecisionParts,
  decideByLabel,
  decisionKindOf,
  inboxText,
  inboxTitle,
  timeAgo,
} from '../../lib/studies';

const route = useRoute();
const client = useQueryClient();
const history = ref(false);
const open = useDecisions(false);
const past = useDecisions(true);
const cards = useCards();
const me = useMe();
const menu = ref('');
const error = ref('');
const shown = computed(() => (history.value ? past.data.value : open.data.value) ?? []);
const listError = computed(() => (history.value ? past.error.value : open.error.value));
const kindOf = (item: DecisionItem) => decisionKindOf(item.kind);
const titleOf = (item: DecisionItem) =>
  item.kind === 'agent_decision'
    ? agentDecisionParts(item.text).title
    : inboxTitle(item, cards.data.value?.find((card) => card.id === item.card_id)?.title);
const line = (item: DecisionItem) =>
  item.kind === 'agent_decision'
    ? agentDecisionParts(item.text).body || kindOf(item).label
    : (inboxText(item, titleOf(item)) ?? `${kindOf(item).label}: ${item.text}`);
const decideByText = (item: DecisionItem) =>
  item.decide_by === 'date' ? (item.decide_by_date ?? '') : (decideByLabel[item.decide_by] ?? '');
// Only the item's addressee has it unread; others who can see it never do.
const unread = (item: DecisionItem) => !item.read_at && item.user_id === me.data.value?.id;
// An open decision is answered, not archived.
const archivable = (item: DecisionItem) => item.status !== 'open' || (!item.options?.length && !item.question_set?.questions?.length);
// The reminder went out: the item expires within a day.
const expiresSoon = (item: DecisionItem) => item.status === 'open' && !!item.reminded_at;
const snoozed = (item: DecisionItem) =>
  !!item.snoozed_until && Date.parse(item.snoozed_until) > Date.now();

async function act(item: DecisionItem, action: () => Promise<unknown>) {
  error.value = '';
  try {
    await action();
    menu.value = '';
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.decisionsAll(item.workspace_id) }),
      client.invalidateQueries({ queryKey: keys.decisionCount(item.workspace_id) }),
    ]);
  } catch (e) {
    error.value = (e as Error).message;
  }
}
// The server replaces both triage fields, so each change sends the other as is.
const triage = (
  item: DecisionItem,
  change: { decide_by?: string; decide_by_date?: string; snoozed_until?: string | null },
) =>
  act(item, () =>
    api('PUT', `/api/decisions/${item.id}/triage`, {
      decide_by: item.decide_by,
      decide_by_date: item.decide_by_date ?? undefined,
      snoozed_until: item.snoozed_until,
      ...change,
    }),
  );
const decideBy = (item: DecisionItem, by: string) => triage(item, { decide_by: by });
const decideOnDate = (item: DecisionItem, date: string) =>
  date && triage(item, { decide_by: 'date', decide_by_date: date });
const snoozeUntil = (item: DecisionItem, date: string) =>
  date && triage(item, { snoozed_until: new Date(`${date}T09:00:00`).toISOString() });
const post = (item: DecisionItem, verb: 'keep' | 'archive' | 'revive') =>
  act(item, () => api('POST', `/api/decisions/${item.id}/${verb}`));
</script>
<template>
  <aside class="sidebar decision-rail">
    <header>
      <span>Decisions</span>
      <span class="decision-views" role="group" aria-label="View">
        <button :class="{ on: !history }" :aria-pressed="!history" @click="history = false">
          Open
        </button>
        <button :class="{ on: history }" :aria-pressed="history" @click="history = true">
          History
        </button>
      </span>
    </header>
    <nav aria-label="Decisions">
      <p v-if="listError || error" role="alert" class="inline-error">
        {{ (listError ?? { message: error }).message }}
      </p>
      <div v-for="item in shown" :key="item.id" class="decision-row-wrap">
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
          <span class="decision-side"
            ><small>{{ timeAgo(item.created_at) }}</small
            ><small v-if="decideByText(item)" class="decide-by">{{ decideByText(item) }}</small
            ><small v-if="snoozed(item)" class="snoozed-mark">Snoozed</small
            ><small v-if="expiresSoon(item)" class="expires-soon">Expires soon</small
            ><i v-if="unread(item)" class="unread"
          /></span>
        </RouterLink>
        <button
          class="icon-button row-menu"
          aria-label="Organise"
          :aria-expanded="menu === item.id"
          @click="menu = menu === item.id ? '' : item.id"
        >
          …
        </button>
        <div v-if="menu === item.id" class="row-menu-panel" role="menu">
          <template v-if="!item.archived_at && item.status === 'open'">
            <span class="type-caption">Decide by</span>
            <button v-for="(label, by) in decideByLabel" :key="by" @click="decideBy(item, by)">
              {{ label }}
            </button>
            <label
              >Decide by…
              <input
                type="date"
                aria-label="Decide by date"
                @change="decideOnDate(item, ($event.target as HTMLInputElement).value)"
            /></label>
            <label
              >Snooze until…
              <input
                type="date"
                aria-label="Snooze until"
                @change="snoozeUntil(item, ($event.target as HTMLInputElement).value)"
            /></label>
            <button v-if="snoozed(item)" @click="triage(item, { snoozed_until: null })">
              Unsnooze
            </button>
          </template>
          <button v-if="!item.kept" @click="post(item, 'keep')">Keep</button>
          <button v-if="item.archived_at" @click="post(item, 'revive')">Revive</button>
          <button v-else-if="archivable(item)" @click="post(item, 'archive')">Archive</button>
        </div>
      </div>
      <p v-if="!shown.length" class="decision-empty">
        {{ history ? 'No history yet' : 'Nothing needs you right now' }}
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
.decision-rail header {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  flex: none;
  padding: 0 8px;
  margin-bottom: 8px;
  font-size: 14px;
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
  padding: 8px 10px;
  border-radius: 6px;
  font-size: 13px;
}
.decision-row:hover,
.decision-row.selected {
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
  font-size: 11px;
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
  font-size: 12px;
  color: var(--muted);
  padding: 8px;
}
.decision-views {
  display: flex;
  gap: 2px;
  margin-left: auto;
}
.decision-views button {
  padding: 2px 8px;
  border-radius: 6px;
  color: var(--muted);
  font-size: 12px;
}
.decision-views button.on {
  background: var(--selected);
  color: var(--text);
}
.decision-row-wrap {
  position: relative;
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
.row-menu {
  position: absolute;
  top: 6px;
  right: 2px;
  opacity: 0;
}
.decision-row-wrap:hover .row-menu,
.row-menu:focus-visible,
.row-menu[aria-expanded='true'] {
  opacity: 1;
}
.row-menu-panel {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px;
  margin: 2px 0 6px;
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
}
.row-menu-panel button,
.row-menu-panel label {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 4px 6px;
  border-radius: 4px;
  text-align: left;
}
.row-menu-panel button:hover {
  background: var(--hover);
}
</style>
