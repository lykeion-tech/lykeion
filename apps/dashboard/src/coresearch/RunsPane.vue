<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { workspaceId } from '../api/session';
import {
  keys,
  projectPath,
  useCoresearchRuns,
  useCoresearchRun,
  useCoresearchFiles,
  useCoresearchChats,
} from './api';
import type { ProjectRow, ChatRow, FileRef, RecordedRun } from './types';
const props = defineProps<{ project: ProjectRow; chat?: ChatRow; userId?: string }>();
const emit = defineEmits<{ openFile: [file: FileRef] }>();
const client = useQueryClient(),
  runs = useCoresearchRuns(() => props.project.id),
  files = useCoresearchFiles(() => props.project.id),
  chats = useCoresearchChats(() => props.project.id);
const logRun = ref('');
const selected = ref(''),
  form = ref(false),
  script = ref(''),
  inputs = ref<string[]>([]),
  error = ref(''),
  busy = ref(false);
const detail = useCoresearchRun(
  () => props.project.id,
  () => selected.value,
);
const owner = computed(
  () => !!props.userId && props.chat?.owner_id === props.userId && !props.project.archived_at,
);
function canRerun(run: RecordedRun) {
  return (
    !!props.userId &&
    !props.project.archived_at &&
    chats.data.value?.find((c) => c.id === run.chat_id)?.owner_id === props.userId &&
    run.status === 'ok' &&
    !!run.env_sha
  );
}
function path(id: string) {
  return files.data.value?.find((f) => f.id === id)?.path ?? 'Unavailable file';
}
async function start(original?: RecordedRun) {
  const ws = workspaceId.value,
    id = props.project.id;
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    await api(
      'POST',
      original
        ? `${projectPath(id)}/runs/${encodeURIComponent(original.key)}/rerun`
        : `${projectPath(id)}/runs`,
      original
        ? undefined
        : { chat_id: props.chat?.id, script: script.value, inputs: inputs.value },
      undefined,
      { workspace: ws },
    );
    form.value = false;
    await client.invalidateQueries({ queryKey: keys.coresearchRuns(ws, id) });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section class="compute-panel" role="tabpanel" aria-label="Runs">
    <header class="art-header">
      <span class="art-title">Runs</span
      ><button v-if="owner" type="button" class="quiet-action" @click="form = !form">
        New run
      </button>
    </header>
    <div class="compute-panel-body">
      <form v-if="form && owner" class="prov-content" @submit.prevent="start()">
        <label
          >Script<select v-model="script" aria-label="Script" required>
            <option value="" disabled>Choose a saved file</option>
            <option v-for="file in files.data.value ?? []" :key="file.id" :value="file.path">
              {{ file.path }} v{{ file.head.n }}
            </option>
          </select></label
        ><label
          >Inputs<select v-model="inputs" multiple aria-label="Inputs">
            <option v-for="file in files.data.value ?? []" :key="file.id" :value="file.path">
              {{ file.path }} v{{ file.head.n }}
            </option>
          </select></label
        ><button type="submit" class="quiet-action" :disabled="busy || !script">Start run</button>
      </form>
      <p v-if="error || runs.error.value" class="nbp-repl-error" role="alert">
        {{ error || runs.error.value?.message }}
      </p>
      <p v-else-if="runs.isPending.value" class="nbp-empty">Loading runs…</p>
      <p v-else-if="!runs.data.value?.length" class="nbp-empty">No recorded runs yet.</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full type-body">
          <thead class="type-small text-fg-subtle">
            <tr>
              <th>Run</th>
              <th>Status</th>
              <th>Script</th>
              <th>Inputs</th>
              <th>Environment</th>
              <th>Finished</th>
              <th>Outputs</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="run in runs.data.value" :key="run.id" class="border-b border-line">
              <td>{{ run.key }}</td>
              <td>
                {{ run.status
                }}<span
                  v-if="run.status === 'ok' && run.rerun_of && run.facts?.matches_original === true"
                  class="viz-meta"
                >
                  · matches original</span
                >
                <p v-if="run.failure_reason || run.facts?.error" class="run-error">
                  {{ run.failure_reason || run.facts?.error }}
                </p>
                <button
                  type="button"
                  class="quiet-action"
                  :aria-label="`Execution log ${run.key}`"
                  @click="logRun = logRun === run.key ? '' : run.key"
                >
                  Execution log
                </button>
                <div v-if="logRun === run.key" class="run-log" :aria-label="`Log ${run.key}`">
                  <pre v-if="run.facts?.stdout_tail">{{ run.facts.stdout_tail.slice(-16384) }}</pre>
                  <pre v-if="run.facts?.stderr_tail">{{ run.facts.stderr_tail.slice(-16384) }}</pre>
                  <p v-if="!run.facts?.stdout_tail && !run.facts?.stderr_tail" class="viz-meta">
                    No execution output
                  </p>
                </div>
              </td>
              <td>{{ path(run.script_file_id) }} v{{ run.script_n }}</td>
              <td>
                <span v-for="input in run.inputs ?? []" :key="input.file_id"
                  >{{ input.path }} v{{ input.n }}
                </span>
              </td>
              <td class="font-mono" :title="run.env_sha ?? undefined">
                {{ run.env_sha?.slice(0, 12) || 'Unavailable' }}
              </td>
              <td>{{ run.finished_at || '—' }}</td>
              <td>
                <button
                  type="button"
                  class="quiet-action"
                  :aria-label="`Outputs ${run.key}`"
                  @click="selected = run.key"
                >
                  Outputs</button
                ><template v-if="selected === run.key"
                  ><p v-if="detail.isPending.value" class="viz-meta">Loading…</p>
                  <p v-else-if="detail.error.value" class="nbp-repl-error">
                    {{ detail.error.value.message }}
                  </p>
                  <template v-else
                    ><button
                      v-for="output in detail.data.value?.outputs ?? []"
                      :key="`${output.file_id}:${output.n}`"
                      type="button"
                      class="quiet-action"
                      @click="emit('openFile', output)"
                    >
                      {{ output.path }} v{{ output.n }}</button
                    ><span v-if="!detail.data.value?.outputs?.length" class="viz-meta"
                      >No outputs</span
                    ></template
                  ></template
                >
              </td>
              <td>
                <button
                  v-if="canRerun(run)"
                  type="button"
                  class="quiet-action"
                  :aria-label="`Rerun ${run.key}`"
                  :disabled="busy"
                  @click="start(run)"
                >
                  Rerun
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>
<style scoped>
.run-error,
.run-log {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-width: 36rem;
}
.run-log pre {
  white-space: pre-wrap;
}
th,
td {
  padding: 12px 16px;
  text-align: left;
  vertical-align: top;
  white-space: nowrap;
}
th {
  font-weight: 400;
}
form {
  display: grid;
  gap: 12px;
}
label {
  display: grid;
  gap: 4px;
}
select {
  border: 1px solid var(--hairline);
  padding: 6px;
}
</style>
