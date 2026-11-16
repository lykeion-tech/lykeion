<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue';
import type { StudyReport } from '../../api/types';
import { lazyRecordedMarkdown } from '../../ui/lazy-recorded-markdown';

const RecordedMarkdown = lazyRecordedMarkdown();
import {
  kindName,
  overviewOf,
  sectionFor,
  statusOf,
  tabRows,
  verdictOf,
  type Status,
  type TabId,
} from './report-sections';

const props = defineProps<{ report: StudyReport; selected: string | null; graphSelection?: number }>();
const emit = defineEmits<{ select: [id: string | null]; hover: [id: string | null] }>();

const section = computed(() =>
  props.selected && props.report.nodes.some((n) => n.id === props.selected)
    ? sectionFor(props.report, props.selected)
    : null,
);
const overview = computed(() => {
  const o = overviewOf(props.report);
  return [
    { status: 'supported', label: 'Findings supported', n: o.supported },
    { status: 'caveated', label: 'Findings supported with caveats', n: o.caveated },
    { status: 'refuted', label: 'Findings refuted', n: o.refuted },
    { status: 'inconclusive', label: 'Findings inconclusive', n: o.inconclusive },
    { status: 'invalid', label: 'Findings invalid', n: o.invalid },
    { status: 'rejected', label: 'Hypotheses rejected', n: o.rejected },
    { status: '', label: 'Objections', n: o.objections },
    { status: '', label: 'Open doubts', n: o.doubts },
  ].filter((row) => row.n > 0);
});

const tab = ref<TabId>('summary');
watch(
  () => props.graphSelection,
  (selection) => {
    if (!selection) return;
    const kind = props.report.nodes.find((node) => node.id === props.selected)?.kind;
    tab.value = kind === 'finding' || kind === 'hypothesis' ? 'findings'
      : kind === 'objection' || kind === 'doubt' ? 'concerns'
        : kind === 'paper' || kind === 'dataset' || kind === 'output' ? 'sources'
          : 'summary';
  },
  { immediate: true },
);
const rows = computed(() => tabRows(props.report, section.value ? props.selected : null));
const tabs = computed<{ id: TabId; label: string; n?: number }[]>(() => [
  { id: 'summary', label: 'Summary' },
  { id: 'findings', label: 'Findings', n: rows.value.findings.length },
  { id: 'concerns', label: 'Concerns', n: rows.value.concerns.length },
  { id: 'sources', label: 'Sources', n: rows.value.sources.length },
]);
function tabKey(e: KeyboardEvent) {
  const step =
    e.key === 'ArrowRight'
      ? 1
      : e.key === 'ArrowLeft'
        ? -1
        : e.key === 'ArrowDown'
          ? 2
          : e.key === 'ArrowUp'
            ? -2
            : 0;
  if (!step) return;
  e.preventDefault();
  const at = tabs.value.findIndex((t) => t.id === tab.value);
  const next = tabs.value[(at + step + tabs.value.length) % tabs.value.length]!;
  tab.value = next.id;
  void nextTick(() => document.getElementById(`report-tab-${next.id}`)?.focus());
}
const handInExpanded = ref(false);
watch(
  () => props.report.card_id,
  () => {
    handInExpanded.value = false;
  },
);
const excerpt = computed(() => {
  const text = (props.report.hand_in?.text ?? '')
    .replace(/[#*_`>[\]]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
  return text.length > 240 ? `${text.slice(0, 240)}…` : text;
});

const colors: Record<Status['icon'], string> = {
  '✓': '#278241',
  '◐': '#c69c25',
  '○': '#d5213d',
  '': 'var(--muted)',
};
const colorOf = (status: string) => colors[statusOf(status).icon];

// Focus follows a selection made in the pane; graph selections keep focus there.
let fromPane = false;
const openFolds = reactive(new Set<string>());
const openRows = reactive(new Set<string>());
const panelEl = ref<HTMLElement | null>(null);
watch(
  () => props.selected,
  () => {
    openFolds.clear();
    openRows.clear();
    if (fromPane) void nextTick(() => panelEl.value?.focus());
    fromPane = false;
  },
);
function go(id: string | null) {
  tab.value = 'summary';
  fromPane = true;
  emit('select', id);
  void nextTick(() => (fromPane = false));
}
const toggle = (set: Set<string>, key: string) => (set.has(key) ? set.delete(key) : set.add(key));
</script>

<template>
  <section class="report-pane type-body" aria-label="Report section">
    <header class="report-review-header">
      <h2>Report</h2>
      <p>Explore the evidence behind this study.</p>
    </header>
    <!-- Focus may stay in the graph, so the newly shown section is announced. -->
    <p class="sr-only" role="status">
      {{ section ? `${kindName(section.node.kind)}: ${section.node.label}` : 'Overview' }}
    </p>
    <div class="pane-tabs" role="tablist" aria-label="Report sections" @keydown="tabKey">
      <button
        v-for="t in tabs"
        :id="`report-tab-${t.id}`"
        :key="t.id"
        role="tab"
        class="pane-tab"
        :aria-selected="tab === t.id"
        :aria-controls="`report-panel-${t.id}`"
        :tabindex="tab === t.id ? 0 : -1"
        @click="tab = t.id"
      >
        <span class="tab-heading"
          >{{ t.label }}<b v-if="t.n !== undefined" class="tab-count">{{ t.n }}</b></span
        >
        <span class="tab-caption">{{
          t.id === 'summary'
            ? 'Overview'
            : t.id === 'findings'
              ? 'Results'
              : t.id === 'concerns'
                ? 'Open questions'
                : 'Evidence'
        }}</span>
      </button>
    </div>
    <section
      :id="`report-panel-${tab}`"
      ref="panelEl"
      role="tabpanel"
      tabindex="-1"
      class="pane-panel"
      :aria-labelledby="`report-tab-${tab}`"
    >
      <template v-if="tab === 'summary' && !section">
        <div class="overview-card">
          <span class="overview-eyebrow">Study overview</span>
          <p class="verdict">{{ verdictOf(report) }}</p>
          <ul v-if="overview.length" class="overview-counts">
            <li v-for="row in overview" :key="row.label">
              <span :style="{ color: colorOf(row.status) }" aria-hidden="true">{{
                statusOf(row.status).icon
              }}</span>
              <span>{{ row.label }}</span
              ><b>{{ row.n }}</b>
            </li>
          </ul>
        </div>
        <section v-if="excerpt" class="review-fold hand-in-preview">
          <h3>Hand-in</h3>
          <RecordedMarkdown
            v-if="handInExpanded"
            class="hand-in-full"
            :content="report.hand_in!.text"
          />
          <p v-else class="excerpt">{{ excerpt }}</p>
          <button
            class="text-button hand-in-toggle"
            :aria-expanded="handInExpanded"
            @click="handInExpanded = !handInExpanded"
          >
            {{ handInExpanded ? 'Show less' : 'Show full hand-in' }}
          </button>
        </section>
      </template>
      <template v-else-if="tab !== 'summary'">
        <p v-if="!rows[tab].length" class="type-caption fold-empty empty-card">None recorded</p>
        <button
          v-for="row in rows[tab]"
          :key="row.id"
          class="check-row list-row"
          @click="go(row.id)"
          @mouseenter="emit('hover', row.id)"
          @mouseleave="emit('hover', null)"
          @focus="emit('hover', row.id)"
          @blur="emit('hover', null)"
        >
          <span :style="{ color: colorOf(row.status) }">
            <span aria-hidden="true">{{ statusOf(row.status, row.kind).icon }}</span>
            <span class="sr-only">{{ statusOf(row.status, row.kind).srText }}</span>
          </span>
          <span class="check-row-label">
            <span v-if="tab === 'concerns'" class="type-caption">{{ kindName(row.kind) }} · </span>
            {{ row.label }}
            <span v-if="row.on" class="type-caption row-sub">on: {{ row.on }}</span>
          </span>
          <span v-if="row.pct !== undefined" class="type-caption">{{ row.pct }}%</span>
          <span v-else-if="row.cited !== undefined" class="type-caption"
            >cited by {{ row.cited }}</span
          >
        </button>
      </template>
      <template v-else-if="section">
        <section class="selected-check" aria-label="Selected">
          <p v-if="section.node.paper_id" class="paper-id">{{ section.node.paper_id }}</p>
          <div v-if="section.status.word" class="selected-status">
            <b :style="{ color: colorOf(section.node.status) }"
              ><span aria-hidden="true">{{ section.status.icon }}</span>
              {{ section.status.word }}</b
            >
          </div>
          <blockquote
            v-if="section.node.quote && section.node.quote !== section.node.label"
            :style="{ borderLeftColor: colorOf(section.node.status) }"
          >
            {{ section.node.quote }}
          </blockquote>
        </section>
        <section v-for="(fold, f) in section.folds" :key="fold.title" class="review-fold">
          <button
            class="fold-toggle"
            :aria-expanded="openFolds.has(fold.title)"
            @click="toggle(openFolds, fold.title)"
          >
            <b>{{ fold.title }}</b
            ><span class="type-caption"
              ><span :aria-hidden="fold.srSummary ? 'true' : undefined">{{ fold.summary }}</span
              ><span v-if="fold.srSummary" class="sr-only">{{ fold.srSummary }}</span></span
            >
          </button>
          <template v-if="openFolds.has(fold.title)">
            <p v-if="!fold.rows.length" class="type-caption fold-empty">None recorded</p>
            <template v-for="(row, r) in fold.rows" :key="`${f}:${r}`">
              <button v-if="row.kind === 'link'" class="row-link" @click="go(row.nodeId)">
                {{ row.label }}
              </button>
              <template v-else>
                <button
                  v-if="!row.inline"
                  class="check-row"
                  :class="{ 'no-status': !row.status, 'no-evidence': row.pct === undefined }"
                  :aria-expanded="openRows.has(`${f}:${r}`)"
                  @click="toggle(openRows, `${f}:${r}`)"
                >
                  <span v-if="row.status" :style="{ color: colorOf(row.status) }"
                    ><span aria-hidden="true">{{
                      row.status ? statusOf(row.status).icon : ''
                    }}</span
                    ><span v-if="row.status" class="sr-only">{{
                      statusOf(row.status).srText
                    }}</span></span
                  >
                  <span class="check-row-label">{{ row.label }}</span>
                  <span v-if="row.pct !== undefined" class="evidence-bar" aria-hidden="true"
                    ><i
                      :style="{
                        width: `${row.pct}%`,
                        background: row.status ? colorOf(row.status) : 'var(--muted)',
                      }"
                  /></span>
                  <span v-if="row.pct !== undefined" class="type-caption">{{
                    row.pctText ?? `${row.pct}%`
                  }}</span>
                </button>
                <dl v-if="row.inline || openRows.has(`${f}:${r}`)" class="row-detail" :class="{ 'inline-detail': row.inline }">
                  <template v-for="(block, b) in row.detail" :key="b">
                    <dt class="type-caption">{{ block.label }}</dt>
                    <dd>
                      <button
                        v-if="block.nodeId"
                        class="detail-link"
                        @click="go(block.nodeId)"
                        v-text="block.text"
                      /><template v-else>{{ block.text }}</template>
                    </dd>
                  </template>
                </dl>
              </template>
            </template>
          </template>
        </section>
      </template>
    </section>
  </section>
</template>

<style scoped>
.report-pane {
  min-width: 0;
  overflow-wrap: anywhere;
}
.report-review-header {
  margin-bottom: 18px;
}
.report-review-header h2 {
  margin: 0 0 4px;
  font-size: 16px;
  font-weight: 600;
}
.report-review-header p {
  margin: 0;
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
}
.overview-card {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
}
.overview-eyebrow {
  display: block;
  margin-bottom: 8px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 600;
}
.verdict {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
}
.excerpt {
  margin: 0;
  color: var(--muted);
  line-height: 1.5;
}
.pane-tabs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin: 16px 0;
}
.pane-tab {
  min-width: 0;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: 7px;
  color: var(--text);
  text-align: left;
}
.pane-tab[aria-selected='true'] {
  border-color: var(--muted);
  background: var(--hover);
}
.pane-tab:hover {
  background: var(--hover);
}
.pane-tab:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}
.tab-heading {
  display: flex;
  justify-content: space-between;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
}
.tab-caption {
  display: block;
  margin-top: 6px;
  color: var(--muted);
  font-size: 11px;
}
.tab-count {
  font-variant-numeric: tabular-nums;
}
.hand-in-full {
  overflow-wrap: anywhere;
}
.hand-in-toggle {
  margin-top: 8px;
  padding-inline: 0;
  color: var(--accent, #337ab7);
  text-align: left;
}
.paper-id {
  margin: 0 0 8px;
  color: var(--muted);
  font-size: 12px;
}
.hand-in-preview h3 {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 600;
}
.empty-card {
  padding: 24px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  text-align: center;
}
.pane-panel:focus {
  outline: none;
}
.check-row.list-row {
  grid-template-columns: 16px minmax(0, 1fr) auto;
}
.row-sub {
  display: block;
  color: var(--muted);
}
.overview-counts {
  list-style: none;
  margin: 0;
  padding: 0;
  margin-top: 8px;
}
.overview-counts li {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr) auto;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
.overview-counts li:last-child {
  border-bottom: 0;
  padding-bottom: 0;
}
.overview-counts b {
  font-variant-numeric: tabular-nums;
}
.selected-check {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 14px;
  margin: 14px 0;
}
.selected-status {
  display: flex;
  align-items: baseline;
  gap: 9px;
  flex-wrap: wrap;
}
.selected-status {
  margin: 10px 0 8px;
}
.selected-status b {
  font-weight: 600;
}
.selected-check blockquote {
  border-left: 2px solid;
  background: var(--input);
  padding: 9px 12px;
  margin: 9px 0 0;
  white-space: pre-wrap;
}
.review-fold {
  margin-top: 16px;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}
.fold-toggle {
  display: flex;
  width: 100%;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 4px 0;
  text-align: left;
}
.fold-toggle > span,
.fold-empty {
  color: var(--muted);
}
.check-row {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr) minmax(45px, 100px) minmax(36px, auto);
  gap: 8px;
  align-items: center;
  width: 100%;
  padding: 9px 4px;
  text-align: left;
  border-radius: 5px;
}
.check-row.no-evidence {
  grid-template-columns: 16px minmax(0, 1fr);
}
.check-row.no-status {
  grid-template-columns: minmax(0, 1fr) minmax(45px, 100px) minmax(36px, auto);
  padding-inline: 0;
}
.check-row.no-status.no-evidence {
  grid-template-columns: minmax(0, 1fr);
}
.row-link {
  display: block;
  width: 100%;
  padding: 9px 4px 9px 28px;
  text-align: left;
  border-radius: 5px;
  text-decoration: underline;
  text-decoration-color: var(--border);
  text-underline-offset: 3px;
}
.detail-link {
  text-align: left;
  text-decoration: underline;
  text-decoration-color: var(--border);
  text-underline-offset: 3px;
}
.check-row:hover,
.check-row[aria-expanded='true'],
.row-link:hover {
  background: var(--hover);
}
.check-row-label {
  overflow-wrap: anywhere;
}
.evidence-bar {
  display: block;
  height: 5px;
  background: var(--border);
  border-radius: 3px;
}
.evidence-bar > i {
  display: block;
  height: 100%;
  border-radius: 3px;
}
.row-detail {
  margin: 4px 0 8px 28px;
  padding: 9px 12px;
  background: var(--input);
  border-radius: 5px;
}
.row-detail.inline-detail {
  margin-left: 0;
}
.row-detail dt {
  color: var(--muted);
}
.row-detail dd {
  margin: 2px 0 8px;
  white-space: pre-wrap;
}
.row-detail dd:last-child {
  margin-bottom: 0;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
</style>
