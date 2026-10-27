<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useMe, useMembers } from '../api/queries';
import { workspaceId } from '../api/session';
import {
  isActiveTurn,
  useCoresearchProject,
  useCoresearchChats,
  useCoresearchMessages,
  useCoresearchFiles,
} from '../coresearch/api';
import type { FileRow } from '../coresearch/types';
import { PANE_TABS, tabAfterClose } from '../coresearch/pane-tabs';
import ProjectSidebar from '../coresearch/ProjectSidebar.vue';
import ChatTranscript from '../coresearch/ChatTranscript.vue';
import ChatComposer from '../coresearch/ChatComposer.vue';
import RightPaneTabs from '../coresearch/RightPaneTabs.vue';
import MembersDialog from '../coresearch/MembersDialog.vue';
import NewChatDialog from '../coresearch/NewChatDialog.vue';
import '../coresearch/l2.css';
const props = defineProps<{ projectId: string; chatId?: string }>();
const router = useRouter(),
  me = useMe(),
  members = useMembers();
const project = useCoresearchProject(() => props.projectId),
  chats = useCoresearchChats(() => props.projectId),
  files = useCoresearchFiles(() => props.projectId);
const chat = computed(
  () =>
    chats.data.value?.find((c) => c.id === props.chatId) ??
    (!props.chatId ? chats.data.value?.[0] : undefined),
);
const messages = useCoresearchMessages(
  () => props.projectId,
  () => chat.value?.id ?? '',
);
const owner = computed(() => !!me.data.value && chat.value?.owner_id === me.data.value.id);
const activeRun = computed(
  () =>
    messages.data.value?.find((m) => m.kind === 'turn' && isActiveTurn(m.status))?.agent_run_id ??
    undefined,
);
const membersOpen = ref(false),
  newChat = ref(false),
  sidebarOpen = ref(false);
const tabs = ref([...PANE_TABS]),
  activePane = ref('notebook'),
  paneMode = ref<'closed' | 'split' | 'focus'>('closed'),
  activeFile = ref<FileRow>();
const stream = ref<HTMLElement>(),
  atBottom = ref(true);
watch(
  () => [workspaceId.value, props.projectId],
  () => {
    membersOpen.value = false;
    newChat.value = false;
    activeFile.value = undefined;
    tabs.value = [...PANE_TABS];
    paneMode.value = 'closed';
    sidebarOpen.value = false;
  },
);
watch(
  () => chat.value?.id,
  () => {
    atBottom.value = true;
    void scrollBottom();
  },
);
watch(
  () => messages.data.value,
  () => {
    if (atBottom.value) void scrollBottom();
  },
  { deep: true },
);
async function scrollBottom() {
  await nextTick();
  if (stream.value) stream.value.scrollTop = stream.value.scrollHeight;
}
watch(stream, (el, _old, onCleanup) => {
  if (typeof ResizeObserver === 'undefined' || !el?.firstElementChild) return;
  const observer = new ResizeObserver(() => {
    if (atBottom.value) void scrollBottom();
  });
  observer.observe(el.firstElementChild);
  onCleanup(() => observer.disconnect());
});
function trackScroll() {
  const el = stream.value;
  if (el) atBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
}
function openChat(id: string) {
  newChat.value = false;
  sidebarOpen.value = false;
  void router.push(`/coresearch/${props.projectId}/${id}`);
}
function openPane(id: string) {
  if (!tabs.value.some((t) => t.id === id)) {
    tabs.value = PANE_TABS.filter(
      (t) => t.id === id || tabs.value.some((open) => open.id === t.id),
    );
  }
  activePane.value = id;
  paneMode.value = 'split';
  sidebarOpen.value = false;
}
function closeTab(id: string) {
  if (activePane.value === id) {
    const next = tabAfterClose(tabs.value, id);
    if (next) activePane.value = next;
    else paneMode.value = 'closed';
  }
  tabs.value = tabs.value.filter((t) => t.id !== id);
}
function openFile(file: FileRow) {
  activeFile.value = file;
  openPane('files');
}
</script>
<template>
  <section class="coresearch coresearch-workspace">
    <p v-if="project.error.value" role="alert" class="p-5">{{ project.error.value.message }}</p>
    <p v-else-if="!project.data.value" role="status" class="p-5">Loading project…</p>
    <template v-else>
      <div class="coresearch-project-sidebar" :class="{ 'is-open': sidebarOpen }">
        <ProjectSidebar
          :name="project.data.value.name"
          :chats="chats.data.value ?? []"
          :active-chat-id="chat?.id"
          :members="members.data.value ?? []"
          :files="files.data.value ?? []"
          :active-file-id="activeFile?.id"
          :files-loading="files.isPending.value"
          :files-error="files.error.value?.message"
          :archived="!!project.data.value.archived_at"
          @open-chat="openChat"
          @new-chat="newChat = true"
          @open-pane="openPane"
          @open-file="openFile"
          @members="membersOpen = true"
        />
      </div>
      <div class="task-screen coresearch-project-main" :data-pane-mode="paneMode">
        <div class="task-columns" :data-pane-mode="paneMode" style="--task-utility-width: 360px">
          <div class="task-main" data-column="conversation" :hidden="paneMode === 'focus'">
            <div class="task-stage-row">
              <button
                type="button"
                class="quiet-action coresearch-sidebar-toggle"
                :aria-expanded="sidebarOpen"
                aria-label="Project sidebar"
                @click="sidebarOpen = !sidebarOpen"
              >
                <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                  <path d="M3 4h10M3 8h10M3 12h10" stroke="currentColor" stroke-width="1.2" /></svg
                ><span class="truncate">{{ project.data.value.name }}</span></button
              ><span v-if="project.data.value.archived_at" class="type-small text-fg-subtle"
                >Archived</span
              >
            </div>
            <div v-if="chats.error.value" class="tab-strip-error" role="alert">
              {{ chats.error.value.message }}
            </div>
            <div class="task-main-body">
              <section class="conversation" data-testid="conversation">
                <div
                  ref="stream"
                  class="conv-stream"
                  data-testid="conv-stream"
                  @scroll="trackScroll"
                >
                  <div class="conv-column">
                    <p v-if="messages.error.value" role="alert" class="type-body text-fg-subtle">
                      {{ messages.error.value.message }}
                    </p>
                    <template v-else-if="chat"
                      ><p
                        v-if="messages.isPending.value"
                        role="status"
                        class="type-body text-fg-subtle"
                      >
                        Loading chat…
                      </p>
                      <ChatTranscript
                        v-else
                        :key="`${workspaceId}:${projectId}:${chat.id}`"
                        :project-id="projectId"
                        :chat-id="chat.id"
                        :messages="messages.data.value ?? []"
                    /></template>
                    <p
                      v-else-if="props.chatId && !chats.isPending.value"
                      role="alert"
                      class="type-body text-fg-subtle"
                    >
                      This chat is not available.
                    </p>
                    <p v-else class="type-body text-fg-subtle">
                      Start a chat to begin working on this project.
                    </p>
                  </div>
                </div>
                <div class="composer-dock">
                  <button v-if="!atBottom" type="button" class="quiet-action" @click="scrollBottom">
                    Jump to latest
                  </button>
                  <div class="composer-column">
                    <ChatComposer
                      v-if="chat"
                      :key="`${workspaceId}:${projectId}:${chat.id}`"
                      :project-id="projectId"
                      :chat-id="chat.id"
                      :owner="owner"
                      :active-run="activeRun"
                      :disabled="
                        !!project.data.value.archived_at ||
                        messages.isPending.value ||
                        !!messages.error.value
                      "
                      @sent="
                        atBottom = true;
                        scrollBottom();
                      "
                    />
                  </div>
                </div>
              </section>
            </div>
          </div>
          <button
            v-if="paneMode !== 'closed'"
            type="button"
            class="task-pane-scrim"
            aria-label="Close panel"
            @click="paneMode = 'closed'"
          />
          <div v-if="paneMode !== 'closed'" class="task-rightpane">
            <RightPaneTabs
              :tabs="tabs"
              :active-id="activePane"
              :pane-mode="paneMode"
              @select="activePane = $event"
              @close-tab="closeTab"
              @toggle-focus="paneMode = paneMode === 'focus' ? 'split' : 'focus'"
              @close-pane="paneMode = 'closed'"
            />
            <div class="task-rightpane-card">
              <div
                role="tabpanel"
                :aria-label="PANE_TABS.find((t) => t.id === activePane)?.label"
                class="p-5 type-body text-fg-subtle"
              >
                <p v-if="activePane === 'files' && activeFile" class="mb-2 text-fg">
                  {{ activeFile.path }}
                </p>
                {{ PANE_TABS.find((t) => t.id === activePane)?.label }} is not available in this
                build yet.
              </div>
            </div>
          </div>
        </div>
      </div>
      <MembersDialog
        v-if="membersOpen"
        :project-id="projectId"
        :editable="project.data.value.role === 'owner' && !project.data.value.archived_at"
        @close="membersOpen = false"
      />
      <NewChatDialog
        v-if="newChat"
        :project-id="projectId"
        @close="newChat = false"
        @created="openChat"
      />
    </template>
  </section>
</template>
<style scoped>
.coresearch-workspace {
  display: flex;
  flex: 1;
  min-height: 0;
  min-width: 0;
  position: relative;
  overflow: hidden;
}
.coresearch-project-sidebar {
  display: flex;
  flex: 0 0 224px;
  min-width: 0;
  min-height: 0;
}
.coresearch-project-main {
  flex: 1;
  min-width: 0;
}
.coresearch-sidebar-toggle {
  display: none;
}
@media (max-width: 959px) {
  .coresearch-project-sidebar {
    display: none;
  }
  .coresearch-project-sidebar.is-open {
    display: flex;
    position: absolute;
    inset: 36px auto 0 0;
    width: 224px;
    z-index: 40;
    box-shadow: 8px 0 24px #0002;
  }
  .coresearch-sidebar-toggle {
    display: inline-flex;
  }
}
</style>
