<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue';
import type { StudyReport } from '../../api/types';
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

const props = defineProps<{ report: StudyReport; selected: string | null }>();
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
const rows = computed(() => tabRows(props.report, section.value ? props.selected : null));
const tabs = computed<{ id: TabId; label: string; n?: number }[]>(() => [
  { id: 'summary', label: 'Summary' },
  { id: 'findings', label: 'Findings', n: rows.value.findings.length },
  { id: 'concerns', label: 'Concerns', n: rows.value.concerns.length },
  { id: 'sources', label: 'Sources', n: rows.value.sources.length },
]);
function tabKey(e: KeyboardEvent) {
  const step = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
  if (!step) return;
  e.preventDefault();
  const at = tabs.value.findIndex((t) => t.id === tab.value);
  const next = tabs.value[(at + step + tabs.value.length) % tabs.value.length]!;
  tab.value = next.id;
  void nextTick(() => document.getElementById(`report-tab-${next.id}`)?.focus());
}
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

// Back retraces the nodes visited from this pane or the graph; reaching the
// overview starts the path again. Focus follows into the pane only when the
// move started here, so a keyboard user in the graph stays on their node.
const path = ref<string[]>([]);
let goingBack = false;
let fromPane = false;
const labelOf = (id: string) => props.report.nodes.find((n) => n.id === id)?.label ?? id;
const openFolds = reactive(new Set<string>());
const openRows = reactive(new Set<string>());
const panelEl = ref<HTMLElement | null>(null);
watch(
  () => props.selected,
  (now, before) => {
    openFolds.clear();
    openRows.clear();
    if (now === null) path.value = [];
    else if (goingBack) path.value = path.value.slice(0, -1);
    else if (before) path.value = [...path.value, before];
    goingBack = false;
    if (fromPane) void nextTick(() => panelEl.value?.focus());
    fromPane = false;
  },
);
// A parent that ignores the select never fires the watch; don't let the
// flags outlive this move.
function go(id: string | null, back = false) {
  goingBack = back;
  fromPane = true;
  emit('select', id);
  void nextTick(() => (goingBack = fromPane = false));
}
function back() {
  const to = path.value.at(-1);
  if (to) go(to, true);
}
const toggle = (set: Set<string>, key: string) => (set.has(key) ? set.delete(key) : set.add(key));
</script>

<template>
  <section class="report-pane type-body" aria-label="Report section">
    <!-- Focus may stay in the graph, so the newly shown section is announced. -->
    <p class="sr-only" role="status">
      {{ section ? `${kindName(section.node.kind)}: ${section.node.label}` : 'Overview' }}
    </p>
    <nav v-if="section" class="pane-back" aria-label="Report path">
      <span class="focus-chip type-caption">
        <span class="focus-kind">{{ kindName(section.node.kind) }}</span>
        <b>{{ section.node.label }}</b>
        <button class="text-button" aria-label="Clear focus" @click="go(null)">×</button>
      </span>
      <button v-if="path.length" class="text-button" @click="back">
        ← Back to {{ labelOf(path.at(-1)!) }}
      </button>
    </nav>
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
        {{ t.label }}<span v-if="t.n !== undefined" class="tab-count">{{ t.n }}</span>
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
        <p class="verdict">{{ verdictOf(report) }}</p>
        <ul class="overview-counts">
          <li v-for="row in overview" :key="row.label">
            <span :style="{ color: colorOf(row.status) }" aria-hidden="true">{{
              statusOf(row.status).icon
            }}</span>
            <span>{{ row.label }}</span
            ><b>{{ row.n }}</b>
          </li>
        </ul>
        <template v-if="excerpt">
          <h3 class="type-caption pane-label">Hand-in says</h3>
          <p class="excerpt">{{ excerpt }}</p>
        </template>
      </template>
      <template v-else-if="tab !== 'summary'">
        <p v-if="!rows[tab].length" class="type-caption fold-empty">None recorded</p>
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
                  class="check-row"
                  :aria-expanded="openRows.has(`${f}:${r}`)"
                  @click="toggle(openRows, `${f}:${r}`)"
                >
                  <span :style="{ color: row.status ? colorOf(row.status) : undefined }"
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
                <dl v-if="openRows.has(`${f}:${r}`)" class="row-detail">
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
.verdict {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 650;
}
.pane-label {
  margin: 14px 0 4px;
  color: var(--muted);
  text-transform: uppercase;
  letter-spacing: 0.07em;
}
.excerpt {
  margin: 0;
  color: var(--muted);
}
.pane-tabs {
  display: flex;
  gap: 2px;
  margin: 8px 0 12px;
  border-bottom: 1px solid var(--border);
}
.pane-tab {
  padding: 6px 10px;
  margin-bottom: -1px;
  border-bottom: 2px solid transparent;
  color: var(--muted);
}
.pane-tab[aria-selected='true'] {
  color: var(--text);
  font-weight: 600;
  border-bottom-color: var(--primary);
}
.tab-count {
  margin-left: 5px;
  font-variant-numeric: tabular-nums;
  opacity: 0.7;
}
.pane-panel:focus {
  outline: none;
}
.focus-chip {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  max-width: 100%;
  padding: 2px 4px 2px 10px;
  border-radius: 12px;
  background: var(--hover);
}
.focus-kind {
  color: var(--muted);
}
.list-row {
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
}
.overview-counts li {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr) auto;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
.overview-counts b {
  font-variant-numeric: tabular-nums;
}
.pane-back {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px;
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
