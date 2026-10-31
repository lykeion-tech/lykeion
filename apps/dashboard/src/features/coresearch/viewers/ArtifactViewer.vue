<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { readFormatText } from '../format-text';
import { tableFromDelimited, TABLE_FULL_MAX_BYTES, TABLE_HEAD_BYTES } from '../csv-head';
import { langForPath } from '../tool-detail';
import CodeBlock from '../CodeBlock.vue';
import MarkdownViewer from './MarkdownViewer.vue';
import JsonViewer from './JsonViewer.vue';
import ImageViewer from './ImageViewer.vue';
const props = defineProps<{ blob: Blob; path: string }>();
const emit = defineEmits<{ text: [text: string | undefined, partial: boolean] }>();
const text = ref<string>(),
  partial = ref(false),
  url = ref(''),
  pending = ref(true),
  error = ref('');
const ext = computed(() => props.path.split('.').pop()?.toLowerCase() ?? '');
const image = computed(() => ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext.value));
const mime = computed(
  () =>
    (
      ({
        svg: 'image/svg+xml',
        png: 'image/png',
        jpg: 'image/jpeg',
        jpeg: 'image/jpeg',
        gif: 'image/gif',
        webp: 'image/webp',
      }) as Record<string, string>
    )[ext.value],
);
const table = computed(() =>
  ['csv', 'tsv'].includes(ext.value) && text.value !== undefined
    ? tableFromDelimited(text.value, ext.value === 'tsv' ? '\t' : ',', partial.value)
    : undefined,
);
watch(
  () => [props.blob, props.path],
  async (_, __, cleanup) => {
    let live = true;
    const object = URL.createObjectURL(
      image.value ? new Blob([props.blob], { type: mime.value }) : props.blob,
    );
    url.value = object;
    cleanup(() => {
      live = false;
      URL.revokeObjectURL(object);
    });
    pending.value = true;
    error.value = '';
    text.value = undefined;
    emit('text', undefined, false);
    try {
      const got = await readFormatText(
        props.blob,
        ['csv', 'tsv'].includes(ext.value)
          ? props.blob.size <= TABLE_FULL_MAX_BYTES
            ? TABLE_FULL_MAX_BYTES
            : TABLE_HEAD_BYTES
          : undefined,
      );
      if (live) {
        text.value = got.text;
        partial.value = got.partial;
        emit('text', image.value ? undefined : got.text, got.partial);
      }
    } catch (e) {
      if (live) error.value = String(e);
    } finally {
      if (live) pending.value = false;
    }
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="pending" class="nbp-empty">Reading…</p>
  <ImageViewer v-else-if="image" :src="url" :path="path" />
  <div v-else-if="error || text === undefined" class="viewer-failure">
    <p class="prov-empty">{{ error || 'No preview for this file.' }}</p>
    <a class="quiet-action" :href="url" :download="path.split('/').pop()">Download</a>
  </div>
  <figure v-else-if="table" class="viz-figure artifact-table">
    <div v-if="partial" class="viz-caption"><span class="viz-meta">First part only</span></div>
    <div class="table-scroll">
      <table class="data-table">
        <thead>
          <tr>
            <th v-for="(cell, i) in table.header" :key="i">
              <span v-if="table.types" class="col-type" aria-hidden="true">{{
                table.types[i] === 'number' ? '123' : 'abc'
              }}</span
              ><span class="col-name">{{ cell }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in table.rows" :key="i">
            <td v-for="(_, j) in table.header" :key="j">{{ row[j] ?? '' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </figure>
  <MarkdownViewer v-else-if="['md', 'markdown'].includes(ext)" :text="text" />
  <JsonViewer v-else-if="ext === 'json' && !partial" :text="text" />
  <div v-else class="artifact-text">
    <div class="viz-caption">
      <span class="viz-meta"
        >{{ blob.type || 'text/plain' }}{{ partial ? ' · first part only' : '' }}</span
      >
    </div>
    <CodeBlock :code="text" :lang="langForPath(path) || 'text'" line-numbers />
  </div>
</template>
