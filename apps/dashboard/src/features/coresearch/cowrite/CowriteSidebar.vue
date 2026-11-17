<script setup lang="ts">
import type { ChatRow } from '../types';
import FileMenu from './FileMenu.vue';
import Icon from '../../../ui/Icon.vue';
import { formatAgo } from './lib/pane-time';
const props = withDefaults(
  defineProps<{
    name: string;
    chats: ChatRow[];
    activeChatId?: string;
    tab?: 'files' | 'chats';
    owner?: boolean;
    members?: { id: string; name: string }[];
    chatTitles?: Record<string, string>;
    // When each chat last moved, as an ISO time.
    chatTimes?: Record<string, string>;
  }>(),
  { tab: 'files' },
);
const emit = defineEmits<{
  'update:tab': ['files' | 'chats'];
  search: [];
  newChat: [];
  newFile: [];
  upload: [];
  members: [];
  openChat: [string];
}>();
function switchTab(e: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight'].includes(e.key)) return;
  e.preventDefault();
  const next = props.tab === 'files' ? 'chats' : 'files';
  emit('update:tab', next);
  (e.currentTarget as HTMLElement).querySelector<HTMLElement>(`[data-tab="${next}"]`)?.focus();
}
const row = 'flex h-8 items-center gap-2.5 mx-0.5 rounded-lg px-2 type-body';
</script>
<template>
  <aside class="task-sidebar" aria-label="Project sidebar" data-testid="context-rail">
    <div class="tsb-header">
      <span class="tsb-project"
        ><span class="tsb-project-name" :title="name">{{ name }}</span></span
      >
    </div>
    <section class="tsb-members" aria-label="Members">
      <div class="tsb-members-header">
        <span class="type-label tsb-members-title">Members</span>
        <button v-if="owner" type="button" aria-label="Manage members" title="Manage members" @click="emit('members')">
          <Icon name="edit" :size="14" />
        </button>
      </div>
      <ul class="tsb-member-list">
        <li v-for="member in members" :key="member.id" :title="member.name">{{ member.name }}</li>
      </ul>
    </section>
    <div class="tsb-switch mt-2 px-2.5 pb-[3px] pt-1.5">
      <div class="tsb-switch-tabs" role="tablist" aria-label="Sidebar" @keydown="switchTab">
        <button
          v-for="id in ['files', 'chats'] as const"
          :key="id"
          type="button"
          role="tab"
          :data-tab="id"
          :aria-selected="tab === id"
          :aria-controls="`tsb-panel-${id}`"
          :tabindex="tab === id ? 0 : -1"
          class="tsb-switch-tab type-label"
          @click="emit('update:tab', id)"
        >
          {{ id === 'files' ? 'Files' : 'Chats' }}
        </button>
      </div>
      <div class="tsb-switch-actions">
        <button
          type="button"
          class="tsb-switch-action"
          aria-label="Search"
          title="Search"
          @click="emit('search')"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.75"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <circle cx="11" cy="11" r="7" />
            <path d="m21 21-4.3-4.3" />
          </svg></button
        ><FileMenu
          v-if="tab === 'files'"
          label="Add a file"
          adding
          @select="$event === 'New file' ? emit('newFile') : emit('upload')"
        /><button
          v-else
          type="button"
          class="tsb-switch-action"
          aria-label="New chat"
          title="New chat"
          @click="emit('newChat')"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path
              d="M12 5v14M5 12h14"
              stroke="currentColor"
              stroke-width="1.75"
              stroke-linecap="round"
            />
          </svg>
        </button>
      </div>
    </div>
    <div
      v-if="tab === 'files'"
      id="tsb-panel-files"
      class="tsb-panel"
      role="tabpanel"
      aria-label="Files"
    >
      <slot />
    </div>
    <div v-else id="tsb-panel-chats" class="tsb-panel" role="tabpanel" aria-label="Chats">
      <div class="tsb-chats">
        <div
          v-for="(chat, i) in chats"
          :key="chat.id"
          :class="[
            row,
            chat.id === activeChatId
              ? 'bg-surface-2 text-fg'
              : 'text-fg-subtle hover:bg-surface hover:text-fg',
          ]"
          class="tsb-chat"
        >
          <button
            type="button"
            class="tsb-chat-open"
            :aria-current="chat.id === activeChatId ? 'page' : undefined"
            @click="emit('openChat', chat.id)"
          >
            <span class="tsb-chat-dot" aria-hidden="true" /><span class="tsb-chat-title">{{
              chatTitles?.[chat.id] || (i === 0 ? name : 'Conversation')
            }}</span
            ><span class="tsb-chat-time">{{
              formatAgo(Date.parse(chatTimes?.[chat.id] ?? chat.created_at) / 1000)
            }}</span>
          </button>
        </div>
      </div>
    </div>
  </aside>
</template>
