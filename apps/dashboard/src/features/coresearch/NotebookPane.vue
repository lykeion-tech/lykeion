<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { chatPath, keys, useCoresearchCells, useCoresearchFiles, useCoresearchRuns } from './api';
import { buildNotebookContexts, kernelsOf } from './notebook-model';
import type { ProjectRow, ChatRow } from './types';
import NotebookCell from './NotebookCell.vue';
import RecordedNotebookCell from './RecordedNotebookCell.vue';
import NotebookAxis from './NotebookAxis.vue';
import EnvironmentBar from './EnvironmentBar.vue';
const props = defineProps<{ project: ProjectRow; chat: ChatRow; owner: boolean }>();
const cells = useCoresearchCells(
    () => props.project.id,
    () => props.chat.id,
  ),
  client = useQueryClient();
const context = computed(() => buildNotebookContexts(cells.data.value ?? [])[0]!);
const runs = useCoresearchRuns(() => props.project.id);
const files = useCoresearchFiles(() => props.project.id);
const recorded = computed(() =>
  (runs.data.value ?? []).flatMap((run) => {
    const path = files.data.value?.find((file) => file.id === run.script_file_id)?.path;
    if (run.chat_id !== props.chat.id || !run.agent_run_id || !path || !/\.(py|r|jl)$/i.test(path))
      return [];
    const kernel = /\.py$/i.test(path) ? 'python3' : /\.jl$/i.test(path) ? 'julia' : 'r';
    return [{ run, path, kernel }];
  }),
);
const kernels = computed(() =>
    [
      ...new Set([...kernelsOf(context.value), ...recorded.value.map((item) => item.kernel)]),
    ].sort(),
  ),
  active = ref<string | null>(null);
const agentCells = computed(() => context.value.cells.filter((cell) => !!cell.agent_run_id));
const userCells = computed(() => context.value.cells.filter((cell) => !cell.agent_run_id));
const shown = computed(() =>
  active.value === null
    ? agentCells.value
    : agentCells.value.filter((cell) => cell.kernel === active.value),
);
const shownRuns = computed(() =>
  recorded.value.filter((item) => !active.value || item.kernel === active.value),
);
const consoleOpen = ref(false);
watch(kernels, (names) => {
  if (active.value && !names.includes(active.value)) active.value = null;
});
const source = ref(''),
  kernel = ref('python3'),
  busy = ref(false),
  error = ref('');
const disabled = computed(() => !!props.project.archived_at);
async function perform() {
  if (busy.value || disabled.value || !source.value.trim() || !kernel.value.trim()) return;
  const ws = workspaceId.value,
    p = props.project.id,
    c = props.chat.id;
  busy.value = true;
  error.value = '';
  try {
    await api(
      'POST',
      `${chatPath(p, c)}/cells`,
      { kernel: kernel.value.trim(), source: source.value },
      undefined,
      { workspace: ws },
    );
    source.value = '';
    await client.invalidateQueries({ queryKey: keys.coresearchCells(ws, p, c) });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section
    class="notebook-panel"
    data-testid="notebook-panel"
    role="tabpanel"
    aria-label="Notebook"
  >
    <header class="art-header">
      <span class="art-title"
        ><svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path d="M4 2h9v12H4zM2 5h4M2 8h4M2 11h4" stroke="currentColor" stroke-width="1.2" /></svg
        >Notebook</span
      >
    </header>
    <NotebookAxis :kernels="kernels" :active="active" @select="active = $event" />
    <div class="nbp-cells" data-testid="notebook-cells">
      <p v-if="cells.error.value" class="nbp-warning" role="alert">
        {{ cells.error.value.message }}
      </p>
      <p v-if="runs.error.value || files.error.value" class="nbp-warning" role="alert">
        Recorded scripts could not be loaded.
      </p>
      <p
        v-else-if="cells.isPending.value || runs.isPending.value || files.isPending.value"
        class="nbp-loading"
        role="status"
      >
        Loading notebook…
      </p>
      <p v-else-if="!shown.length && !shownRuns.length" class="nbp-empty">
        {{ active ? `No ${active} cells. Choose All to see the rest.` : 'No agent cells yet.' }}
      </p>
      <NotebookCell
        v-for="cell in shown"
        :key="cell.id"
        :project-id="project.id"
        :chat-id="chat.id"
        :cell="cell"
      />
      <RecordedNotebookCell
        v-for="item in shownRuns"
        :key="item.run.id"
        :project-id="project.id"
        :run="item.run"
        :path="item.path"
      />
    </div>
    <EnvironmentBar :project="project" :chat="chat" :owner="owner" :disabled="disabled" />
    <div v-if="owner || userCells.length" class="notebook-console-dock">
      <button
        type="button"
        class="notebook-console-toggle"
        :aria-label="owner ? 'Run code' : 'Code history'"
        :aria-expanded="consoleOpen"
        @click="consoleOpen = !consoleOpen"
      >
        <span>{{ owner ? 'Run code' : 'Code history' }}</span
        ><span aria-hidden="true">{{ consoleOpen ? '⌄' : '⌃' }}</span>
      </button>
      <div v-if="consoleOpen" class="notebook-console" data-testid="cell-editor">
        <div class="notebook-console-head">
          <label v-if="owner"
            >Kernel
            <input
              v-model="kernel"
              list="coresearch-kernels"
              aria-label="Kernel"
              :disabled="disabled || busy"
            />
            <datalist id="coresearch-kernels">
              <option value="python3" />
              <option
                v-for="name in kernels.filter((k) => k !== 'python3')"
                :key="name"
                :value="name"
              />
            </datalist>
          </label>
          <span v-else>Code history</span>
          <span>{{
            chat.kernel_state ? `Kernel · ${chat.kernel_state}` : 'Kernel status unavailable'
          }}</span>
        </div>
        <div v-if="userCells.length" class="notebook-console-history">
          <NotebookCell
            v-for="cell in userCells"
            :key="cell.id"
            :project-id="project.id"
            :chat-id="chat.id"
            :cell="cell"
          />
        </div>
        <div v-if="owner" class="notebook-console-input">
          <span aria-hidden="true">&gt;&gt;&gt;</span>
          <textarea
            v-model="source"
            aria-label="Cell source"
            rows="3"
            placeholder="Write code…"
            :disabled="disabled || busy"
          />
        </div>
        <div v-if="owner" class="notebook-console-footer">
          <span>Runs in the notebook kernel</span>
          <button
            type="button"
            :disabled="disabled || busy || !source.trim() || !kernel.trim()"
            @click="perform"
          >
            Run
          </button>
        </div>
        <p v-if="error" class="notebook-console-error" role="alert">{{ error }}</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.notebook-panel {
  flex: 1 1 auto;
}
.notebook-panel .nbp-empty {
  padding: 2px 20px 16px;
}
.notebook-console-dock {
  flex: none;
  border-top: 1px solid var(--hairline);
  background: var(--surface-2);
}
.notebook-console-toggle {
  width: 100%;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border: 0;
  background: transparent;
  color: var(--ink);
  font: inherit;
  cursor: pointer;
}
.notebook-console-toggle:hover {
  background: var(--surface-3);
}
.notebook-console {
  margin: 0 10px 10px;
  max-height: min(48vh, 360px);
  border-radius: 7px;
  overflow-y: auto;
  background: var(--surface);
  color: var(--text);
}
.notebook-console-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border);
  color: var(--text);
  font-size: 13px;
}
.notebook-console-head label {
  display: flex;
  align-items: center;
  gap: 6px;
}
.notebook-console-head input {
  width: 110px;
  max-width: 100%;
  padding: 3px 6px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--surface);
  color: var(--text);
  font: inherit;
}
.notebook-console-history {
  max-height: 180px;
  overflow-y: auto;
}
.notebook-console-history :deep(.nbp-cell) {
  border-color: var(--border);
  color: var(--text);
}
.notebook-console-history :deep(.code-block) {
  background: var(--surface);
  border-color: var(--border);
  color: var(--text);
}
.notebook-console-history :deep(.nbp-cell-meta),
.notebook-console-history :deep(.nbp-gutter),
.notebook-console-history :deep(.nbp-cell-by),
.notebook-console-history :deep(.nbp-cell-lang) {
  color: var(--text);
}
.notebook-console-input {
  display: flex;
  gap: 10px;
  padding: 12px;
  color: var(--text);
  font: 13px/1.5 var(--font-mono, monospace);
}
.notebook-console-input textarea {
  flex: 1;
  min-width: 0;
  width: 100%;
  resize: none;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text);
  font: inherit;
}
.notebook-console-input textarea::placeholder {
  color: var(--text);
  opacity: 1;
}
.notebook-console-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 0 10px 10px;
  color: var(--text);
  font-size: 13px;
}
.notebook-console-footer button {
  padding: 5px 12px;
  border: 0;
  border-radius: 5px;
  background: #357bac;
  color: white;
  font: inherit;
  cursor: pointer;
}
.notebook-console-footer button:disabled {
  opacity: 0.5;
  cursor: default;
}
.notebook-console-error {
  margin: 0;
  padding: 0 10px 10px;
  color: #ffbc9b;
}
</style>
