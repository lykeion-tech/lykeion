<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useMe } from '../api/queries';
import { workspaceId } from '../api/session';
import { guardResourceDrafts } from '../state/resource-draft';
import {
  isActiveTurn,
  useCoresearchProject,
  useCoresearchChats,
  useCoresearchMessages,
  useCoresearchFiles,
} from '../coresearch/api';
import type { FileRow } from '../coresearch/types';
import { PANE_TABS } from '../coresearch/pane-tabs';
import SidebarFiles from '../coresearch/SidebarFiles.vue';
import ChatTranscript from '../coresearch/ChatTranscript.vue';
import ChatComposer from '../coresearch/ChatComposer.vue';
import RightPaneTabs from '../coresearch/RightPaneTabs.vue';
import NotebookPane from '../coresearch/NotebookPane.vue';
import ComputePane from '../coresearch/ComputePane.vue';
import FilesPane from '../coresearch/FilesPane.vue';
import '../coresearch/l2.css';
const props = defineProps<{ projectId: string; chatId?: string }>();
const me = useMe();
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
const activePane = ref('notebook'),
  paneMode = ref<'closed' | 'split' | 'focus'>('closed'),
  activeFile = ref<FileRow>();
const stream = ref<HTMLElement>(),
  atBottom = ref(true);
watch(
  () => [workspaceId.value, props.projectId],
  () => {
    activeFile.value = undefined;
    activePane.value = 'notebook';
    paneMode.value = 'closed';
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
async function discardFile() {
  return guardResourceDrafts();
}
async function selectPane(id: string) {
  if (activePane.value === 'files' && !(await discardFile())) return;
  if (id === 'files') activeFile.value = undefined;
  activePane.value = id;
}
async function closePane() {
  if (activePane.value === 'files' && !(await discardFile())) return;
  paneMode.value = 'closed';
}
async function openPane(id: string) {
  if (activePane.value === 'files' && !(await discardFile())) return;
  activePane.value = id;
  paneMode.value = 'split';
}
async function openFile(file: FileRow) {
  if (!(await discardFile())) return;
  activeFile.value = file;
}
</script>
<template>
  <section class="coresearch coresearch-workspace">
    <p v-if="project.error.value" role="alert" class="p-5">{{ project.error.value.message }}</p>
    <p v-else-if="!project.data.value" role="status" class="p-5">Loading project…</p>
    <template v-else>
      <div class="task-screen coresearch-project-main" :data-pane-mode="paneMode">
        <div class="task-columns" :data-pane-mode="paneMode" style="--task-utility-width: 360px">
          <div class="task-main" data-column="conversation" :hidden="paneMode === 'focus'">
            <div class="task-stage-row"></div>
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
                      ><template #top
                        ><button
                          type="button"
                          class="composer-notebook-chip"
                          :class="{ 'is-on': paneMode !== 'closed' && activePane === 'notebook' }"
                          :aria-pressed="paneMode !== 'closed' && activePane === 'notebook'"
                          @click="openPane('notebook')"
                        >
                          <svg
                            width="13"
                            height="13"
                            viewBox="0 0 24 24"
                            fill="none"
                            stroke="currentColor"
                            stroke-width="1.75"
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            aria-hidden="true"
                          >
                            <rect x="5" y="3" width="14" height="18" rx="2" />
                            <path d="M9 3v18M9 8h6M9 12h6" /></svg
                          >Notebook
                        </button></template
                      ></ChatComposer
                    >
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
            @click="closePane"
          />
          <div v-if="paneMode !== 'closed'" class="task-rightpane">
            <RightPaneTabs
              :tabs="PANE_TABS"
              :active-id="activePane"
              :pane-mode="paneMode"
              @select="selectPane"
              @toggle-focus="paneMode = paneMode === 'focus' ? 'split' : 'focus'"
              @close-pane="closePane"
            />
            <div class="task-rightpane-card">
              <NotebookPane
                v-if="activePane === 'notebook' && chat"
                :key="`${workspaceId}:${projectId}:${chat.id}:notebook`"
                :project="project.data.value"
                :chat="chat"
                :owner="owner"
              />
              <ComputePane
                v-else-if="activePane === 'compute' && chat"
                :key="`${workspaceId}:${projectId}:${chat.id}:compute`"
                :project="project.data.value"
                :chat="chat"
                :owner="owner"
              />
              <FilesPane
                v-else-if="activePane === 'files' && activeFile"
                :key="`${workspaceId}:${projectId}:${activeFile?.id}`"
                :project="project.data.value"
                :file="activeFile"
              />
              <SidebarFiles
                v-else-if="activePane === 'files'"
                :files="files.data.value ?? []"
                :loading="files.isPending.value"
                :error="files.error.value?.message"
                @open="openFile"
              />
            </div>
          </div>
        </div>
      </div>
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
.coresearch-project-main {
  flex: 1;
  min-width: 0;
}
</style>
