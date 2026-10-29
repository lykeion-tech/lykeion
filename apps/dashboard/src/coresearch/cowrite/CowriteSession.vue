<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { useMe, useMembers } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { guardResourceDrafts } from '../../state/resource-draft';
import { useWorkspaceTabs } from '../../state/workspace-tabs';
import {
  useCoresearchChats,
  useCoresearchFiles,
  useCoresearchMembers,
  useCoresearchMessages,
  isActiveTurn,
  projectPath,
  chatPath,
  filePath,
} from '../api';
import {
  useAristotle,
  acceptanceRequest,
  type AcceptanceRequest,
  type ComposerAttachment,
  holdComposerContinuation,
  takeComposerContinuation,
} from '../aristotle';
import type { ProjectRow, FileRow, ChatRow } from '../types';
import ChatTranscript from '../ChatTranscript.vue';
import ChatComposer from '../ChatComposer.vue';
import AristotleComposer from '../AristotleComposer.vue';
import ComputePane from '../ComputePane.vue';
import RightPaneTabs from '../RightPaneTabs.vue';
import FilesPane from '../FilesPane.vue';
import MembersDialog from '../MembersDialog.vue';
import CowriteSidebar from './CowriteSidebar.vue';
import ManuscriptFiles from './ManuscriptFiles.vue';
import Outline from './Outline.vue';
import Editor from './Editor.vue';
import HomeChatDrawer from './HomeChatDrawer.vue';
import SelectionToolbar, { type SelectionTarget } from './SelectionToolbar.vue';
import { outlineOf, proofreadPrompt, compileManuscriptNotes } from './lib/manuscript';
import { docPath } from './api';
const props = defineProps<{ project: ProjectRow; chatId?: string }>();
const route = useRoute(),
  router = useRouter(),
  client = useQueryClient(),
  tabs = useWorkspaceTabs();
const me = useMe(),
  members = useMembers(),
  aristotle = useAristotle();
const chats = useCoresearchChats(() => props.project.id),
  files = useCoresearchFiles(() => props.project.id),
  projectMembers = useCoresearchMembers(() => props.project.id);
const chat = computed(
  () =>
    chats.data.value?.find((c) => c.id === props.chatId) ??
    (!props.chatId ? chats.data.value?.[0] : undefined),
);
const messages = useCoresearchMessages(
  () => props.project.id,
  () => chat.value?.id ?? '',
);
const owner = computed(() => !!me.data.value && chat.value?.owner_id === me.data.value.id);
const computeChat = computed(() =>
  owner.value ? chat.value : chats.data.value?.find((c) => c.owner_id === me.data.value?.id),
);
const conversation = computed(() =>
  aristotle.conversations.data.value?.find((c) => !c.shared && c.chat_id === chat.value?.id),
);
const chatTitles = computed(() =>
  Object.fromEntries(
    (aristotle.conversations.data.value ?? [])
      .filter((c) => c.project_id === props.project.id)
      .map((c) => [c.chat_id, c.title]),
  ),
);
const model = computed(() =>
  conversation.value && !conversation.value.shared ? (conversation.value.snapshot_model ?? '') : '',
);
const activeRun = computed(
  () =>
    messages.data.value?.find((m) => m.kind === 'turn' && isActiveTurn(m.status))?.agent_run_id ??
    undefined,
);
const fileID = ref(''),
  heldFile = ref<FileRow>(),
  text = ref(''),
  line = ref<number>(),
  selection = ref<SelectionTarget>(),
  error = ref(''),
  tab = ref<'files' | 'chats'>('files'),
  namingNew = ref(false),
  membersOpen = ref(false),
  compute = ref(false),
  drawerOpen = ref(true);
const editor = ref<InstanceType<typeof Editor>>(),
  drawer = ref<InstanceType<typeof HomeChatDrawer>>(),
  uploadInput = ref<HTMLInputElement>();
const file = computed(() => files.data.value?.find((f) => f.id === fileID.value) ?? heldFile.value);
const missing = computed(
  () => !!file.value && !!files.data.value && !files.data.value.some((f) => f.id === fileID.value),
);
const openedJournals = ref(new Set<string>());
const editable = computed(
  () =>
    !!file.value &&
    (file.value.has_doc ||
      openedJournals.value.has(file.value.id) ||
      /\.(tex|bib)$/i.test(file.value.path)),
);
const outline = computed(() =>
  file.value?.path.toLowerCase().endsWith('.tex') ? outlineOf(text.value) : [],
);
const people = computed(() =>
  (members.data.value ?? [])
    .filter((m) => projectMembers.data.value?.some((p) => p.user_id === m.user_id))
    .map((m) => ({ id: m.user_id, name: m.name?.trim() || m.email || m.user_id })),
);
const draft = ref(''),
  busy = ref(false),
  attachments = ref<ComposerAttachment[]>([]);
let pending: AcceptanceRequest | null = null;
let selectedRequest:
  | { body: string; selection: { file_id: string; version: number; from: number; to: number } }
  | undefined;
const pathFor = (path: string, chatID = chat.value?.id ?? '') =>
  router.resolve({
    path: `/coresearch/${props.project.id}/${chatID}`,
    query: path === 'main.tex' ? {} : { file: path },
  }).fullPath;
watch(
  () => route.query.file,
  () => {
    const path = typeof route.query.file === 'string' ? route.query.file : 'main.tex';
    const found = files.data.value?.find((f) => f.path === path);
    if (fileID.value !== (found?.id ?? '')) {
      text.value = '';
      line.value = undefined;
      selection.value = undefined;
    }
    fileID.value = found?.id ?? '';
    heldFile.value = found;
  },
  { immediate: true },
);
watch(
  files.data,
  (list) => {
    if (!list) return;
    if (!fileID.value) {
      const path = typeof route.query.file === 'string' ? route.query.file : 'main.tex';
      const found = list.find((f) => f.path === path);
      if (found) {
        fileID.value = found.id;
        heldFile.value = found;
      }
      return;
    }
    const current = list.find((f) => f.id === fileID.value);
    if (current) {
      const previous = heldFile.value;
      heldFile.value = current;
      if (previous && previous.path !== current.path) {
        const old = pathFor(previous.path),
          next = pathFor(current.path);
        for (const t of tabs.tabs.value) {
          if (t.path === old) {
            t.path = next;
            t.title = current.path;
            t.history = t.history.map((p) => (p === old ? next : p));
          }
        }
        void router.replace(next);
      }
    }
  },
  { deep: true },
);
watch(
  () => chat.value?.id,
  () => {
    draft.value = '';
    pending = null;
    selectedRequest = undefined;
    attachments.value = [];
    const held = takeComposerContinuation(
      aristotle.scope.value,
      route.fullPath,
      window.history.state?.position,
    );
    if (held) {
      draft.value = held.body;
      attachments.value = held.attachments;
      pending = held.pending;
      if (pending?.payload) {
        const options = JSON.parse(pending.payload);
        if (options.selection) selectedRequest = { body: held.body, selection: options.selection };
      }
    }
  },
  { immediate: true },
);
watch(messages.data, () => drawer.value?.scrollBottom());
async function invalidate() {
  await client.invalidateQueries({
    queryKey: ['ws', workspaceId.value, 'coresearch', props.project.id],
  });
}
async function openFile(path: string) {
  if (!(await guardResourceDrafts())) return;
  const target = pathFor(path);
  const existing = tabs.tabs.value.find((t) => t.path === target);
  if (existing) tabs.selected.value = existing.id;
  else tabs.openTab(target, path, 'file');
  await router.push(target);
}
async function openChat(id: string) {
  if (!(await guardResourceDrafts())) return;
  await router.push(pathFor(file.value?.path ?? 'main.tex', id));
}
function report(e: unknown) {
  error.value = e instanceof Error ? e.message : String(e);
}
async function newChat() {
  try {
    if (!(await guardResourceDrafts())) return;
    const cli = aristotle.agent.data.value?.harness ?? chat.value?.cli;
    if (cli !== 'claude' && cli !== 'codex')
      throw new Error('Choose a configured agent before starting a chat');
    const created = await api<ChatRow>('POST', `${projectPath(props.project.id)}/chats`, { cli });
    await invalidate();
    await openChat(created.id);
  } catch (e) {
    report(e);
  }
}
async function uploadFile(blob: File, path = blob.name) {
  const form = new FormData();
  form.set('path', path);
  form.set('base', String(files.data.value?.find((f) => f.path === path)?.head.n ?? 0));
  form.set('file', blob);
  await api('POST', `${projectPath(props.project.id)}/files`, form);
  await invalidate();
}
async function newFile(path: string) {
  try {
    await uploadFile(new File([''], path, { type: 'text/plain' }), path);
    await openFile(path);
  } catch (e) {
    report(e);
  }
}
async function upload(e: Event) {
  for (const blob of Array.from((e.target as HTMLInputElement).files ?? [])) {
    try {
      await uploadFile(blob);
    } catch (err) {
      error.value = `${blob.name}: ${err instanceof Error ? err.message : String(err)}`;
    }
  }
  (e.target as HTMLInputElement).value = '';
}
async function rename(from: string, to: string) {
  try {
    const f = files.data.value?.find((f) => f.path === from);
    if (f) {
      await api('PATCH', filePath(props.project.id, f.id), { path: to });
      await invalidate();
    }
  } catch (e) {
    report(e);
  }
}
async function remove(path: string) {
  if (!(await guardResourceDrafts())) throw new Error('The document could not be saved');
  const f = files.data.value?.find((f) => f.path === path);
  if (!f) return;
  await api('DELETE', filePath(props.project.id, f.id));
  if (fileID.value === f.id) {
    fileID.value = '';
    heldFile.value = undefined;
    await router.replace(pathFor('main.tex'));
  }
  await invalidate();
}
async function send(
  body = draft.value,
  selectionValue?: { file_id: string; version: number; from: number; to: number },
) {
  if (
    !owner.value ||
    busy.value ||
    activeRun.value ||
    props.project.archived_at ||
    !body.trim() ||
    !chat.value
  )
    return;
  if (selectionValue) selectedRequest = { body, selection: selectionValue };
  else if (selectedRequest?.body === body) selectionValue = selectedRequest.selection;
  else selectedRequest = undefined;
  busy.value = true;
  error.value = '';
  const workspace = workspaceId.value,
    project = props.project.id,
    id = chat.value.id;
  try {
    await editor.value?.flush();
    const options = {
      ...(attachments.value.length ? { attachments: attachments.value } : {}),
      ...(selectionValue ? { selection: selectionValue } : {}),
    };
    pending = acceptanceRequest(pending, body, id, JSON.stringify(options));
    await api(
      'POST',
      `${chatPath(project, id)}/turns`,
      { body, request_id: pending.request_id, ...options },
      undefined,
      { workspace },
    );
    draft.value = '';
    attachments.value = [];
    pending = null;
    selectedRequest = undefined;
    drawerOpen.value = true;
    await invalidate();
    drawer.value?.scrollBottom();
  } catch (e) {
    report(e);
  } finally {
    busy.value = false;
  }
}
async function selectionAction(kind: 'proofread' | 'edit', instruction = '') {
  if (!owner.value) return;
  drawerOpen.value = true;
  try {
    const at = await editor.value!.selection();
    const body =
      kind === 'proofread'
        ? proofreadPrompt(at.path, at.line, at.quote)
        : compileManuscriptNotes(
            [
              {
                id: 'now',
                anchor: { path: at.path, line: at.line, quote: at.quote, from: '', to: '' },
                kind: 'edit',
                text: instruction,
              },
            ],
            '',
          );
    draft.value = body;
    await send(body, { file_id: at.file_id, version: at.version, from: at.from, to: at.to });
  } catch (e) {
    report(e);
  }
}
async function comment(_target: SelectionTarget, body: string, mentions: string[]) {
  try {
    const at = await editor.value!.selection();
    await api('POST', `${docPath(props.project.id, at.file_id)}/comments`, {
      base: at.version,
      from: at.from,
      to: at.to,
      quote: at.quote,
      line: at.line,
      body,
      mentions,
    });
    await invalidate();
  } catch (e) {
    report(e);
  }
}
async function attach(blobs: File[]) {
  if (busy.value) return;
  busy.value = true;
  try {
    for (const blob of blobs) {
      try {
        const form = new FormData();
        form.set('file', blob);
        const result = await api<{ upload_id: string }>('POST', '/api/coresearch/uploads', form);
        attachments.value.push({ upload_id: result.upload_id, name: blob.name });
      } catch (e) {
        error.value = `${blob.name}: ${e instanceof Error ? e.message : String(e)}`;
      }
    }
  } finally {
    busy.value = false;
  }
}
async function stop() {
  if (!owner.value || !activeRun.value || !chat.value) return;
  try {
    await api(
      'POST',
      `${chatPath(props.project.id, chat.value.id)}/turns/${activeRun.value}/cancel`,
    );
    await invalidate();
  } catch (e) {
    report(e);
  }
}
async function manage(page: string) {
  if (!(await guardResourceDrafts())) return;
  holdComposerContinuation({
    scope: aristotle.scope.value,
    path: route.fullPath,
    position: window.history.state?.position,
    body: draft.value,
    mode: 'cowrite',
    template: 'blank',
    harness: chat.value?.cli ?? '',
    model: model.value,
    attachments: attachments.value,
    pending,
  });
  await router.push(`/settings?tab=${page}`);
}
async function build() {
  try {
    await editor.value?.flush();
    await api('POST', `${projectPath(props.project.id)}/builds`);
    await invalidate();
  } catch (e) {
    report(e);
  }
}
</script>
<template>
  <div class="task-screen coresearch-project-main">
    <Teleport to="#cowrite-context-rail" defer
      ><CowriteSidebar
        v-model:tab="tab"
        :name="project.name"
        :chats="chats.data.value ?? []"
        :active-chat-id="chat?.id"
        :chat-titles="chatTitles"
        :owner="project.role === 'owner'"
        :compute-active="compute"
        @compute="compute = !compute"
        @search="editor?.search()"
        @new-chat="newChat"
        @new-file="namingNew = true"
        @upload="uploadInput?.click()"
        @members="membersOpen = true"
        @open-chat="openChat"
        ><p v-if="files.error.value" class="tsb-files-error" role="alert">
          {{ files.error.value.message }}
        </p>
        <ManuscriptFiles
          :files="files.data.value ?? []"
          :active-path="file?.path"
          :naming-new="namingNew"
          :remove="remove"
          @open="openFile"
          @new-file="newFile"
          @naming-new-done="namingNew = false"
          @rename="rename" /><Outline
          :entries="outline"
          :at="line"
          @jump="
            line = $event;
            editor?.jump($event);
          " /></CowriteSidebar></Teleport
    ><input
      ref="uploadInput"
      type="file"
      multiple
      hidden
      aria-label="Upload manuscript files"
      @change="upload"
    />
    <div
      class="task-columns"
      :data-pane-mode="compute ? 'split' : 'closed'"
      style="--task-utility-width: 360px"
    >
      <div class="task-main" data-column="paper">
        <div class="task-stage-row" />
        <p v-if="error" class="tab-strip-error" role="alert">{{ error }}</p>
        <p v-if="missing" class="tab-strip-error" role="alert">This file is no longer available.</p>
        <div class="task-main-body">
          <div class="manuscript-pane">
            <div class="manuscript-reading">
              <Editor
                v-if="file && editable && me.data.value"
                :key="`${workspaceId}:${project.id}:${file.id}`"
                ref="editor"
                :project-id="project.id"
                :file-id="file.id"
                :path="file.path"
                :disabled="!!project.archived_at || missing"
                @ready="openedJournals.add(file.id)"
                @text="text = $event"
                @selection="selection = $event"
                @error="error = $event"
                @build="build"
              /><FilesPane
                v-else-if="file && !editable && !missing"
                :project="project"
                :file="file"
              />
              <p v-else class="ms-pane-empty">
                {{ files.isPending.value ? 'Loading files…' : 'This file is not available.' }}
              </p>
              <SelectionToolbar
                v-if="selection && !missing"
                :target="selection"
                :owner="owner && !project.archived_at"
                :people="people"
                @proofread="selectionAction('proofread')"
                @edit="(_target, instruction) => selectionAction('edit', instruction)"
                @comment="comment"
                @close="selection = undefined"
              /><HomeChatDrawer
                v-if="chat"
                ref="drawer"
                :project-id="project.id"
                :open="drawerOpen"
                @toggle="drawerOpen = !drawerOpen"
                @open="drawerOpen = true"
                ><ChatTranscript
                  :project-id="project.id"
                  :chat-id="chat.id"
                  :messages="messages.data.value ?? []" /><template #composer
                  ><AristotleComposer
                    v-if="owner"
                    v-model="draft"
                    mode="cowrite"
                    :busy="busy"
                    :blocker="project.archived_at ? 'Project is archived' : undefined"
                    :active-run="activeRun"
                    :selected-model="model"
                    :models="model ? [{ value: model, label: model }] : []"
                    :attachments="attachments"
                    @send="send()"
                    @stop="stop"
                    @files="attach"
                    @remove="attachments = attachments.filter((f) => f.upload_id !== $event)"
                    @manage="manage" /><ChatComposer
                    v-else
                    :project-id="project.id"
                    :chat-id="chat.id"
                    :owner="false"
                    :disabled="!!project.archived_at" /></template
              ></HomeChatDrawer>
            </div>
          </div>
        </div>
      </div>
      <div v-if="compute && chat" class="task-rightpane">
        <RightPaneTabs
          :tabs="[{ id: 'compute', label: 'Compute' }]"
          active-id="compute"
          pane-mode="split"
          @close-pane="compute = false"
        />
        <div class="task-rightpane-card">
          <ComputePane v-if="computeChat" :project="project" :chat="computeChat" :owner="true" />
        </div>
      </div>
    </div>
    <MembersDialog
      v-if="membersOpen"
      :project-id="project.id"
      :editable="project.role === 'owner'"
      @close="membersOpen = false"
    />
  </div>
</template>
