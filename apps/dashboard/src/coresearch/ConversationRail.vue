<script setup lang="ts">
import { useAristotle, newComposerDraft } from './aristotle';
import { isNavigationFailure, NavigationFailureType, type NavigationFailure } from 'vue-router';
import Icon from '../components/Icon.vue';
import Aristotle from '../components/Aristotle.vue';
const state = useAristotle();
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
      <RouterLink
        v-for="c in state.conversations.data.value ?? []"
        :key="c.shared ? `shared:${c.project_id}` : c.id"
        :to="
          c.mode === 'cowrite'
            ? `/coresearch/${c.project_id}/${c.chat_id}`
            : `/coresearch/conversations/${c.id}`
        "
        class="conversation-link"
        active-class="selected"
        ><Aristotle still class="rail-portrait" /><span
          >{{ c.title
          }}<small
            >{{ c.mode === 'cowrite' ? 'Cowrite' : 'Experiment'
            }}{{ c.shared ? ' · Shared' : '' }}</small
          ><small>{{
            c.preview || (c.is_welcome ? 'Your research companion' : 'New conversation')
          }}</small></span
        ></RouterLink
      >
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
.conversation-link:hover,
.conversation-link.selected {
  background: var(--hover);
}
</style>

<style scoped>
.conversation-link :deep(.rail-portrait) {
  width: 22px;
  height: 24px;
  flex: none;
}
</style>
