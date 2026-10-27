<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { workspaceId } from '../api/session';
import { chatPath, keys, useCoresearchCells } from './api';
import { buildNotebookContexts, kernelsOf } from './notebook-model';
import type { ProjectRow, ChatRow } from './types';
import NotebookCell from './NotebookCell.vue';
import NotebookAxis from './NotebookAxis.vue';
import NotebookStatusBar from './NotebookStatusBar.vue';
import EnvironmentBar from './EnvironmentBar.vue';
const props = defineProps<{ project: ProjectRow; chat: ChatRow; owner: boolean }>();
const cells = useCoresearchCells(
    () => props.project.id,
    () => props.chat.id,
  ),
  client = useQueryClient();
const context = computed(() => buildNotebookContexts(cells.data.value ?? [])[0]!);
const kernels = computed(() => kernelsOf(context.value)),
  active = ref<string | null>(null);
const shown = computed(() =>
  active.value === null
    ? context.value.cells
    : context.value.cells.filter((cell) => cell.kernel === active.value),
);
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
      ><span class="nbp-session-label">{{ chat.cli }}</span>
    </header>
    <NotebookAxis :kernels="kernels" :active="active" @select="active = $event" />
    <div class="nbp-cells" data-testid="notebook-cells">
      <p v-if="cells.error.value" class="nbp-warning" role="alert">
        {{ cells.error.value.message }}
      </p>
      <p v-else-if="cells.isPending.value" class="nbp-loading" role="status">Loading notebook…</p>
      <p v-else-if="!shown.length" class="nbp-empty">
        {{
          active
            ? `No ${active} cells. Choose All to see the rest.`
            : 'No cells yet. Run code in this chat to begin the notebook.'
        }}
      </p>
      <NotebookCell
        v-for="cell in shown"
        :key="cell.id"
        :project-id="project.id"
        :chat-id="chat.id"
        :cell="cell"
      />
      <div v-if="owner" class="nbp-cell" data-testid="cell-editor">
        <div class="code-block">
          <div class="code-block-head">
            <label class="nbp-cell-meta flex-wrap"
              ><span class="nbp-gutter">In [ ]</span
              ><input
                v-model="kernel"
                list="coresearch-kernels"
                aria-label="Kernel"
                class="nbp-cell-lang w-28 max-w-full"
                :disabled="disabled || busy" /><datalist id="coresearch-kernels">
                <option value="python3" />
                <option
                  v-for="name in kernels.filter((k) => k !== 'python3')"
                  :key="name"
                  :value="name"
                /></datalist></label
            ><button
              type="button"
              class="code-block-copy"
              :disabled="disabled || busy || !source.trim() || !kernel.trim()"
              @click="perform"
            >
              Run
            </button>
          </div>
          <textarea
            v-model="source"
            aria-label="Cell source"
            class="code-block-pre w-full resize-y border-0 bg-transparent p-3 font-mono text-sm"
            rows="4"
            placeholder="Write code…"
            :disabled="disabled || busy"
          />
        </div>
        <p v-if="error" class="nbp-warning" role="alert">{{ error }}</p>
      </div>
    </div>
    <EnvironmentBar :project="project" :chat="chat" :owner="owner" :disabled="disabled" />
    <NotebookStatusBar
      :project-id="project.id"
      :chat="chat"
      :count="shown.length"
      :owner="owner"
      :disabled="disabled"
    />
  </section>
</template>
