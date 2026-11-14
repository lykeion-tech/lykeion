<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { Trash2 } from '@lucide/vue';
import { useAristotle, startConversationDraft, type Conversation } from './aristotle';
import { useRoute, useRouter } from 'vue-router';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { guardResourceDrafts } from '../../state/resource-draft';
import RailActions from '../../ui/RailActions.vue';
import ChatAgentAvatar from './ChatAgentAvatar.vue';
import { useAgents } from '../../api/queries';
const state = useAristotle();
const agents = useAgents();
const router = useRouter(),
  route = useRoute(),
  client = useQueryClient();
const emit = defineEmits<{ search: [] }>();
const shown = computed(() => state.conversations.data.value ?? []);
const deleting = ref(''),
  deleteError = ref('');
async function deleteConversation(c: Conversation) {
  if (
    deleting.value ||
    !confirm(
      `Delete “${c.title}” and its project, including files and paper? This cannot be undone.`,
    )
  )
    return;
  const ws = workspaceId.value,
    scope = state.scope.value;
  const active = route.params.conversationId === c.id || route.params.projectId === c.project_id;
  if (active && !(await guardResourceDrafts())) return;
  if (scope !== state.scope.value) return;
  deleting.value = c.id;
  deleteError.value = '';
  try {
    await api(
      'DELETE',
      `/api/coresearch/conversations/${encodeURIComponent(c.id)}`,
      undefined,
      undefined,
      { workspace: ws },
    );
    if (scope !== state.scope.value) return;
    if (route.params.conversationId === c.id || route.params.projectId === c.project_id)
      await router.push('/coresearch/new');
    await state.bootstrap.refetch();
    await client.invalidateQueries({ queryKey: keys.coresearchConversations(ws) });
    await client.invalidateQueries({ queryKey: keys.coresearchProjects(ws) });
  } catch (error) {
    if (scope === state.scope.value) deleteError.value = (error as Error).message;
  } finally {
    deleting.value = '';
  }
}
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
      <div
        v-for="c in shown"
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
          ><ChatAgentAvatar
            :agent="
              c.shared ? undefined : agents.data.value?.find((agent) => agent.id === c.agent_id)
            "
            :size="24"
            :fallback-aristotle="!c.shared && c.agent_id === state.agent.data.value?.id"
          />
          <span
            >{{ c.title }}<small v-if="c.shared">Shared</small
            ><small>{{
              c.preview || (c.is_welcome ? 'Your research companion' : 'New conversation')
            }}</small></span
          ></RouterLink
        >
        <button
          v-if="!c.shared"
          type="button"
          class="conversation-delete"
          :aria-label="`Delete ${c.title}`"
          :disabled="!!deleting"
          @click="deleteConversation(c)"
        >
          <Trash2 :size="15" aria-hidden="true" />
        </button>
      </div>
    </nav>
    <p v-if="deleteError" role="alert">{{ deleteError }}</p>
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
.conversation-link {
  display: flex;
  gap: 9px;
  align-items: flex-start;
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
.conversation-delete {
  position: absolute;
  right: 6px;
  top: 50%;
  transform: translateY(-50%);
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--icon-faint);
  opacity: 0;
  pointer-events: none;
}
.conversation-item:hover .conversation-delete,
.conversation-item:focus-within .conversation-delete {
  opacity: 1;
  pointer-events: auto;
}
@media (hover: none) {
  .conversation-delete {
    opacity: 1;
    pointer-events: auto;
  }
}
.conversation-link > span {
  min-width: 0;
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
