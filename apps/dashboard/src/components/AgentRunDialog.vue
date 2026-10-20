<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import Icon from './Icon.vue';
import Modal from './Modal.vue';
import RunTurn from './RunTurn.vue';
import StudyEnvironment from './StudyEnvironment.vue';
import { useRunMessages } from '../api/queries';
import type { AgentRun, Runtime } from '../api/types';
import { backendLabel } from '../lib/backends';
import { executionCells, notebookJson, recordedCode } from '../lib/run-transcript';
import { columnName, roleLabel } from '../lib/studies';

const props = defineProps<{
  runs: AgentRun[];
  runtime: (runtimeId: string | null) => Runtime | undefined;
  studyId: string;
}>();
const id = defineModel<string>({ required: true });
const emit = defineEmits<{ close: [] }>();

const index = computed(() =>
  Math.max(
    0,
    props.runs.findIndex((r) => r.id === id.value),
  ),
);
const run = computed(() => props.runs[index.value]!);
const name = computed(() => run.value.agent_name || roleLabel(run.value.role));
function step(by: number) {
  const n = props.runs.length;
  id.value = props.runs[(index.value + by + n) % n]!.id;
}

const tabs = [
  { id: 'transcript', label: 'Transcript' },
  { id: 'code', label: 'Code' },
  { id: 'log', label: 'Execution Log' },
  { id: 'environment', label: 'Environment' },
] as const;
type Tab = (typeof tabs)[number]['id'];
const tab = ref<Tab>('transcript');
watch(id, () => (tab.value = 'transcript'));
const tabList = ref<HTMLElement>();
function moveTab(event: KeyboardEvent) {
  const by = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
  if (!by) return;
  event.preventDefault();
  const i = tabs.findIndex((t) => t.id === tab.value);
  tab.value = tabs[(i + by + tabs.length) % tabs.length]!.id;
  void nextTick(() => tabList.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.focus());
}

const messages = useRunMessages(run, true);
const code = computed(() => recordedCode(messages.data.value ?? []));
const cells = computed(() => executionCells(messages.data.value ?? []));
// Only what the run and its machine's daemon recorded; nothing is filled in.
const environment = computed(() => {
  const r = run.value;
  const machine = props.runtime(r.runtime_id);
  return [
    { key: 'Runtime', value: backendLabel(r.harness) },
    { key: 'Model', value: r.model },
    { key: 'Machine', value: machine ? machine.custom_name || machine.device_name : '' },
    { key: 'Language', value: machine?.language ?? '' },
    {
      key: 'Platform',
      value: machine?.os && machine.arch ? `${machine.os} / ${machine.arch}` : (machine?.os ?? ''),
    },
    { key: 'Kernel', value: machine?.kernel ?? '' },
    { key: 'Stage', value: columnName(r.board_column) },
    { key: 'Round', value: String(r.round) },
    { key: 'Attempt', value: `${r.attempt} of ${r.max_attempts}` },
  ].filter((e) => e.value);
});
const lines = (text: string) => text.replace(/\n$/, '').split('\n');

const notice = ref('');
async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    notice.value = 'Copied';
  } catch {
    notice.value = 'Could not copy. Select the code to copy it.';
  }
}
function download(name: string, text: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  URL.revokeObjectURL(url);
}
const baseName = (path: string) => path.split(/[\\/]/).pop() || path;
</script>
<template>
  <Modal :title="`${name} transcript`" wide class="agent-run-dialog" @close="emit('close')">
    <template #default="{ close }">
      <header class="run-dialog-heading">
        <div class="run-dialog-title">
          <h2>{{ name }}</h2>
          <p>
            Run {{ index + 1 }} · {{ columnName(run.board_column) }} · Round {{ run.round }} ·
            Attempt {{ run.attempt }}
          </p>
        </div>
        <div class="run-dialog-actions">
          <button
            v-if="runs.length > 1"
            class="icon-button"
            aria-label="Previous agent"
            @click="step(-1)"
          >
            <Icon name="left" />
          </button>
          <span v-if="runs.length > 1" class="run-position"
            >{{ index + 1 }} / {{ runs.length }}</span
          >
          <button
            v-if="runs.length > 1"
            class="icon-button"
            aria-label="Next agent"
            @click="step(1)"
          >
            <Icon name="right" />
          </button>
          <button class="icon-button" aria-label="Close transcript" @click="close()">
            <Icon name="close" />
          </button>
        </div>
      </header>
      <div ref="tabList" class="run-tabs" role="tablist" aria-label="Run views" @keydown="moveTab">
        <button
          v-for="t in tabs"
          :id="`run-tab-${t.id}`"
          :key="t.id"
          role="tab"
          :aria-selected="tab === t.id"
          :aria-controls="`run-panel-${t.id}`"
          :tabindex="tab === t.id ? 0 : -1"
          @click="tab = t.id"
        >
          {{ t.label }}
        </button>
      </div>
      <div
        :id="`run-panel-${tab}`"
        class="run-panel"
        role="tabpanel"
        :aria-labelledby="`run-tab-${tab}`"
        tabindex="0"
      >
        <RunTurn v-if="tab === 'transcript'" :key="run.id" :run="run" />

        <template v-else-if="tab === 'code'">
          <p v-if="!code.length" class="run-empty">No code was recorded in this run.</p>
          <section v-for="c in code" :key="c.key" class="code-section" :aria-label="c.name">
            <button
              class="text-button download-link"
              @click="download(baseName(c.name), c.code, 'text/plain')"
            >
              Download script
            </button>
            <div class="code-card">
              <div class="code-bar">
                <span class="code-language">{{ c.language }}</span>
                <span class="code-name truncate">{{ c.name }}</span>
                <button class="button compact copy-button" @click="copy(c.code)">
                  <Icon name="copy" :size="13" />Copy
                </button>
              </div>
              <pre tabindex="0"><code><span
                v-for="(line, i) in lines(c.code)"
                :key="i"
                class="code-line"
              ><span class="line-number" aria-hidden="true">{{ i + 1 }}</span>{{ line }}
</span></code></pre>
            </div>
          </section>
        </template>

        <template v-else-if="tab === 'log'">
          <p v-if="!cells.length" class="run-empty">No tool calls were recorded in this run.</p>
          <template v-else>
            <button
              class="text-button download-link"
              @click="
                download(
                  `${run.role}-attempt-${run.attempt}.ipynb`,
                  notebookJson(cells),
                  'application/x-ipynb+json',
                )
              "
            >
              Download notebook
            </button>
            <ol class="cells">
              <li v-for="(cell, i) in cells" :key="cell.key" :class="{ 'cell-error': cell.error }">
                <div class="code-card">
                  <div class="code-bar">
                    <span class="cell-number">In [{{ i + 1 }}]</span>
                    <span class="code-language">{{ cell.language }}</span>
                    <span class="code-name truncate">{{ cell.label }}</span>
                    <button class="button compact copy-button" @click="copy(cell.source)">
                      <Icon name="copy" :size="13" />Copy
                    </button>
                  </div>
                  <pre tabindex="0"><code><span
                    v-for="(line, j) in lines(cell.source)"
                    :key="j"
                    class="code-line"
                  ><span class="line-number" aria-hidden="true">{{ j + 1 }}</span>{{ line }}
</span></code></pre>
                </div>
                <details v-if="cell.output" class="cell-output">
                  <summary>Show output</summary>
                  <pre tabindex="0">{{ cell.output }}</pre>
                </details>
              </li>
            </ol>
          </template>
        </template>

        <template v-else>
          <ul class="environment">
            <li v-for="e in environment" :key="e.key">
              <span>{{ e.key }}</span
              ><code>{{ e.value }}</code>
            </li>
          </ul>
          <StudyEnvironment :id="studyId" :runs="runs" class="study-environment" />
        </template>
        <span class="copy-notice" role="status">{{ notice }}</span>
      </div>
    </template>
  </Modal>
</template>
<style scoped>
.run-dialog-title {
  flex: 1;
  min-width: 0;
}
.run-dialog-title h2 {
  overflow-wrap: anywhere;
}
.run-dialog-title p {
  color: var(--muted);
  font-size: 12px;
  margin-top: 4px;
}

.agent-run-dialog {
  height: min(800px, 90vh);
}
.agent-run-dialog[open] {
  display: flex;
  flex-direction: column;
}
.run-dialog-heading {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 20px 20px 0;
}
.run-dialog-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 2px;
}
.run-position {
  padding: 0 4px;
  font-size: 12px;
  color: var(--muted);
}
.run-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: 14px 20px 0;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}
.run-tabs button {
  height: 32px;
  padding: 0 12px;
  border-radius: 6px;
  color: var(--muted);
  font-size: 14px;
}
.run-tabs button:hover {
  color: var(--text);
}
.run-tabs button[aria-selected='true'] {
  background: var(--fill-active);
  color: var(--text);
}
.run-panel {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px 20px 28px;
}
.run-empty {
  color: var(--muted);
  font-size: 13px;
}
.download-link {
  display: block;
  padding: 4px 0 14px;
  font-size: 14px;
}
.code-section + .code-section {
  margin-top: 24px;
}
.code-card {
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.code-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px 6px 12px;
  background: var(--fill-inset);
  font-size: 12px;
  color: var(--muted);
  min-width: 0;
}
.code-language {
  padding: 0 6px;
  border-radius: 4px;
  background: var(--fill-chip);
  font:
    11px/20px ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
.cell-number {
  font:
    12px ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
.code-name {
  min-width: 0;
}
.copy-button {
  margin-left: auto;
  border: 0;
  background: transparent;
  color: var(--muted);
}
.code-card pre {
  margin: 0;
  padding: 12px 0;
  overflow-x: auto;
  background: #1f2328;
  color: #e6edf3;
  font:
    12.5px/1.75 ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
.code-line {
  display: block;
  padding-right: 16px;
  white-space: pre;
}
.line-number {
  display: inline-block;
  width: 44px;
  padding-right: 16px;
  box-sizing: border-box;
  text-align: right;
  color: #6e7681;
  user-select: none;
}
.cells {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.cell-error .cell-number {
  color: var(--error-text);
}
.cell-output {
  margin-top: 6px;
  font-size: 13px;
  color: var(--muted);
}
.cell-output summary {
  cursor: pointer;
  width: fit-content;
}
.cell-output pre {
  margin: 4px 0 0;
  padding: 10px 12px;
  border-radius: 6px;
  background: var(--fill-inset);
  color: var(--text);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font:
    12px/1.6 ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
.environment {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
.environment li {
  display: inline-flex;
  align-items: baseline;
  gap: 8px;
  padding: 5px 12px;
  border-radius: 6px;
  background: var(--fill-chip);
  font-size: 13px;
}
.environment span {
  color: var(--muted);
}
.environment code {
  font:
    12.5px ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
.study-environment {
  margin-top: 24px;
}
.copy-notice {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>
