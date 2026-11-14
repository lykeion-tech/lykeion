<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { api, downloadFile, PreviewTooLargeError } from '../../api/client';
import { useKindRecords } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { formatSize } from '../../lib/attachments';
import { columnName, roleLabel } from '../../lib/studies';
import { agentNameWithoutCli } from '../../lib/agent-name';
import type { AgentRun, Card, StudyRecord } from '../../api/types';
import ArtifactViewer from '../coresearch/viewers/ArtifactViewer.vue';
import StudyFileThumbnail from './StudyFileThumbnail.vue';
import Icon from '../../ui/Icon.vue';

const props = withDefaults(defineProps<{ cardId: string; attachments?: Card['attachments']; runs?: AgentRun[] }>(), {
  attachments: () => [],
  runs: () => [],
});
const outputs = useKindRecords(() => props.cardId, 'output');
type Entry = {
  id: string;
  name: string;
  size?: number;
  date?: string;
  provenance?: string;
  group: string;
  url: string;
};
const entries = computed<Entry[]>(() => [
  ...props.attachments.map((a) => ({
    id: `attachment:${a.id}`,
    name: a.name,
    size: a.size_bytes,
    group: 'Your uploads',
    url: `/api/cards/${encodeURIComponent(props.cardId)}/attachments/${encodeURIComponent(a.id)}`,
  })),
  ...(outputs.data.value ?? []).map((r: StudyRecord) => ({
    id: `output:${r.id}`,
    name: typeof r.body.name === 'string' ? r.body.name : r.key,
    size: typeof r.body.size_bytes === 'number' ? r.body.size_bytes : undefined,
    date: r.created_at,
    provenance: (() => {
      const run = props.runs.find((item) => item.id === r.agent_run_id);
      return run ? agentNameWithoutCli(run.agent_name || roleLabel(run.role), run.harness) : roleLabel(r.role);
    })(),
    group: `${columnName(r.board_column)} · Round ${r.round}`,
    url: `/api/cards/${encodeURIComponent(props.cardId)}/outputs/${encodeURIComponent(r.key)}`,
  })),
]);
const query = ref('');
const layout = ref<'grid' | 'list'>('grid');
const visible = computed(() =>
  entries.value.filter((entry) =>
    entry.name.toLowerCase().includes(query.value.trim().toLowerCase()),
  ),
);
const groups = computed(() =>
  [...new Set(visible.value.map((entry) => entry.group))].map((name) => ({
    name,
    files: visible.value.filter((entry) => entry.group === name),
  })),
);
const selected = ref<Entry>();
const blob = ref<Blob>();
const loading = ref(false);
const error = ref('');
const exceedsLimit = ref(false);
const previewLimit = 8 * 1024 * 1024;
const downloadOnly = computed(
  () =>
    !!selected.value &&
    (exceedsLimit.value || selected.value.size === undefined || selected.value.size > previewLimit),
);
let generation = 0;
let request: AbortController | undefined;
watch(
  [workspaceId, () => props.cardId],
  () => {
    generation++;
    request?.abort();
    selected.value = undefined;
    blob.value = undefined;
    loading.value = false;
    error.value = '';
    exceedsLimit.value = false;
  },
  { flush: 'sync' },
);
onUnmounted(() => {
  generation++;
  request?.abort();
});
async function open(entry: Entry) {
  request?.abort();
  request = new AbortController();
  const signal = request.signal;
  const workspace = workspaceId.value,
    card = props.cardId,
    visit = ++generation;
  selected.value = entry;
  blob.value = undefined;
  error.value = '';
  exceedsLimit.value = false;
  if (entry.size === undefined || entry.size > previewLimit) {
    loading.value = false;
    return;
  }
  loading.value = true;
  const current = () =>
    generation === visit && workspaceId.value === workspace && props.cardId === card;
  try {
    const value = await api<Blob>('GET', entry.url, undefined, undefined, {
      workspace,
      response: 'blob',
      signal,
      maxBytes: previewLimit,
    });
    if (current()) blob.value = value;
  } catch (e) {
    if (current()) {
      if (e instanceof PreviewTooLargeError) exceedsLimit.value = true;
      else error.value = (e as Error).message;
    }
  } finally {
    if (current()) loading.value = false;
  }
}
function back() {
  generation++;
  request?.abort();
  selected.value = undefined;
  blob.value = undefined;
  loading.value = false;
}
function openAttachment(id: string) {
  const entry = entries.value.find((item) => item.id === `attachment:${id}`);
  if (entry) void open(entry);
}
function openOutput(key: string) {
  const record = outputs.data.value?.find((item) => item.key === key);
  const entry = entries.value.find((item) => item.id === `output:${record?.id}`);
  if (entry) void open(entry);
}
defineExpose({ back, openAttachment, openOutput, hasPreview: computed(() => !!selected.value) });
function download() {
  const entry = selected.value;
  if (!entry || loading.value) return;
  downloadFile(entry.url, workspaceId.value);
}
</script>
<template>
  <section class="study-outputs" aria-label="Files">
    <template v-if="selected">
      <header class="preview-header">
        <strong>{{ selected.name }}</strong>
        <button type="button" :disabled="loading || (!blob && !downloadOnly)" @click="download">
          Download
        </button>
      </header>
      <p v-if="loading" role="status">Loading file…</p>
      <p v-else-if="error" role="alert">
        {{ error }} <button type="button" @click="open(selected)">Retry</button>
      </p>
      <p v-else-if="downloadOnly" class="empty">
        {{
          selected.size === undefined
            ? 'File size is unavailable; download to open it.'
            : 'This file is too large to preview. Download to open it.'
        }}
      </p>
      <ArtifactViewer v-else-if="blob" :blob="blob" :path="selected.name" />
    </template>
    <template v-else>
      <h2>Files</h2>
      <div class="files-toolbar">
        <input
          v-model="query"
          type="search"
          aria-label="Search files"
          placeholder="Search files…"
        />
        <button
          type="button"
          :aria-pressed="layout === 'grid'"
          aria-label="Grid view"
          @click="layout = 'grid'"
        >
          <Icon name="board" :size="14" />
        </button>
        <button
          type="button"
          :aria-pressed="layout === 'list'"
          aria-label="List view"
          @click="layout = 'list'"
        >
          <Icon name="list" :size="14" />
        </button>
      </div>
      <p v-if="outputs.isPending.value && !entries.length" role="status">Loading files…</p>
      <p v-else-if="outputs.isError.value" role="alert">
        {{ outputs.error.value?.message }} <button @click="outputs.refetch()">Retry</button>
      </p>
      <div v-else-if="!entries.length" class="no-files">
        <Icon name="file" :size="30" />
        <h2>No files yet</h2>
        <p>Files saved by agents will appear here.</p>
      </div>
      <p v-else-if="!visible.length" class="empty">No files match.</p>
      <section v-for="group in groups" :key="group.name" class="file-group">
        <h3>
          {{ group.name }} <span>{{ group.files.length }}</span>
        </h3>
        <ul :class="layout">
          <li v-for="entry in group.files" :key="entry.id">
            <button type="button" class="file-card" @click="open(entry)">
              <StudyFileThumbnail
                :key="`${workspaceId}:${cardId}:${entry.id}`"
                :name="entry.name"
                :size="entry.size"
                :url="entry.url"
              />
              <span class="file-details"
                ><strong>{{ entry.name }}</strong
                ><small v-if="entry.provenance">{{ entry.provenance }}</small
                ><small
                  ><template v-if="entry.date">{{ new Date(entry.date).toLocaleString() }}</template
                  ><template v-if="entry.size !== undefined"
                    >{{ entry.date ? ' · ' : '' }}{{ formatSize(entry.size) }}</template
                  ></small
                ></span
              >
            </button>
          </li>
        </ul>
      </section>
    </template>
  </section>
</template>
<style scoped>
.no-files {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  padding: 20px;
  text-align: center;
  color: var(--muted);
  font-size: 14px;
}
.no-files h2 {
  margin: 0;
  color: var(--text);
  font-size: 18px;
  font-weight: 500;
}
.no-files p {
  margin: 0;
}
.study-outputs {
  display: flex;
  flex: 1;
  flex-direction: column;
  padding: 24px 20px;
  min-width: 0;
}
h2 {
  font-size: 16px;
  margin: 0 0 16px;
}
.empty,
small {
  color: var(--muted);
}
.files-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 13px;
}
.files-toolbar input {
  min-width: 0;
  flex: 1;
  border: 0;
  background: transparent;
  color: var(--text);
}
.files-toolbar input:focus {
  outline: none;
}
.files-toolbar input:focus-visible {
  box-shadow: 0 2px 0 var(--focus-ring);
}
.files-toolbar button,
.preview-header button {
  cursor: pointer;
  color: inherit;
}
.files-toolbar button[aria-pressed='true'] {
  background: var(--fill-control);
}
.file-group h3 {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  margin: 20px 0 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--border);
}
.file-group h3 span {
  color: var(--muted);
  font-weight: normal;
}
ul {
  list-style: none;
  padding: 0;
  margin: 0;
}
ul.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 180px), 1fr));
  gap: 12px;
}
ul.list {
  display: grid;
  gap: 8px;
}
.file-card {
  display: flex;
  flex-direction: column;
  padding: 0;
  width: 100%;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--surface);
  color: var(--text);
  text-align: left;
  cursor: pointer;
}
.file-card:hover {
  background: var(--fill-control);
}
.file-details {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  width: 100%;
  padding: 10px;
  box-sizing: border-box;
}
.file-details strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 500;
}
.file-details small {
  font-size: 13px;
}
.list .file-card {
  flex-direction: row;
  align-items: center;
}
.list :deep(.thumbnail) {
  width: 60px;
  height: 52px;
  flex: 0 0 60px;
  border-bottom: 0;
  border-right: 1px solid var(--border);
}
.preview-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.preview-header strong {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 13px;
}
.preview-header button {
  color: var(--primary);
}
</style>
