<script setup lang="ts">
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { workspaceId } from '../../api/session';
import { useCoresearchFiles, useCoresearchProject } from './api';
import FilesPane from './FilesPane.vue';
import './l2.css';
const props = defineProps<{ projectId: string; fileId: string }>();
const route = useRoute();
const router = useRouter();
const project = useCoresearchProject(() => props.projectId);
const files = useCoresearchFiles(() => props.projectId);
const file = computed(() => files.data.value?.find((item) => item.id === props.fileId));
const selectedN = computed(() => {
  const n = Number(route.query.version);
  return Number.isSafeInteger(n) && n > 0 ? n : undefined;
});
function selectVersion(n: number | undefined) {
  if (n === selectedN.value) return;
  void router.replace({
    query: { ...route.query, version: n === undefined ? undefined : String(n) },
  });
}
</script>
<template>
  <section class="coresearch coresearch-file-view" aria-label="File">
    <p v-if="project.error.value || files.error.value" role="alert">Could not open this file.</p>
    <p v-else-if="project.isPending.value || files.isPending.value">Loading file…</p>
    <p v-else-if="!file || !project.data.value">This file is unavailable.</p>
    <FilesPane
      v-else
      :key="`${workspaceId}:${projectId}:${fileId}`"
      :project="project.data.value"
      :file="file"
      :selected-n="selectedN"
      @update:selected-n="selectVersion"
      in-workspace-tab
    />
  </section>
</template>
<style scoped>
.coresearch-file-view {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: auto;
  background: var(--surface);
}
</style>
