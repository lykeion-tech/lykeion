<script setup lang="ts">
import { downloadFile } from '../../api/client';
import { workspaceId } from '../../api/session';
import type { Comment } from '../../api/types';
import { formatSize } from '../../lib/attachments';
const props = defineProps<{ comment: Comment; studyId: string }>();
function download(id: string) {
  downloadFile(
    `/api/cards/${encodeURIComponent(props.studyId)}/comments/${encodeURIComponent(props.comment.id)}/attachments/${encodeURIComponent(id)}`,
    workspaceId.value,
  );
}
</script>
<template>
  <ul v-if="comment.attachments?.length" class="comment-files" aria-label="Comment attachments">
    <li v-for="file in comment.attachments" :key="file.id">
      <button type="button" @click="download(file.id)">{{ file.name }}</button>
      <span>{{ formatSize(file.size_bytes) }}</span>
    </li>
  </ul>
</template>
<style scoped>
.comment-files {
  list-style: none;
  padding: 0;
  margin: 8px 0 0;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.comment-files li {
  display: flex;
  gap: 6px;
  padding: 5px 8px;
  border: 1px solid var(--border);
  border-radius: 5px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.comment-files button {
  color: var(--primary);
  text-align: left;
}
.comment-files span {
  color: var(--muted);
}
</style>
