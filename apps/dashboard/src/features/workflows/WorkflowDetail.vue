<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Icon from '../../ui/Icon.vue';
import RecordedMarkdown from '../../ui/RecordedMarkdown.vue';
import { useWorkflow, workflowFile } from '../../api/workflows';
import { domainLabel, stripComments } from '../../lib/workflows';
import { matchesQuery } from '../../lib/search';
import { useStudyComposer } from '../../state/study-composer';

const props = defineProps<{ slug: string }>();
const { openComposer } = useStudyComposer();
const workflow = useWorkflow(() => props.slug);
const w = computed(() => workflow.data.value);
const tabs = ['README', 'Instruction', 'Files'] as const;
const tab = ref<(typeof tabs)[number]>('README');
const filter = ref('');
const selected = ref('');
const preview = ref('');
const previewFailed = ref(false);
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
    selected.value = '';
    preview.value = '';
    previewFailed.value = false;
    open.value = [];
    filter.value = '';
  },
);
async function select(path: string) {
  selected.value = path;
  preview.value = '';
  previewFailed.value = false;
  const file = w.value?.files.find((f) => f.path === path);
  if (!file?.embedded) return;
  try {
    const text = await (await workflowFile(props.slug, path)).text();
    if (selected.value === path) preview.value = text;
  } catch {
    if (selected.value === path) previewFailed.value = true;
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
        <nav class="workflow-crumbs" aria-label="Breadcrumb">
          <RouterLink to="/workflows">Workflows</RouterLink> › {{ domainLabel(w.domain) }} ›
          {{ w.name }}
        </nav>
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
        <RecordedMarkdown :content="stripComments(w.readme)" />
      </div>
      <div v-else-if="tab === 'Instruction'" class="workflow-body" role="tabpanel">
        <RecordedMarkdown :content="stripComments(w.instruction)" />
      </div>
      <div v-else class="workflow-body workflow-files" role="tabpanel">
        <div class="workflow-tree">
          <p class="workflow-count">{{ w.files.length }} files</p>
          <label class="agent-search"
            ><Icon name="search" :size="14" /><input
              v-model="filter"
              placeholder="Search files..."
              aria-label="Search files"
          /></label>
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
          <RecordedMarkdown v-else-if="isMarkdown(chosen.path)" :content="stripComments(preview)" />
          <pre v-else>{{ preview }}</pre>
        </div>
      </div>
    </template>
  </section>
</template>
<style src="../agents/agent-detail.css" />
<style scoped>
.workflow-crumbs {
  padding: 12px var(--gutter) 0;
  color: var(--muted);
}
.workflow-body {
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
.workflow-preview pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
