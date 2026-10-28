<script setup lang="ts">
import { useAristotle } from './aristotle';
import Icon from '../components/Icon.vue';
import Aristotle from '../components/Aristotle.vue';
const state = useAristotle();
</script>
<template>
  <aside class="sidebar conversation-rail" aria-label="Conversations">
    <header>
      <RouterLink to="/coresearch">Conversations</RouterLink
      ><RouterLink to="/coresearch/new" aria-label="New conversation" class="icon-button"
        ><Icon name="plus" :size="16"
      /></RouterLink>
    </header>
    <nav>
      <RouterLink
        v-for="c in state.conversations.data.value ?? []"
        :key="c.id"
        :to="`/coresearch/conversations/${c.id}`"
        class="conversation-link"
        active-class="selected"
        ><Aristotle still class="rail-portrait" /><span
          >{{ c.title
          }}<small>{{
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
    <RouterLink to="/settings" class="rail-settings"
      ><Icon name="settings" :size="16" />Settings</RouterLink
    >
  </aside>
</template>
<style scoped>
.conversation-rail {
  padding: 16px 10px 100px;
  overflow: auto;
  position: relative;
}
.conversation-rail header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  color: var(--muted);
  padding: 4px 10px;
  margin-bottom: 8px;
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
.rail-settings {
  position: absolute;
  bottom: 12px;
  left: 20px;
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 13px;
}
</style>

<style scoped>
.conversation-link :deep(.rail-portrait) {
  width: 22px;
  height: 24px;
  flex: none;
}
.conversation-link :deep(.rail-portrait > span) {
  width: 59px;
  height: 83px;
  transform: translate(-14px, -3px);
}
</style>
