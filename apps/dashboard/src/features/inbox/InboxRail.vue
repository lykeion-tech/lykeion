<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import Icon from '../../ui/Icon.vue';
import { useCards, useInbox } from '../../api/queries';
import type { InboxItem } from '../../api/types';
import { inboxText, timeAgo } from '../../lib/studies';

const route = useRoute();
const archived = ref(false);
const open = useInbox(false);
const resolved = useInbox(true);
const cards = useCards();
const shown = computed(() => (archived.value ? resolved.data.value : open.data.value) ?? []);
const listError = computed(() => (archived.value ? resolved.error.value : open.error.value));
const kinds: Record<InboxItem['kind'], { label: string; mark: string }> = {
  escalated: { label: 'Escalated', mark: '!' },
  released: { label: 'Released', mark: '✓' },
  autoresearch_card: { label: 'Autoresearch study', mark: '↻' },
  autoresearch_report: { label: 'Autoresearch report', mark: '↻' },
  middle_score: { label: 'Middle score', mark: '~' },
  blocked: { label: 'Blocked', mark: '!' },
  no_tool: { label: 'No tool', mark: '?' },
  scoring_unavailable: { label: 'Scoring unavailable', mark: '!' },
  waiting_for_machine: { label: 'Waiting for machine', mark: '!' },
  done: { label: 'Done', mark: '✓' },
};
const titleOf = (item: InboxItem) =>
  cards.data.value?.find((card) => card.id === item.card_id)?.title ??
  (item.kind === 'autoresearch_report' ? 'Autoresearch' : 'Study');
const line = (item: InboxItem) =>
  inboxText(item, titleOf(item)) ?? `${kinds[item.kind].label}: ${item.text}`;
</script>
<template>
  <aside class="sidebar inbox-rail">
    <header>
      <button
        v-if="archived"
        class="icon-button"
        aria-label="Back to Inbox"
        @click="archived = false"
      >
        <Icon name="back" />
      </button>
      <span>{{ archived ? 'Archived' : 'Inbox' }}</span>
    </header>
    <nav aria-label="Inbox items">
      <p v-if="listError" role="alert" class="inline-error">{{ listError.message }}</p>
      <RouterLink
        v-for="item in shown"
        :key="item.id"
        :to="'/inbox/' + item.id"
        class="inbox-row"
        :class="{ selected: item.id === route.params.itemId }"
      >
        <span class="workspace-avatar">{{ kinds[item.kind].mark }}</span>
        <span class="inbox-text"
          ><b>{{ titleOf(item) }}</b
          ><small>{{ line(item) }}</small></span
        >
        <span class="inbox-side"
          ><small>{{ timeAgo(item.created_at) }}</small
          ><i v-if="!item.read_at" class="unread"
        /></span>
      </RouterLink>
      <p v-if="!shown.length" class="inbox-empty">
        {{ archived ? 'Nothing archived yet' : 'Nothing needs you right now' }}
      </p>
      <button v-if="!archived" class="archive-row" @click="archived = true">
        <Icon name="archive" /><span>Archived</span
        ><Icon name="right" :size="12" class="push-right" />
      </button>
    </nav>
    <slot name="footer" />
  </aside>
</template>
<style scoped>
.inbox-rail {
  padding: 0 10px;
  overflow: hidden;
}
.inbox-rail header {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  flex: none;
  padding: 0 8px;
  margin-bottom: 8px;
  font-size: 14px;
}
.inbox-rail nav {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.inbox-row {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 10px;
  border-radius: 6px;
  font-size: 13px;
}
.inbox-row:hover,
.inbox-row.selected {
  background: var(--hover);
}
.inbox-text {
  flex: 1;
  min-width: 0;
}
.inbox-text b,
.inbox-text small {
  display: block;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.inbox-text b {
  font-weight: 500;
}
.inbox-text small,
.inbox-side small {
  color: var(--muted);
  font-size: 11px;
}
.inbox-text small {
  margin-top: 5px;
}
.inbox-side {
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
.inbox-empty {
  font-size: 12px;
  color: var(--muted);
  padding: 8px;
}
.archive-row {
  display: flex;
  gap: 9px;
  align-items: center;
  width: 100%;
  font-size: 12px;
  padding: 10px;
}
</style>
