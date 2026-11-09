<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Icon from '../../ui/Icon.vue';
import { lazyRecordedMarkdown } from '../../ui/lazy-recorded-markdown';
import ArtifactViewer from '../coresearch/viewers/ArtifactViewer.vue';
import { useWorkflow, workflowFile } from '../../api/workflows';
import { domainLabel, stripComments } from '../../lib/workflows';
import { matchesQuery } from '../../lib/search';
import { useStudyComposer } from '../../state/study-composer';

const RecordedMarkdown = lazyRecordedMarkdown();

const props = defineProps<{ slug: string }>();
const { openComposer } = useStudyComposer();
const workflow = useWorkflow(() => props.slug);
const w = computed(() => workflow.data.value);
const tabs = ['README', 'Instruction', 'Files'] as const;
const tab = ref<(typeof tabs)[number]>('README');
const filter = ref('');
const selected = ref('');
const preview = ref('');
const previewBlob = ref<Blob>();
const previewLoading = ref(false);
const previewFailed = ref(false);
let previewRequest = 0;
const open = ref<string[]>([]);
// Top-level folders first, each holding its files; loose files follow.
const tree = computed(() => {
  const matched = (w.value?.files ?? [])
    .filter((f) => matchesQuery(f.path, filter.value))
    .sort((a, b) => a.path.localeCompare(b.path));
  const folders = new Map<string, typeof matched>();
  const loose: typeof matched = [];
  for (const f of matched) {
    const slash = f.path.indexOf('/');
    if (slash < 0) loose.push(f);
    else folders.set(f.path.slice(0, slash), [...(folders.get(f.path.slice(0, slash)) ?? []), f]);
  }
  return { folders: [...folders], loose };
});
const expanded = (name: string) => !!filter.value || open.value.includes(name);
const toggle = (name: string) =>
  (open.value = open.value.includes(name)
    ? open.value.filter((x) => x !== name)
    : [...open.value, name]);
const chosen = computed(() => w.value?.files.find((f) => f.path === selected.value));
const hours = (n: number) => Math.round((n / 3600) * 10) / 10;
const rows = computed(() => {
  const x = w.value;
  if (!x) return [];
  return [
    ['Authors', x.authors.map((a) => `${a.name} (${a.organization})`).join(', ')],
    ['Domain', [domainLabel(x.domain), x.field, x.subfield].filter(Boolean).join(' / ')],
    ['Tags', x.tags.join(', ')],
    ['Expert time', `${x.expert_hours} hours`],
    ['Agent budget', `${hours(x.agent_budget_sec)} hours`],
    ['Resources', `${x.cpus} CPUs · ${x.memory_mb / 1024} GB RAM`],
    ['Works', 'Experimentation · one agent · scored by Jev'],
  ];
});

watch(
  () => props.slug,
  () => {
    previewRequest++;
    selected.value = '';
    preview.value = '';
    previewBlob.value = undefined;
    previewLoading.value = false;
    previewFailed.value = false;
    open.value = [];
    filter.value = '';
  },
);
async function select(path: string) {
  const request = ++previewRequest;
  selected.value = path;
  preview.value = '';
  previewBlob.value = undefined;
  previewLoading.value = false;
  previewFailed.value = false;
  const file = w.value?.files.find((f) => f.path === path);
  if (!file?.embedded) return;
  previewLoading.value = true;
  try {
    const blob = await workflowFile(props.slug, path);
    const text = isMarkdown(path) ? await blob.text() : '';
    if (request !== previewRequest) return;
    preview.value = text;
    previewBlob.value = blob;
  } catch {
    if (request === previewRequest) previewFailed.value = true;
  } finally {
    if (request === previewRequest) previewLoading.value = false;
  }
}
const isMarkdown = (path: string) => /\.(md|markdown)$/i.test(path);
const unavailable = computed(
  () => !!chosen.value && (!chosen.value.embedded || previewFailed.value),
);
</script>
<template>
  <section class="agent-page workflow-detail">
    <p v-if="workflow.error.value" class="inline-error" role="alert">
      {{ workflow.error.value.message }}
    </p>
    <template v-if="w">
      <div class="agent-page-header">
        <header class="agent-hero">
          <div class="agent-identity">
            <div class="team-heading">
              <h1>{{ w.name }}</h1>
            </div>
            <div class="agent-actions">
              <button
                class="button primary"
                type="button"
                @click="openComposer(undefined, undefined, slug)"
              >
                <Icon name="plus" :size="14" />Start study with this workflow
              </button>
            </div>
          </div>
        </header>
        <div class="agent-tabs-bar" role="tablist" aria-label="Workflow sections">
          <button v-for="t in tabs" :key="t" role="tab" :aria-selected="tab === t" @click="tab = t">
            {{ t }}
          </button>
        </div>
      </div>
      <div v-if="tab === 'README'" class="workflow-body" role="tabpanel">
        <p>{{ w.description }}</p>
        <table class="workflow-facts" aria-label="Workflow facts">
          <tbody>
            <tr v-for="[label, value] in rows" :key="label">
              <th scope="row">{{ label }}</th>
              <td>{{ value }}</td>
            </tr>
          </tbody>
        </table>
        <RecordedMarkdown :content="stripComments(w.readme)" :breaks="false" />
      </div>
      <div v-else-if="tab === 'Instruction'" class="workflow-body" role="tabpanel">
        <RecordedMarkdown :content="stripComments(w.instruction)" :breaks="false" />
      </div>
      <div v-else class="workflow-body workflow-files" role="tabpanel">
        <div class="workflow-tree">
          <div class="workflow-tree-toolbar">
            <p class="workflow-count">{{ w.files.length }} files</p>
            <label class="agent-search"
              ><Icon name="search" :size="14" /><input
                v-model="filter"
                placeholder="Search files..."
                aria-label="Search files"
            /></label>
          </div>
          <ul>
            <li v-for="[name, list] in tree.folders" :key="name">
              <button type="button" :aria-expanded="expanded(name)" @click="toggle(name)">
                {{ expanded(name) ? '▾' : '▸' }} {{ name }}
              </button>
              <ul v-if="expanded(name)" class="workflow-nested">
                <li v-for="f in list" :key="f.path">
                  <button
                    type="button"
                    :aria-current="selected === f.path ? 'true' : undefined"
                    @click="select(f.path)"
                  >
                    {{ f.path.slice(name.length + 1) }}
                  </button>
                </li>
              </ul>
            </li>
            <li v-for="f in tree.loose" :key="f.path">
              <button
                type="button"
                :aria-current="selected === f.path ? 'true' : undefined"
                @click="select(f.path)"
              >
                {{ f.path }}
              </button>
            </li>
          </ul>
        </div>
        <div class="workflow-preview">
          <p v-if="!chosen" class="workflow-count">Select a file to preview it.</p>
          <p v-else-if="unavailable">Preview unavailable ({{ chosen.size }} bytes)</p>
          <p v-else-if="previewLoading">Reading…</p>
          <RecordedMarkdown
            v-else-if="isMarkdown(chosen.path)"
            :content="stripComments(preview)"
            :breaks="false"
          />
          <ArtifactViewer v-else-if="previewBlob" :blob="previewBlob" :path="chosen.path" />
        </div>
      </div>
    </template>
  </section>
</template>
<style src="../agents/agent-detail.css" />
<style scoped>
.workflow-detail {
  --gutter: 16px;
  scrollbar-gutter: auto;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.workflow-detail .team-heading {
  flex: 1;
  min-width: 0;
}
.workflow-detail .agent-identity > .agent-actions {
  flex: none;
}
.workflow-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 20px var(--gutter);
}
.workflow-facts {
  border-collapse: collapse;
  margin: 12px 0 20px;
}
.workflow-facts th,
.workflow-facts td {
  padding: 6px 16px 6px 0;
  text-align: left;
  vertical-align: top;
  border-bottom: 1px solid var(--border);
}
.workflow-facts th {
  color: var(--muted);
  font-weight: 500;
}
.workflow-files {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  gap: 20px;
}
.workflow-tree {
  width: 224px;
  max-width: 100%;
}
.workflow-tree-toolbar {
  display: grid;
  justify-items: stretch;
  width: 224px;
  max-width: 100%;
  gap: 8px;
}
.workflow-tree-toolbar .agent-search {
  min-width: 0;
  width: 100%;
}
.workflow-tree-toolbar .workflow-count {
  margin: 0;
  text-align: right;
}
.workflow-tree ul {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
}
.workflow-tree button {
  width: 100%;
  padding: 4px 6px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.workflow-tree button[aria-current='true'],
.workflow-tree button:hover {
  background: var(--fill-card);
}
.workflow-nested {
  padding-left: 16px !important;
}
.workflow-count {
  color: var(--muted);
  margin: 0 0 8px;
}
.workflow-preview {
  min-width: 0;
}
.workflow-preview :deep(.viz-figure) {
  margin: 0;
}
.workflow-preview :deep(.table-scroll),
.workflow-preview :deep(.image-stage),
.workflow-preview :deep(.code-block-pre) {
  overflow: auto;
}
.workflow-preview :deep(.table-scroll) {
  border: 1px solid var(--border);
  border-radius: 6px;
}
.workflow-preview :deep(.data-table) {
  border-collapse: collapse;
  width: 100%;
}
.workflow-preview :deep(.data-table th),
.workflow-preview :deep(.data-table td) {
  padding: 5px 10px;
  border-bottom: 1px solid var(--border);
  text-align: left;
  white-space: nowrap;
}
.workflow-preview :deep(.col-type) {
  margin-right: 6px;
  color: var(--muted);
}
.workflow-preview :deep(.image-controls) {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.workflow-preview :deep(.image-stage) {
  display: flex;
  justify-content: center;
  max-height: 70vh;
}
.workflow-preview :deep(.image-el:not(.is-zoomed)) {
  max-width: 100%;
  object-fit: contain;
}
.workflow-preview :deep(.code-block-pre) {
  white-space: pre;
}
</style>
