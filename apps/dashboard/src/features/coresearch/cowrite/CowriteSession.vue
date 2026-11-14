<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueries, useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../../../api/client';
import { useMe, useMembers, useRuntimes } from '../../../api/queries';
import { workspaceId } from '../../../api/session';
import { guardResourceDrafts } from '../../../state/resource-draft';
import { useWorkspaceTabs } from '../../../state/workspace-tabs';
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
  wireCapabilities,
  type AcceptanceRequest,
  type ComposerAttachment,
  holdComposerContinuation,
  takeComposerContinuation,
} from '../aristotle';
import type { ProjectRow, FileRow, ChatRow } from '../types';
import ChatTranscript from '../ChatTranscript.vue';
import ChatComposer from '../ChatComposer.vue';
import AristotleComposer from '../AristotleComposer.vue';
import CowriteRightPane from './CowriteRightPane.vue';
import FilesPane from '../FilesPane.vue';
import MembersDialog from '../MembersDialog.vue';
import CowriteSidebar from './CowriteSidebar.vue';
import ManuscriptFiles from './ManuscriptFiles.vue';
import Outline from './Outline.vue';
import Editor from './Editor.vue';
import HomeChatDrawer from './HomeChatDrawer.vue';
import SelectionToolbar, { type SelectionTarget } from './SelectionToolbar.vue';
import MergeChangesRow, { type Proposal } from './MergeChangesRow.vue';
import TurnRail from '../TurnRail.vue';
import RailRow from '../RailRow.vue';
import {
  outlineOf,
  proofreadPrompt,
  compileManuscriptNotes,
  bibKeysOf,
  labelsOf,
  parseTexLog,
} from './lib/manuscript';
import { docPath, useBuilds, useVersions } from './api';
import { keys } from '../api';
import type { Doc, Comment, Change, Version } from './types';
const props = defineProps<{ project: ProjectRow; chatId?: string }>();
const route = useRoute(),
  router = useRouter(),
  client = useQueryClient(),
  tabs = useWorkspaceTabs();
const me = useMe(),
  members = useMembers(),
  runtimes = useRuntimes(),
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
const chatTimes = computed(() => {
  const last = messages.data.value?.at(-1)?.created_at;
  return chat.value && last ? { [chat.value.id]: last } : {};
});
const model = computed(() =>
  conversation.value && !conversation.value.shared ? (conversation.value.snapshot_model ?? '') : '',
);
// The label the owner's CLI reported for the session's model; the bare value
// only once no runtime of theirs lists it any more.
const sessionModels = computed(() => {
  if (!model.value) return [];
  const listed = (runtimes.data.value ?? [])
    .filter((r) => r.owner_id === chat.value?.owner_id && r.harness === chat.value?.cli)
    .flatMap((r) => r.models ?? [])
    .find((m) => m.value === model.value);
  return [listed ?? { value: model.value, label: model.value }];
});
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
  focused = ref(false),
  compiling = ref(false),
  refusedAfter = ref<number | null>(null),
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
const composerCapabilities = ref({ skills: [] as string[], connectors: [] as string[] });
const draft = ref(''),
  busy = ref(false),
  attachments = ref<ComposerAttachment[]>([]);
let pending: AcceptanceRequest | null = null;
type PinnedSelection = { file_id: string; version: number; from: number; to: number };
// A selection action's failed request, adopted by the empty composer so Send
// retries it unchanged.
let selectedRequest: { body: string; selection: PinnedSelection; queue?: boolean } | undefined;
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
    composerCapabilities.value = { skills: [], connectors: [] };
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
      composerCapabilities.value = held.capabilities ?? { skills: [], connectors: [] };
      pending = held.pending;
      if (pending?.payload) {
        const options = JSON.parse(pending.payload);
        if (options.selection)
          selectedRequest = {
            body: held.body,
            selection: options.selection,
            ...(options.queue ? { queue: true } : {}),
          };
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
  // A file opens inside the session's own tab, as a task's files do in L2.
  await router.push(pathFor(path));
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
// The composer cannot send while another turn is being accepted or is running.
const turnBlocked = computed(
  () =>
    !owner.value || busy.value || !!activeRun.value || !!props.project.archived_at || !chat.value,
);
async function send(capabilities?: { skills: string[]; connectors: string[] }) {
  const body = draft.value;
  if (turnBlocked.value || !body.trim() || !chat.value) return;
  if (selectedRequest?.body !== body) selectedRequest = undefined;
  const selectionValue = selectedRequest?.selection,
    queue = selectedRequest?.queue;
  busy.value = true;
  error.value = '';
  const workspace = workspaceId.value,
    project = props.project.id,
    id = chat.value.id;
  try {
    await editor.value?.flush();
    const options = {
      ...(capabilities && (capabilities.skills.length || capabilities.connectors.length)
        ? { capabilities: wireCapabilities(capabilities) }
        : {}),
      ...(attachments.value.length ? { attachments: attachments.value } : {}),
      ...(selectionValue ? { selection: selectionValue } : {}),
      ...(queue ? { queue: true } : {}),
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
// A selection action sends its own turn, queued behind any running one, and
// leaves the owner's draft alone.
async function selectionAction(kind: 'proofread' | 'edit', instruction = '') {
  if (!owner.value || !!props.project.archived_at || !chat.value) return;
  drawerOpen.value = true;
  const id = chat.value.id,
    workspace = workspaceId.value;
  let body = '',
    options: { selection: PinnedSelection; queue: true } | undefined,
    request: AcceptanceRequest | undefined;
  try {
    const at = await editor.value!.selection();
    body =
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
    options = {
      selection: { file_id: at.file_id, version: at.version, from: at.from, to: at.to },
      queue: true,
    };
    error.value = '';
    await editor.value?.flush();
    request = acceptanceRequest(null, body, id, JSON.stringify(options));
    await api(
      'POST',
      `${chatPath(props.project.id, id)}/turns`,
      { body, request_id: request.request_id, ...options },
      undefined,
      { workspace },
    );
    await invalidate();
    drawer.value?.scrollBottom();
  } catch (e) {
    report(e);
    if (
      request &&
      options &&
      !draft.value.trim() &&
      !attachments.value.length &&
      chat.value?.id === id
    ) {
      draft.value = body;
      pending = request;
      selectedRequest = { body, ...options };
    }
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
    capabilities: composerCapabilities.value,
    pending,
  });
  await router.push(`/settings?tab=${page}`);
}
const builds = useBuilds(() => props.project.id),
  versions = useVersions(() => props.project.id);
const paperFiles = computed(() =>
  (files.data.value ?? []).filter((f) => /\.(tex|bib)$/i.test(f.path)),
);
const paperDocs = useQueries({
  queries: computed(() =>
    paperFiles.value.map((f) => ({
      queryKey: keys.cowriteDoc(workspaceId.value, props.project.id, f.id),
      enabled: !!workspaceId.value,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        api<Doc>('GET', docPath(props.project.id, f.id), undefined, undefined, {
          signal,
          workspace: workspaceId.value,
        }),
      refetchInterval: 4000,
    })),
  ),
});
const paper = computed(() =>
  paperFiles.value.flatMap((f, i) => {
    const doc = paperDocs.value[i]?.data;
    if (f.id === fileID.value && editor.value) return [{ path: f.path, text: text.value }];
    return doc ? [{ path: f.path, text: doc.text }] : [];
  }),
);
const sources = computed(() =>
  paper.value.filter((f) => /\.tex$/i.test(f.path)).map((f) => f.text),
);
const bibliography = computed(() => [
  ...new Map(
    paper.value
      .filter((f) => /\.bib$/i.test(f.path))
      .flatMap((f) => bibKeysOf(f.text))
      .map((k) => [k.key, k]),
  ).values(),
]);
const labels = computed(() => labelsOf(paper.value));
const commentFiles = computed(() =>
  (files.data.value ?? []).filter((f) => f.has_doc || /\.(tex|bib)$/i.test(f.path)),
);
const commentQueries = useQueries({
  queries: computed(() =>
    commentFiles.value.map((f) => ({
      queryKey: keys.cowriteComments(workspaceId.value, props.project.id, f.id),
      enabled: !!workspaceId.value,
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        api<Comment[]>('GET', `${docPath(props.project.id, f.id)}/comments`, undefined, undefined, {
          signal,
          workspace: workspaceId.value,
        }),
    })),
  ),
});
const comments = computed(() =>
  commentFiles.value.flatMap((f, i) =>
    (Array.isArray(commentQueries.value[i]?.data) ? commentQueries.value[i]!.data! : []).map(
      (c) => ({ ...c, path: f.path }),
    ),
  ),
);
// The paper's tracked changes, every file at once, grouped as the proposals
// that wrote them. The text of each hunk is cut from the live document.
const changeQueries = useQueries({
  queries: computed(() =>
    commentFiles.value.flatMap((f) => [
      {
        queryKey: keys.cowriteChanges(workspaceId.value, props.project.id, f.id),
        enabled: !!workspaceId.value,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
          api<Change[]>('GET', `${docPath(props.project.id, f.id)}/changes`, undefined, undefined, {
            signal,
            workspace: workspaceId.value,
          }),
        refetchInterval: 4000,
      },
      {
        queryKey: keys.cowriteDoc(workspaceId.value, props.project.id, f.id),
        enabled: !!workspaceId.value,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
          api<Doc>('GET', docPath(props.project.id, f.id), undefined, undefined, {
            signal,
            workspace: workspaceId.value,
          }),
        refetchInterval: 4000,
      },
    ]),
  ),
});
const proposals = computed(() => {
  const held = new Map<string, Proposal & { at: string }>();
  commentFiles.value.forEach((f, i) => {
    const rows = changeQueries.value[2 * i]?.data as Change[] | undefined;
    const doc = changeQueries.value[2 * i + 1]?.data as Doc | undefined;
    for (const c of Array.isArray(rows) ? rows : []) {
      const p = held.get(c.proposal_id) ?? { id: c.proposal_id, files: [], at: c.created_at };
      held.set(c.proposal_id, p);
      let file = p.files.find((x) => x.path === f.path);
      if (!file) p.files.push((file = { path: f.path, hunks: [] }));
      file.hunks.push({
        id: c.id,
        status: c.status,
        oldText: c.removed_text,
        newText: doc?.text.slice(c.from_pos, c.to_pos) ?? '',
      });
    }
  });
  // ponytail: the API does not say which turn wrote a proposal, so cards with
  // something still owed follow the transcript instead of sitting on their turn.
  return [...held.values()]
    .filter((p) => p.files.some((f) => f.hunks.some((h) => h.status === 'pending')))
    .sort((a, b) => a.at.localeCompare(b.at));
});
async function settleHunk(id: string, verdict: 'keep' | 'undo') {
  try {
    await editor.value?.flush();
    await api(
      'POST',
      `${projectPath(props.project.id)}/changes/${encodeURIComponent(id)}/${verdict}`,
    );
    await invalidate();
    await editor.value?.refresh();
  } catch (e) {
    report(e);
  }
}
const latestBuild = computed(() => [...(builds.data.value ?? [])].sort((a, b) => b.seq - a.seq)[0]);
const log = ref<string>(),
  logFailed = ref(false);
watch(
  () => [workspaceId.value, props.project.id, latestBuild.value?.id, latestBuild.value?.has_log],
  async (_, __, cleanup) => {
    const controller = new AbortController();
    cleanup(() => controller.abort());
    const b = latestBuild.value,
      workspace = workspaceId.value,
      p = props.project.id;
    log.value = undefined;
    logFailed.value = false;
    if (!b?.has_log) return;
    try {
      const blob = await api<Blob>(
        'GET',
        `${projectPath(p)}/builds/${b.seq}.log`,
        undefined,
        undefined,
        { workspace, signal: controller.signal, response: 'blob' },
      );
      const value = await blob.text();
      if (!controller.signal.aborted) log.value = value;
    } catch {
      if (!controller.signal.aborted) logFailed.value = true;
    }
  },
  { immediate: true },
);
const texErrors = computed(() => parseTexLog(log.value ?? '').errors);
async function jumpSource(path: string, at: number) {
  const match = (files.data.value ?? [])
    .filter((f) => path === f.path || path.endsWith(`/${f.path}`))
    .sort((a, b) => b.path.length - a.path.length)[0];
  if (!match) return;
  if (match.id !== fileID.value) {
    await openFile(match.path);
    if (fileID.value !== match.id) return;
    await nextTick();
  }
  line.value = at;
  editor.value?.jump(at);
}
async function restore(version: Version) {
  try {
    await editor.value?.flush();
    await api('POST', `${projectPath(props.project.id)}/versions/${version.seq}/restore`);
    await invalidate();
    await editor.value?.refresh();
  } catch (e) {
    report(e);
  }
}
async function deleteComment(id: string) {
  try {
    await api('DELETE', `${projectPath(props.project.id)}/comments/${encodeURIComponent(id)}`);
    await invalidate();
  } catch (e) {
    report(e);
  }
}
// A refusal for want of a machine holds until a build newer than it lands,
// whoever's machine ran that build.
const newestBuild = () => Math.max(0, ...(builds.data.value ?? []).map((b) => b.seq));
const noMachine = computed(
  () => refusedAfter.value !== null && newestBuild() <= refusedAfter.value,
);
async function build() {
  if (compiling.value || props.project.archived_at) return;
  compiling.value = true;
  try {
    await editor.value?.flush();
    await api('POST', `${projectPath(props.project.id)}/builds`);
    refusedAfter.value = null;
    await invalidate();
  } catch (e) {
    if (e instanceof ApiError && e.body.code === 'no_machine') refusedAfter.value = newestBuild();
    else report(e);
  } finally {
    compiling.value = false;
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
        :chat-times="chatTimes"
        :owner="project.role === 'owner'"
        :members="people"
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
      :data-pane-mode="focused ? 'focus' : 'split'"
      style="--task-utility-width: 50%"
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
                :bib-keys="bibliography"
                :labels="labels"
                :errors="texErrors"
                @cursor="line = $event"
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
                  :messages="messages.data.value ?? []" /><TurnRail
                  v-for="proposal in proposals"
                  :key="proposal.id"
                  ><RailRow marker="ok"
                    ><MergeChangesRow
                      :proposal="proposal"
                      @settle="settleHunk" /></RailRow></TurnRail
                ><template #composer
                  ><AristotleComposer
                    v-if="owner"
                    v-model="draft"
                    mode="cowrite"
                    :busy="busy"
                    :blocker="project.archived_at ? 'Project is archived' : undefined"
                    :active-run="activeRun"
                    :selected-model="model"
                    :models="sessionModels"
                    :attachments="attachments"
                    :initial-capabilities="composerCapabilities"
                    @capabilities="composerCapabilities = $event"
                    @send="send($event)"
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
      <CowriteRightPane
        :key="`${workspaceId}:${project.id}:${me.data.value?.id}`"
        :project-id="project.id"
        :title="project.name"
        :builds="builds.data.value ?? []"
        :sources="sources"
        :versions="versions.data.value ?? []"
        :comments="comments"
        :people="people"
        :me-id="me.data.value?.id"
        :disabled="!!project.archived_at"
        :no-machine="noMachine"
        :log="log"
        :log-failed="logFailed"
        :errors="log === undefined ? undefined : texErrors.length"
        :cursor="file && editable ? { path: file.path, line: line ?? 1 } : undefined"
        :restore="restore"
        @compile="build"
        @undo="editor?.undo()"
        @redo="editor?.redo()"
        @go-to-source="jumpSource"
        @delete-comment="deleteComment"
        @focus="focused = $event"
      />
    </div>
    <MembersDialog
      v-if="membersOpen"
      :project-id="project.id"
      :editable="project.role === 'owner'"
      @close="membersOpen = false"
    />
  </div>
</template>
