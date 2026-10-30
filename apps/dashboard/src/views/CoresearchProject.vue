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
import SidebarFiles from '../coresearch/SidebarFiles.vue';
import ChatTranscript from '../coresearch/ChatTranscript.vue';
import ChatComposer from '../coresearch/ChatComposer.vue';
import SurfacePane from '../components/SurfacePane.vue';
import NotebookPane from '../coresearch/NotebookPane.vue';
import ComputePane from '../coresearch/ComputePane.vue';
import FilesPane from '../coresearch/FilesPane.vue';
import '../coresearch/l2.css';
import CowriteSession from '../coresearch/cowrite/CowriteSession.vue';
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
const activePane = ref<string | null>(null),
  paneMode = ref<'closed' | 'split' | 'focus'>('closed'),
  activeFile = ref<FileRow>();
const stream = ref<HTMLElement>(),
  atBottom = ref(true);
watch(
  () => [workspaceId.value, props.projectId],
  () => {
    activeFile.value = undefined;
    activePane.value = null;
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
async function selectPane(id: string | null) {
  if (activePane.value === 'files' && !(await discardFile())) return;
  if (id === 'files') activeFile.value = undefined;
  activePane.value = id;
}
const experimentSurfaces = [
  { id: 'notebook', label: 'Notebook', icon: 'book' },
  { id: 'files', label: 'Files', icon: 'file' },
  { id: 'compute', label: 'Compute', icon: 'monitor' },
];
async function toggleSurfaces() {
  if (paneMode.value === 'closed') paneMode.value = 'split';
  else await closePane();
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
    <CowriteSession
      v-else-if="project.data.value.mode === 'cowrite'"
      :key="`${workspaceId}:${projectId}`"
      :project="project.data.value"
      :chat-id="chatId"
    />
    <template v-else>
      <div class="task-screen coresearch-project-main" :data-pane-mode="paneMode">
        <div class="task-columns experiment-surface-layout" :data-pane-mode="paneMode">
          <div class="task-main" data-column="conversation" :hidden="paneMode === 'focus'">
            <div class="task-stage-row">
              <button type="button" aria-label="Open experiment surfaces" @click="toggleSurfaces">
                ☰
              </button>
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
          <SurfacePane
            :open="paneMode !== 'closed'"
            :active="activePane"
            :focused="paneMode === 'focus'"
            :surfaces="experimentSurfaces"
            width-key="lykeion.experiment-pane-width"
            @update:open="closePane"
            @update:active="selectPane"
            @update:focused="paneMode = $event ? 'focus' : 'split'"
          >
            <template #default>
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
            </template>
          </SurfacePane>
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

<style scoped>
.experiment-surface-layout {
  display: flex !important;
  position: relative;
  min-width: 0;
}
.experiment-surface-layout > .task-main {
  min-width: 0;
  flex: 1;
}
.experiment-surface-layout > .task-main[hidden] {
  display: none !important;
}
.experiment-surface-layout :deep(.surface-pane) {
  min-height: 0;
}
.experiment-surface-layout :deep(.task-rightpane-card) {
  border: 0;
  border-radius: 0;
  height: 100%;
}
.experiment-toggle {
  margin-left: auto;
}
</style>
