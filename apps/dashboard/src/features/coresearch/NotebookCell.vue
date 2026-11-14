<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { chatPath, keys } from './api';
import type { CellRow, CellOutput as Output } from './types';
import { outputsOf } from './notebook-model';
import CodeBlock from './CodeBlock.vue';
import CellOutput from './CellOutput.vue';
const props = defineProps<{ projectId: string; chatId: string; cell: CellRow }>();
const open = ref(['error', 'failed'].includes(props.cell.status));
const blob = useQuery({
  queryKey: computed(() =>
    keys.coresearchCellOutputs(workspaceId.value, props.projectId, props.chatId, props.cell.id),
  ),
  enabled: computed(() => open.value && !!props.cell.outputs_blob),
  queryFn: ({ signal }) =>
    api<Output[]>(
      'GET',
      `${chatPath(props.projectId, props.chatId)}/cells/${encodeURIComponent(props.cell.id)}/outputs`,
      undefined,
      undefined,
      { signal, workspace: workspaceId.value },
    ),
});
const outputs = computed(() =>
  outputsOf(props.cell.outputs_blob ? blob.data.value : props.cell.outputs),
);
const hasOutput = computed(() => !!props.cell.outputs_blob || outputs.value.length > 0);
</script>
<template>
  <div
    :id="`cell-${cell.id}`"
    class="nbp-cell"
    :class="{ 'nbp-cell--err': ['error', 'failed'].includes(cell.status) }"
  >
    <CodeBlock
      :code="cell.source"
      :lang="cell.kernel === 'python3' ? 'python' : cell.kernel"
      line-numbers
    >
      <template #meta
        ><span class="nbp-cell-meta"
          ><span class="nbp-gutter">In [{{ cell.execution_count ?? ' ' }}]</span
          ><span class="nbp-cell-lang">{{ cell.kernel }}</span
          ><span class="nbp-cell-by"
            >{{ cell.agent_run_id ? 'agent' : 'repl' }} · {{ cell.status }}</span
          ></span
        ></template
      >
    </CodeBlock>
    <p v-if="cell.failure_reason" class="nbp-warning">{{ cell.failure_reason }}</p>
    <div v-if="hasOutput" class="nbp-output-wrap">
      <button type="button" class="nbp-output-summary" :aria-expanded="open" @click="open = !open">
        {{ open ? 'Hide output' : 'Show output' }}
      </button>
      <div v-if="open" class="nbp-outputs">
        <p v-if="cell.outputs_blob && blob.isPending.value" class="nbp-loading" role="status">
          Loading output…
        </p>
        <p v-else-if="blob.error.value" class="nbp-warning" role="alert">
          {{ blob.error.value.message }}
        </p>
        <CellOutput v-for="(output, i) in outputs" :key="i" :output="output" />
      </div>
    </div>
  </div>
</template>
<style scoped>
.nbp-cell-meta {
  flex-wrap: wrap;
}
</style>
