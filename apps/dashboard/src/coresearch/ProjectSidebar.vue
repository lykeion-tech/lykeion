<script setup lang="ts">
import { ref } from 'vue';
import type { Member } from '../api/types';
import { backendLabel } from '../lib/backends';
import type { ChatRow, FileRow } from './types';
import CliIcon from './CliIcon.vue';
import SidebarFiles from './SidebarFiles.vue';
import CoresearchModal from './CoresearchModal.vue';
const props = defineProps<{
  name: string;
  chats: ChatRow[];
  activeChatId?: string;
  members: Member[];
  files: FileRow[];
  activeFileId?: string;
  filesLoading?: boolean;
  filesError?: string;
  archived?: boolean;
}>();
const emit = defineEmits<{
  openChat: [id: string];
  newChat: [];
  openPane: [id: string];
  openFile: [file: FileRow];
  members: [];
}>();
const tab = ref<'files' | 'chats'>('chats'),
  allFiles = ref(false);
const SIDEBAR_FILES_LIMIT = 10;
const person = (id: string) => {
  const member = props.members.find((m) => m.user_id === id);
  return member?.name?.trim() || member?.email?.trim() || id;
};
const row = 'flex h-8 items-center gap-2.5 mx-0.5 rounded-lg px-2 type-body';
function switchTab(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight'].includes(event.key)) return;
  event.preventDefault();
  tab.value = tab.value === 'files' ? 'chats' : 'files';
  (event.currentTarget as HTMLElement)
    .querySelector<HTMLElement>(`[data-tab="${tab.value}"]`)
    ?.focus();
}
</script>
<template>
  <aside class="task-sidebar" aria-label="Project sidebar">
    <div class="tsb-header">
      <span class="tsb-project"
        ><span class="tsb-project-name" :title="name">{{ name }}</span></span
      >
    </div>
    <div class="tsb-menu">
      <button
        type="button"
        :class="row"
        class="text-fg-subtle hover:bg-surface hover:text-fg"
        @click="emit('members')"
      >
        Members
      </button>
    </div>
    <div class="tsb-switch mt-2 px-2.5 pb-[3px] pt-1.5">
      <div class="tsb-switch-tabs" role="tablist" aria-label="Sidebar" @keydown="switchTab">
        <button
          v-for="id in ['files', 'chats'] as const"
          :key="id"
          type="button"
          role="tab"
          :data-tab="id"
          :aria-selected="tab === id"
          :tabindex="tab === id ? 0 : -1"
          :aria-controls="`tsb-panel-${id}`"
          class="tsb-switch-tab type-label"
          @click="tab = id"
        >
          {{ id === 'files' ? 'Files' : 'Chats' }}
        </button>
      </div>
      <div class="tsb-switch-actions">
        <button
          type="button"
          class="tsb-switch-action"
          aria-label="Search project"
          title="Project search is not available in this build yet"
          disabled
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5" stroke="currentColor" stroke-width="1.2" />
            <path
              d="m10.5 10.5 3 3"
              stroke="currentColor"
              stroke-width="1.2"
              stroke-linecap="round"
            />
          </svg>
        </button>
        <button
          type="button"
          class="tsb-switch-action"
          aria-label="New chat"
          title="New chat"
          :disabled="archived"
          @click="emit('newChat')"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path
              d="M8 3v10M3 8h10"
              stroke="currentColor"
              stroke-width="1.2"
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
      <SidebarFiles
        :files="files"
        :active-id="activeFileId"
        :limit="SIDEBAR_FILES_LIMIT"
        :loading="filesLoading"
        :error="filesError"
        @open="emit('openFile', $event)"
        @see-all="allFiles = true"
      />
    </div>
    <div v-else id="tsb-panel-chats" class="tsb-panel" role="tabpanel" aria-label="Chats">
      <div class="tsb-chats">
        <div
          v-for="chat in chats"
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
            <span
              class="grid h-4 w-4 shrink-0 place-items-center rounded-full bg-surface-2 type-small"
              :title="person(chat.owner_id)"
              :aria-label="`Owner: ${person(chat.owner_id)}`"
              >{{ person(chat.owner_id).charAt(0).toUpperCase() }}</span
            ><span class="tsb-chat-title">{{ person(chat.owner_id) }}</span
            ><CliIcon
              :cli="chat.cli"
              class="h-4 w-4 shrink-0"
              :aria-label="backendLabel(chat.cli)"
            />
          </button>
        </div>
        <p v-if="!chats.length" class="tsb-tasks-empty">No chats yet.</p>
      </div>
    </div>
    <CoresearchModal v-if="allFiles" label="Files" files @close="allFiles = false"
      ><template #default="{ close }"
        ><div class="ref-modal-body ref-library">
          <header class="ref-library-head">
            <h2 class="ref-library-title">Files</h2>
            <button type="button" class="ref-pane-close" aria-label="Close files" @click="close">
              ×
            </button>
          </header>
          <SidebarFiles
            :files="files"
            :active-id="activeFileId"
            @open="
              emit('openFile', $event);
              close();
            "
          /></div></template
    ></CoresearchModal>
  </aside>
</template>
