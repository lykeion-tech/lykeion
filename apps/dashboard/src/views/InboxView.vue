<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import TaskDetail from './TaskDetail.vue';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useCards, useInbox } from '../api/queries';
import type { InboxItem } from '../api/types';
import { timeAgo } from '../lib/studies';

const props = defineProps<{ itemId?: string }>();
const client = useQueryClient();
const archived = ref(false);
const open = useInbox(false);
const resolved = useInbox(true);
const cards = useCards();
const shown = computed(() => (archived.value ? resolved.data.value : open.data.value) ?? []);
const selected = computed(() =>
  [...(open.data.value ?? []), ...(resolved.data.value ?? [])].find((i) => i.id === props.itemId),
);
const kinds: Record<InboxItem['kind'], { label: string; mark: string }> = {
  escalated: { label: 'Escalated', mark: '!' },
  out_of_budget: { label: 'Out of budget', mark: '$' },
  released: { label: 'Released', mark: '✓' },
  publish_request: { label: 'Publication request', mark: '↗' },
  revised: { label: 'Starting point revised', mark: '✎' },
  reopened: { label: 'Reopened', mark: '↺' },
};
const titleOf = (i: InboxItem) =>
  cards.data.value?.find((c) => c.id === i.card_id)?.title ?? 'Study';

const error = ref('');
const listError = computed(() => (archived.value ? resolved.error.value : open.error.value));
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
      await api('POST', `/api/inbox/${id}/read`);
      if (!current()) return;
      await client.invalidateQueries({ queryKey: keys.inboxAll(workspace) });
    } catch (e) {
      if (current()) error.value = (e as Error).message;
    }
  },
  { immediate: true },
);
</script>
<template>
  <section class="resources resource-inbox">
    <div class="split-workspace">
      <nav class="conversation-list" aria-label="Inbox items">
        <header>
          <button
            v-if="archived"
            class="icon-button"
            aria-label="Back to Inbox"
            @click="archived = false"
          >
            <Icon name="back" />
          </button>
          <h2>{{ archived ? 'Archived' : 'Inbox' }}</h2>
        </header>
        <p v-if="listError" role="alert" class="inline-error">{{ listError.message }}</p>
        <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
        <RouterLink
          v-for="item in shown"
          :key="item.id"
          :to="'/inbox/' + item.id"
          class="conversation-row inbox-row"
          :class="{ selected: item.id === itemId }"
        >
          <span class="workspace-avatar">{{ kinds[item.kind].mark }}</span>
          <span class="inbox-text"
            ><b>{{ titleOf(item) }}</b
            ><small>{{ kinds[item.kind].label }}: {{ item.text }}</small></span
          >
          <span class="inbox-side"
            ><small>{{ timeAgo(item.created_at) }}</small
            ><i v-if="!item.read_at" class="unread"
          /></span>
        </RouterLink>
        <p v-if="!shown.length">
          {{ archived ? 'Nothing archived yet' : 'Nothing needs you right now' }}
        </p>
        <button v-if="!archived" class="archive-row" @click="archived = true">
          <Icon name="archive" /><span>Archived</span
          ><Icon name="right" :size="12" class="push-right" />
        </button>
      </nav>
      <div class="conversation-content">
        <TaskDetail v-if="selected" :id="selected.card_id" :key="selected.card_id" embedded />
        <div v-else class="conversation-empty">
          <Icon name="inbox" :size="38" />
          <p>Select an item to open its study</p>
        </div>
      </div>
    </div>
  </section>
</template>
<style scoped src="./resources.css"></style>
<style scoped>
.inbox-row {
  border-radius: 8px;
  padding: 8px 10px;
}
.inbox-row.selected {
  background: var(--fill-active);
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
</style>
