<script setup lang="ts">
import { computed, ref } from 'vue';
import { useAristotle, startConversationDraft } from './aristotle';
import { useRouter } from 'vue-router';
import RailActions from '../../ui/RailActions.vue';
import Icon from '../../ui/Icon.vue';
import ConversationActions from './ConversationActions.vue';
const state = useAristotle();
const router = useRouter();
const emit = defineEmits<{ search: [] }>();
const shown = computed(() =>
  (state.conversations.data.value ?? []).filter((c) => c.shared || !c.archived_at),
);
const archived = computed(() =>
  (state.conversations.data.value ?? []).filter((c) => !c.shared && c.archived_at),
);
const showArchived = ref(false);
const groups = computed(() => [
  { archived: false, rows: shown.value },
  { archived: true, rows: showArchived.value ? archived.value : [] },
]);
async function startDraft(event: MouseEvent) {
  const plain =
    !event.defaultPrevented &&
    event.button === 0 &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.shiftKey &&
    !event.altKey;
  if (!plain) return;
  event.preventDefault();
  await startConversationDraft(router);
}
</script>
<template>
  <aside class="sidebar conversation-rail" aria-label="Chat">
    <slot name="brand" />
    <RailActions
      new-to="/coresearch/new"
      new-label="New conversation"
      search-label="Search conversations"
      @new="startDraft"
      @search="emit('search')"
    />
    <nav>
      <template v-for="group in groups" :key="String(group.archived)">
        <h2 v-if="group.archived" class="archive-heading">
          <button
            type="button"
            class="archive-toggle"
            :aria-expanded="showArchived"
            @click="showArchived = !showArchived"
          >
            <Icon :name="showArchived ? 'down' : 'right'" :size="12" />Archived
          </button>
        </h2>
        <div
          v-for="c in group.rows"
          :key="c.shared ? `shared:${c.project_id}` : c.id"
          class="conversation-item"
        >
          <RouterLink
            :to="
              c.mode === 'cowrite'
                ? `/coresearch/${c.project_id}/${c.chat_id}`
                : `/coresearch/conversations/${c.id}`
            "
            class="conversation-link"
            active-class="selected"
          >
            <span
              >{{ c.title }}<small v-if="c.shared">Shared</small
              ><small>{{
                c.preview || (c.is_welcome ? 'Your research companion' : 'New conversation')
              }}</small></span
            ></RouterLink
          >
          <ConversationActions
            v-if="!c.shared"
            class="conversation-actions"
            :conversation="c"
            :variant="group.archived ? 'project' : 'rail'"
          />
        </div>
      </template>
    </nav>
    <p v-if="state.bootstrap.error.value || state.conversations.error.value" role="alert">
      Could not load conversations.
      <button
        @click="
          state.bootstrap.refetch();
          state.conversations.refetch();
        "
      >
        Try again
      </button>
    </p>
    <slot name="footer" />
  </aside>
</template>
<style scoped>
.conversation-rail {
  padding: 0 10px;
  overflow: hidden;
  position: relative;
}
.conversation-rail > nav {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.archive-heading {
  margin: 12px 0 8px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}
.archive-toggle {
  display: flex;
  align-items: center;
  gap: 5px;
  width: fit-content;
  padding: 4px 10px;
  border-radius: 6px;
  color: inherit;
  font: inherit;
  text-align: left;
}
.conversation-link {
  display: block;
  padding: 10px;
  border-radius: 6px;
  font-size: 13px;
}
.conversation-item {
  position: relative;
}
.conversation-item .conversation-link {
  padding-right: 32px;
}
.conversation-actions {
  position: absolute;
  right: 6px;
  top: 50%;
  transform: translateY(-50%);
  opacity: 0;
  pointer-events: none;
}
.conversation-item:hover .conversation-actions,
.conversation-item:focus-within .conversation-actions,
.conversation-item:has(.conversation-link.selected) .conversation-actions,
.conversation-actions:has([aria-expanded='true']) {
  opacity: 1;
  pointer-events: auto;
}
@media (hover: none) {
  .conversation-actions {
    opacity: 1;
    pointer-events: auto;
  }
}
.conversation-link > span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.conversation-link small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  color: var(--muted);
  margin-top: 5px;
}
.conversation-link:hover,
.conversation-link.selected {
  background: var(--hover);
}
</style>
