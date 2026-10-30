<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import { api } from '../api/client';
import { useKindRecords } from '../api/queries';
import { workspaceId } from '../api/session';
import { formatSize } from '../lib/attachments';
import { columnName } from '../lib/studies';
import { notify } from '../state/notice';
import type { StudyRecord } from '../api/types';
const props = defineProps<{ cardId: string }>();
const outputs = useKindRecords(() => props.cardId, 'output');
const downloading = ref('');
let generation = 0;
watch(
  [workspaceId, () => props.cardId],
  () => {
    generation++;
    downloading.value = '';
  },
  { flush: 'sync' },
);
onUnmounted(() => {
  generation++;
});
const name = (record: StudyRecord) =>
  typeof record.body.name === 'string' ? record.body.name : record.key;
async function download(record: StudyRecord) {
  const workspace = workspaceId.value,
    card = props.cardId,
    visit = generation;
  downloading.value = record.id;
  const current = () =>
    generation === visit && workspaceId.value === workspace && props.cardId === card;
  try {
    const blob = await api<Blob>(
      'GET',
      `/api/cards/${encodeURIComponent(card)}/outputs/${encodeURIComponent(record.key)}`,
      undefined,
      undefined,
      { workspace, response: 'blob' },
    );
    if (!current()) return;
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = name(record).split('/').pop() || record.key;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) {
    if (current()) notify(`${name(record)}: ${(error as Error).message}`);
  } finally {
    if (current()) downloading.value = '';
  }
}
</script>
<template>
  <section class="study-outputs" aria-label="Files">
    <h2>Files</h2>
    <p v-if="outputs.isPending.value" role="status">Loading files…</p>
    <p v-else-if="outputs.isError.value" role="alert">
      {{ outputs.error.value?.message }} <button @click="outputs.refetch()">Retry</button>
    </p>
    <p v-else-if="!outputs.data.value?.length" class="empty">
      No files yet. Files saved by agents will appear here.
    </p>
    <ul v-else>
      <li v-for="record in outputs.data.value" :key="record.id">
        <button :disabled="!!downloading" @click="download(record)">{{ name(record) }}</button>
        <small
          >{{ columnName(record.board_column) }} · Round {{ record.round }} · {{ record.role
          }}<template v-if="record.agent_run_id"> · {{ record.agent_run_id }}</template></small
        >
        <small
          >{{ new Date(record.created_at).toLocaleString()
          }}<template v-if="typeof record.body.size_bytes === 'number'">
            · {{ formatSize(record.body.size_bytes) }}</template
          ></small
        >
      </li>
    </ul>
  </section>
</template>
<style scoped>
.study-outputs {
  padding: 16px;
}
h2 {
  font-size: 16px;
  margin: 0 0 16px;
}
.empty,
small {
  color: var(--muted);
}
ul {
  list-style: none;
  padding: 0;
  margin: 0;
}
li {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 3px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
  overflow-wrap: anywhere;
}
li button {
  text-align: left;
  color: inherit;
  cursor: pointer;
}
</style>
