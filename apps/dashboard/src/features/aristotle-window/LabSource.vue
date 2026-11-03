<script setup lang="ts">
import { computed } from 'vue';
import { useCard, useComments, useNotebookRecords } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { labRecordTitle } from './api';

const props = defineProps<{ study: string; citation: string }>();
const kind = computed(() => props.citation.split(':')[0]);
const id = computed(() => props.citation.split(':')[1]);
const recordStudy = computed(() => (kind.value === 'record' ? props.study : ''));
const study = useCard(recordStudy),
  records = useNotebookRecords(recordStudy);
const comments = useComments(() => (kind.value === 'comment' ? props.study : ''));
const queries = computed(() => (kind.value === 'record' ? [study, records] : [comments]));
const loading = computed(() => queries.value.some((q) => q.isFetching.value));
const failed = computed(() => queries.value.some((q) => q.isError.value));
function retry() {
  queries.value.forEach((q) => void q.refetch());
}
const source = computed(() => {
  if (kind.value === 'comment') {
    const row = comments.data.value?.find((c) => c.id === id.value);
    return row && { title: 'Comment', text: row.body };
  }
  const row = records.data.value?.find((r) => r.id === id.value);
  if (!row) return;
  const title = labRecordTitle(row, study.data.value, workspaceId.value);
  return title ? { title, text: JSON.stringify(row.body, null, 2) } : undefined;
});
</script>
<template>
  <aside class="source" aria-label="Cited source">
    <RouterLink class="dismiss" :to="'/studies/' + props.study" aria-label="Close cited source"
      >×</RouterLink
    >
    <small>Cited source</small>
    <p v-if="loading">Loading source…</p>
    <button v-else-if="failed" @click="retry">Couldn’t load source · Retry</button>
    <template v-else-if="source"
      ><strong>{{ source.title }}</strong>
      <pre>{{ source.text }}</pre>
    </template>
    <p v-else>This source is no longer available.</p>
  </aside>
</template>
<style scoped>
.source {
  margin: 12px 18px;
  padding: 12px 14px;
  max-height: 35vh;
  overflow: auto;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 9px;
  color: var(--text);
}
small,
strong {
  display: block;
}
small {
  color: var(--muted);
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.dismiss {
  float: right;
  color: var(--muted);
  padding: 0 5px;
}
</style>
