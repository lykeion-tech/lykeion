<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { readFormatText } from '../coresearch/format-text';
import { tableFromDelimited } from '../coresearch/csv-head';
import { queueThumbnail } from './study-thumbnail-queue';
const props = defineProps<{ name: string; size?: number; url: string }>();
const text = ref('');
const image = ref('');
const table = ref<{
  header: string[];
  types?: readonly string[];
  rows?: number;
  columns: number;
}>();
let active = true;
const thumbnail = ref<HTMLElement>();
const controller = new AbortController();
let observer: IntersectionObserver | undefined;
const ext = props.name.split('.').pop()?.toLowerCase() ?? '';
const types: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
};
const textTypes = [
  'csv',
  'tsv',
  'txt',
  'json',
  'md',
  'py',
  'r',
  'js',
  'ts',
  'fasta',
  'fa',
  'fastq',
  'fq',
];
async function load() {
  // Only bounded files are fetched for thumbnails; open larger files to preview them.
  if (
    props.size === undefined ||
    props.size > 512 * 1024 ||
    (!types[ext] && !textTypes.includes(ext))
  )
    return;
  const workspace = workspaceId.value;
  try {
    const value = await api<Blob>('GET', props.url, undefined, undefined, {
      workspace,
      response: 'blob',
      signal: controller.signal,
      maxBytes: 512 * 1024,
    });
    if (!active || workspaceId.value !== workspace) return;
    if (types[ext]) image.value = URL.createObjectURL(new Blob([value], { type: types[ext] }));
    else {
      const result = await readFormatText(value, 2048);
      if (active && workspaceId.value === workspace && result.text !== undefined) {
        if (ext === 'csv' || ext === 'tsv') {
          const parsed = tableFromDelimited(
            result.text,
            ext === 'tsv' ? '\t' : ',',
            result.partial,
          );
          table.value = {
            header: parsed.header.slice(0, 5),
            columns: parsed.header.length,
            types: parsed.types,
            ...(!result.partial ? { rows: parsed.rows.length } : {}),
          };
        } else text.value = result.text;
      }
    }
  } catch {
    /* Opening a file shows its error and retry control. */
  }
}
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined' || !thumbnail.value) {
    void queueThumbnail(load, controller.signal);
    return;
  }
  observer = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      observer?.disconnect();
      void queueThumbnail(load, controller.signal);
    }
  });
  observer.observe(thumbnail.value);
});
onUnmounted(() => {
  active = false;
  observer?.disconnect();
  controller.abort();
  if (image.value) URL.revokeObjectURL(image.value);
});
</script>
<template>
  <span ref="thumbnail" class="thumbnail" aria-hidden="true">
    <img v-if="image" :src="image" alt="" />
    <span v-else-if="table" class="columns">
      <small v-if="table.rows !== undefined"
        >{{ table.rows }} rows · {{ table.columns }} columns</small
      >
      <span v-for="(column, i) in table.header" :key="i"
        ><em>{{ table.types ? (table.types[i] === 'number' ? '123' : 'abc') : '' }}</em
        >{{ column }}</span
      >
    </span>
    <pre v-else-if="text">{{ text }}</pre>
    <span v-else class="extension">{{ ext.toUpperCase().slice(0, 8) || 'FILE' }}</span>
  </span>
</template>
<style scoped>
.thumbnail {
  height: 110px;
  width: 100%;
  display: grid;
  place-items: center;
  overflow: hidden;
  border-bottom: 1px solid var(--border);
  color: var(--muted);
  background: var(--fill-preview);
}
.thumbnail img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.thumbnail pre {
  width: 100%;
  height: 100%;
  margin: 0;
  padding: 8px;
  box-sizing: border-box;
  overflow: hidden;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 7px;
  line-height: 1.35;
  color: var(--text);
}
.columns {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 8px;
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  overflow: hidden;
  font-size: 10px;
  color: var(--text);
}
.columns small {
  color: var(--muted);
}
.columns > span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.columns em {
  display: inline-block;
  width: 28px;
  font-style: normal;
  color: var(--muted);
}
.extension {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.03em;
}
</style>
