<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { filePath } from './api';
import { queuePreview } from './preview-queue';
import { readFormatText } from './format-text';
import type { FileRow } from './types';
const props = defineProps<{
  projectId: string;
  file: FileRow;
  list: boolean;
}>();
defineEmits<{ open: [file: FileRow] }>();
const ext = computed(() => props.file.path.split('.').pop()?.toLowerCase() ?? '');
const picture = computed(() =>
  ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico'].includes(ext.value),
);
const readable = computed(() =>
  ['csv', 'tsv', 'json', 'txt', 'md', 'py', 'r', 'js', 'ts'].includes(ext.value),
);
const element = ref<HTMLElement>();
const visible = ref(false);
let observer: IntersectionObserver | undefined;
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined' || !element.value) {
    visible.value = true;
    return;
  }
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries)
        if (entry.target === element.value) visible.value = entry.isIntersecting;
    },
    { rootMargin: '200px' },
  );
  observer.observe(element.value);
});
onUnmounted(() => observer?.disconnect());
const eligible = computed(
  () =>
    visible.value &&
    !props.list &&
    props.file.head.size <= (picture.value ? 4 * 1024 * 1024 : 262144) &&
    (picture.value || readable.value),
);
const preview = ref(''),
  url = ref('');
watch(
  () => [
    eligible.value,
    workspaceId.value,
    props.projectId,
    props.file.id,
    props.file.head.n,
    props.file.path,
  ],
  async (_, __, cleanup) => {
    preview.value = '';
    url.value = '';
    if (!eligible.value || !workspaceId.value) return;
    const controller = new AbortController();
    let object = '';
    let current = true;
    const ws = workspaceId.value,
      project = props.projectId,
      file = props.file.id,
      version = props.file.head.n;
    cleanup(() => {
      current = false;
      controller.abort();
      if (object) URL.revokeObjectURL(object);
    });
    try {
      const blob = await queuePreview(controller.signal, () =>
        api<Blob>(
          'GET',
          `${filePath(project, file)}/versions/${version}/content`,
          undefined,
          undefined,
          {
            signal: controller.signal,
            workspace: ws,
            response: 'blob',
            maxBytes: picture.value ? 4 * 1024 * 1024 : 262144,
          },
        ),
      );
      if (!current) return;
      if (picture.value) {
        const mime = ext.value === 'jpg' ? 'jpeg' : ext.value === 'ico' ? 'x-icon' : ext.value;
        object = URL.createObjectURL(new Blob([blob], { type: `image/${mime}` }));
        url.value = object;
      } else {
        const result = await readFormatText(blob, 4096);
        if (current) preview.value = result.text ?? '';
      }
    } catch {
      /* A failed thumbnail never blocks opening the file. */
    }
  },
  { immediate: true },
);
</script>
<template>
  <button
    ref="element"
    type="button"
    class="experiment-file"
    :class="{ 'is-list': list }"
    :title="file.path"
    :aria-label="`Open ${file.path.split('/').at(-1)}`"
    @click="$emit('open', file)"
  >
    <span v-if="!list" class="experiment-file-preview" aria-hidden="true">
      <img v-if="url" :src="url" alt="" @error="url = ''" />
      <span v-else-if="preview" class="experiment-file-text">{{ preview }}</span>
      <span v-else class="experiment-file-type">{{ ext || 'file' }}</span>
    </span>
    <span class="experiment-file-meta"
      ><span class="experiment-file-name">{{ file.path.split('/').at(-1) }}</span
      ><span class="experiment-file-date">{{
        new Date(file.head.created_at).toLocaleDateString()
      }}</span></span
    >
  </button>
</template>
<style scoped>
.experiment-file {
  background: var(--surface, #fff);
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  padding: 0;
  text-align: left;
  color: var(--text);
  cursor: pointer;
  min-width: 0;
}
.experiment-file:hover,
.experiment-file:focus-visible {
  border-color: var(--muted);
}
.experiment-file-preview {
  height: 112px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--fill-preview);
  overflow: hidden;
  border-bottom: 1px solid var(--border);
}
.experiment-file-preview img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.experiment-file-text {
  align-self: stretch;
  padding: 8px;
  white-space: pre-wrap;
  overflow: hidden;
  font: 8px/1.3 monospace;
  width: 100%;
  overflow-wrap: anywhere;
}
.experiment-file-type {
  text-transform: uppercase;
  font-size: 12px;
  color: var(--muted);
}
.experiment-file-meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px;
}
.experiment-file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}
.experiment-file-date {
  color: var(--muted);
  font-size: 13px;
}
.experiment-file.is-list .experiment-file-meta {
  flex-direction: row;
  justify-content: space-between;
}
</style>
