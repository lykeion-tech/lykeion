<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useNotebookRecords, useRuns } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { agentDisplayName } from '../../lib/agent-name';
import { notebookCells, type NotebookCell } from '../../lib/notebook';
import { columnName, stages } from '../../lib/studies';
import HighlightedSource from '../runs/HighlightedSource.vue';

const props = defineProps<{ cardId: string; runId?: string | null; scrollRequest?: number }>();
const records = useNotebookRecords(() => props.cardId);
const runs = useRuns(() => props.cardId);
const cells = computed(() => notebookCells(records.data.value ?? []));
const groups = computed(() =>
  stages.flatMap((column) => {
    const stageCells = cells.value.filter((cell) => cell.column === column);
    return [...new Set(stageCells.map((cell) => cell.round))]
      .sort((a, b) => a - b)
      .map((round) => ({
        column,
        round,
        cells: stageCells.filter((cell) => cell.round === round),
      }));
  }),
);
const expanded = ref(new Set<string>());
watch([workspaceId, () => props.cardId], () => {
  expanded.value = new Set();
});
const agentName = (cell: NotebookCell) => {
  const run = runs.data.value?.find((run) => run.id === cell.run);
  return run?.agent_name ? agentDisplayName(run.agent_name) : 'Agent not recorded';
};
const lines = (output: string) => output.replace(/\r?\n$/, '').split(/\r?\n/);
const visibleOutput = (cell: NotebookCell, error = false) => {
  const stream = error ? cell.error : cell.output;
  return expanded.value.has(`${cell.key}:${error}`) || lines(stream).length <= 12
    ? stream
    : lines(stream).slice(0, 12).join('\n');
};
function toggle(cell: NotebookCell, error = false) {
  const key = `${cell.key}:${error}`;
  if (expanded.value.has(key)) expanded.value.delete(key);
  else expanded.value.add(key);
}
const elements = new Map<string, HTMLElement>();
function rememberCell(key: string, element: unknown) {
  if (element) elements.set(key, element as HTMLElement);
  else elements.delete(key);
}
let scrollPending = true;
watch(
  [workspaceId, () => props.cardId, () => props.runId, () => props.scrollRequest],
  () => {
    scrollPending = true;
  },
  { flush: 'sync' },
);
watch(
  [() => props.runId, () => props.scrollRequest, groups],
  async (_, __, onCleanup) => {
    if (!scrollPending || !props.runId) return;
    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });
    const cell = groups.value
      .flatMap((group) => group.cells)
      .find((cell) => cell.run === props.runId);
    if (!cell) return;
    await nextTick();
    const element = elements.get(cell.key);
    if (!cancelled && element) {
      scrollPending = false;
      element.scrollIntoView?.({ block: 'start', behavior: 'smooth' });
    }
  },
  { immediate: true },
);
</script>
<template>
  <section class="study-notebook" aria-label="Notebook">
    <h2>Notebook</h2>
    <p v-if="records.isPending.value" role="status">Loading notebook…</p>
    <p v-else-if="records.isError.value" role="alert">
      {{ records.error.value?.message }} <button @click="records.refetch()">Retry</button>
    </p>
    <p v-else-if="!cells.length" class="empty">No scripts were recorded in this study yet.</p>
    <section
      v-for="group in groups"
      v-else
      :key="`${group.column}:${group.round}`"
      class="cell-group"
    >
      <h3>{{ columnName(group.column) }} · try {{ group.round }}</h3>
      <article
        v-for="cell in group.cells"
        :key="cell.key"
        :ref="(el) => rememberCell(cell.key, el)"
        :data-run-id="cell.run"
        class="notebook-cell"
      >
        <header>
          <div class="cell-agent">
            {{ agentName(cell) }} · {{ columnName(cell.column) }} · try {{ cell.round }}
          </div>
          <code v-if="cell.path" class="cell-path">{{ cell.path }}</code>
          <span v-if="cell.exit !== null && cell.exit !== 0" class="cell-failure"
            >✗ exit {{ cell.exit }}</span
          >
        </header>
        <HighlightedSource :code="cell.code" :language="cell.language" />
        <div v-if="cell.output || cell.outputTruncated" class="cell-output">
          <pre aria-label="Printed output">{{ visibleOutput(cell) }}</pre>
          <button
            v-if="lines(cell.output).length > 12"
            :aria-expanded="expanded.has(`${cell.key}:false`)"
            @click="toggle(cell)"
          >
            {{
              expanded.has(`${cell.key}:false`)
                ? 'show less'
                : `show all ${lines(cell.output).length} lines`
            }}
          </button>
          <p v-if="cell.outputTruncated" class="truncated">Recorded output was truncated.</p>
        </div>
        <div v-if="cell.error || cell.errorTruncated" class="cell-output cell-error">
          <small>Standard error</small>
          <pre aria-label="Standard error">{{ visibleOutput(cell, true) }}</pre>
          <button
            v-if="lines(cell.error).length > 12"
            :aria-expanded="expanded.has(`${cell.key}:true`)"
            @click="toggle(cell, true)"
          >
            {{
              expanded.has(`${cell.key}:true`)
                ? 'show less'
                : `show all ${lines(cell.error).length} lines`
            }}
          </button>
          <p v-if="cell.errorTruncated" class="truncated">Recorded error was truncated.</p>
        </div>
      </article>
    </section>
  </section>
</template>
<style scoped>
.study-notebook {
  padding: 24px 20px;
}
h2 {
  margin: 0 0 16px;
  font-size: 16px;
}
h3 {
  margin: 24px 0 12px;
  font-size: 13px;
  color: var(--muted);
}
.notebook-cell {
  margin-bottom: 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  scroll-margin-top: 16px;
}
header {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 12px;
  border-bottom: 1px solid var(--border);
}
.cell-agent {
  font-size: 12px;
}
.cell-path {
  font-size: 12px;
  overflow-wrap: anywhere;
}
.cell-failure {
  color: var(--danger-text);
  font-size: 12px;
}
.cell-output {
  border-top: 1px solid var(--border);
  padding: 12px;
}
pre {
  margin: 0;
  overflow: auto;
  font:
    12px/1.6 ui-monospace,
    monospace;
}
small {
  display: block;
  margin-bottom: 6px;
  color: var(--muted);
}
button {
  margin-top: 8px;
  font-size: 12px;
  color: var(--muted);
  text-decoration: underline;
  cursor: pointer;
}
.empty,
.truncated {
  color: var(--muted);
}
.truncated {
  font-size: 12px;
  margin: 8px 0 0;
}
</style>
