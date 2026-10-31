<script setup lang="ts">
import { ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { Trash2 } from '@lucide/vue';
import { useAristotle, newComposerDraft, type Conversation } from './aristotle';
import {
  isNavigationFailure,
  NavigationFailureType,
  useRoute,
  useRouter,
  type NavigationFailure,
} from 'vue-router';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { guardResourceDrafts } from '../state/resource-draft';
import Icon from '../ui/Icon.vue';
import ChatAgentAvatar from './ChatAgentAvatar.vue';
import { useAgents } from '../api/queries';
const state = useAristotle();
const agents = useAgents();
const router = useRouter(),
  route = useRoute(),
  client = useQueryClient();
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
async function startDraft(
  event: MouseEvent,
  navigate: (event: MouseEvent) => Promise<void | NavigationFailure>,
) {
  const plain =
    !event.defaultPrevented &&
    event.button === 0 &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.shiftKey &&
    !event.altKey;
  const result = await navigate(event);
  if (plain && (!result || isNavigationFailure(result, NavigationFailureType.duplicated)))
    newComposerDraft();
}
</script>
<template>
  <aside class="sidebar conversation-rail" aria-label="Chat">
    <header>
      <RouterLink to="/coresearch">Chat</RouterLink
      ><RouterLink v-slot="{ href, navigate, isActive, isExactActive }" to="/coresearch/new" custom>
        <a
          :href="href"
          aria-label="New conversation"
          class="icon-button"
          :class="{ 'router-link-active': isActive, 'router-link-exact-active': isExactActive }"
          :aria-current="isExactActive ? 'page' : undefined"
          @click="startDraft($event, navigate)"
          ><Icon name="plus" :size="16"
        /></a>
      </RouterLink>
    </header>
    <nav>
      <div
        v-for="c in state.conversations.data.value ?? []"
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
            >{{ c.title
            }}<small class="conversation-mode">{{
              c.mode === 'cowrite' ? 'Cowrite' : 'Experiment'
            }}</small
            ><small v-if="c.shared">Shared</small
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
.conversation-rail header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  color: var(--muted);
  height: 32px;
  line-height: 20px;
  flex: none;
  padding: 0 8px;
  margin-bottom: 8px;
}
.conversation-rail header > a:first-child {
  font-size: 14px;
  font-weight: 400;
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
  top: 7px;
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
  font-size: 11px;
  color: var(--muted);
  margin-top: 5px;
}
.conversation-link .conversation-mode {
  display: table;
  padding: 1px 5px;
  background: var(--fill-chip);
  border: 1px solid var(--border);
  border-radius: 4px;
}
.conversation-link:hover,
.conversation-link.selected {
  background: var(--hover);
}
</style>
