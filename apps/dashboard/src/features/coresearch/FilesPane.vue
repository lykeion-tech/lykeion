<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError, downloadFile, PreviewTooLargeError } from '../../api/client';
import { workspaceId } from '../../api/session';
import {
  keys,
  filePath,
  useCoresearchVersions,
  useCoresearchContent,
  useCoresearchFiles,
} from './api';
import type { FileRow, FileVersion, ProjectRow } from './types';
import { boundedUnifiedDiff, DIFF_MAX_BYTES } from './diff';
import { readFormatText } from './format-text';
import ArtifactViewer from './viewers/ArtifactViewer.vue';
import CodeBlock from './CodeBlock.vue';
import ProvenancePanel from './ProvenancePanel.vue';
const props = defineProps<{
  project: ProjectRow;
  file?: FileRow;
  selectedN?: number;
  inWorkspaceTab?: boolean;
}>();
const emit = defineEmits<{ 'update:selectedN': [number | undefined] }>();
const client = useQueryClient(),
  files = useCoresearchFiles(() => props.project.id);
const versions = useCoresearchVersions(
  () => props.project.id,
  () => props.file?.id ?? '',
);
const pin = ref<number | undefined>(props.selectedN),
  busy = ref(false),
  error = ref('');
const tab = ref('preview'),
  diffing = ref(false),
  comparePin = ref<number>(),
  picker = ref(false),
  previewText = ref<string>(),
  partial = ref(false),
  oldText = ref<string>(),
  oldReading = ref(false);
const tabs = [
  { id: 'preview', label: 'Preview' },
  { id: 'code', label: 'Code' },
  { id: 'log', label: 'Execution Log' },
  { id: 'transcript', label: 'Transcript' },
];
const chain = computed(() => [...(versions.data.value ?? [])].sort((a, b) => a.n - b.n));
const head = computed(() =>
  Math.max(
    files.data.value?.find((f) => f.id === props.file?.id)?.head.n ?? props.file?.head.n ?? 0,
    chain.value.at(-1)?.n ?? 0,
  ),
);
const shownN = computed(() => pin.value ?? head.value);
const version = computed(
  () =>
    chain.value.find((v) => v.n === shownN.value) ??
    (props.file?.head.n === shownN.value ? props.file.head : undefined),
);
const earlier = computed(() => chain.value.filter((v) => v.n < shownN.value));
const comparison = computed(
  () => earlier.value.find((v) => v.n === comparePin.value) ?? earlier.value.at(-1),
);
const PREVIEW_MAX_BYTES = 32 * 1024 * 1024;
const scientificBinary = computed(() => /\.(?:fits|fit|fts|npy)$/i.test(props.file?.path ?? ''));
const previewMaxBytes = computed(() =>
  scientificBinary.value ? 50 * 1024 * 1024 : PREVIEW_MAX_BYTES,
);
const metadataTooLarge = computed(
  () => !!version.value && version.value.size > previewMaxBytes.value,
);
const content = useCoresearchContent(
  () => props.project.id,
  () => (version.value && !metadataTooLarge.value ? (props.file?.id ?? '') : ''),
  () => shownN.value,
  () => previewMaxBytes.value,
);
const tooLarge = computed(
  () => metadataTooLarge.value || content.error.value instanceof PreviewTooLargeError,
);
const compareContent = useCoresearchContent(
  () => props.project.id,
  () =>
    diffing.value &&
    (comparison.value?.size ?? 0) <= DIFF_MAX_BYTES &&
    (version.value?.size ?? 0) <= DIFF_MAX_BYTES
      ? (props.file?.id ?? '')
      : '',
  () => comparison.value?.n ?? 0,
);
function download() {
  if (!props.file) return;
  downloadFile(
    `${filePath(props.project.id, props.file.id)}/versions/${shownN.value}/content`,
    workspaceId.value,
  );
}
watch(
  () => props.selectedN,
  (n) => {
    pin.value = n;
  },
);
watch(shownN, () => {
  comparePin.value = undefined;
  previewText.value = undefined;
  partial.value = false;
});
watch(
  () => compareContent.data.value,
  async (blob, _, cleanup) => {
    let live = true;
    cleanup(() => (live = false));
    oldText.value = undefined;
    oldReading.value = !!blob;
    if (blob) {
      const read = await readFormatText(blob, DIFF_MAX_BYTES + 1);
      if (live) {
        if (!read.partial) oldText.value = read.text;
        oldReading.value = false;
      }
    }
  },
  { immediate: true },
);
const diff = computed(() => {
  if (!comparison.value) return { message: 'No earlier version to compare.' };
  if ((version.value?.size ?? 0) > DIFF_MAX_BYTES || comparison.value.size > DIFF_MAX_BYTES)
    return { message: 'Too large to compare.' };
  if (compareContent.error.value) return { message: 'Could not read the comparison.' };
  if (compareContent.isPending.value || oldReading.value || previewText.value === undefined)
    return { message: 'Reading comparison…' };
  if (oldText.value === undefined) return { message: 'These versions cannot be compared as text.' };
  const result = boundedUnifiedDiff(oldText.value, previewText.value);
  return result.kind === 'diff' ? { text: result.text } : { message: 'Too large to compare.' };
});
function refresh(ws: string, id: string) {
  void client.invalidateQueries({ queryKey: keys.coresearchFiles(ws, id) });
  void client.invalidateQueries({
    queryKey: keys.coresearchVersions(ws, id, props.file?.id ?? ''),
  });
}
async function restore() {
  if (!props.file || busy.value) return;
  const ws = workspaceId.value,
    id = props.project.id;
  busy.value = true;
  error.value = '';
  try {
    const saved = await api<FileVersion>(
      'POST',
      `${filePath(id, props.file.id)}/restore`,
      { n: shownN.value, base: head.value },
      undefined,
      { workspace: ws },
    );
    client.setQueryData<FileVersion[]>(keys.coresearchVersions(ws, id, props.file.id), (old) => [
      saved,
      ...(old ?? []),
    ]);
    pin.value = undefined;
    if (props.inWorkspaceTab) emit('update:selectedN', undefined);
    refresh(ws, id);
  } catch (e) {
    error.value =
      e instanceof ApiError && e.body.error === 'stale_base'
        ? `version ${e.body.base} is not the latest (${e.body.current} is)`
        : String(e);
  } finally {
    busy.value = false;
  }
}
function step(n: number | undefined) {
  pin.value = n;
  if (props.inWorkspaceTab) emit('update:selectedN', n);
  picker.value = false;
}
</script>
<template>
  <section class="artifact-pane" role="tabpanel" aria-label="Files" data-testid="artifact-pane">
    <p v-if="!file" class="nbp-empty">Choose a saved file from Files.</p>
    <template v-else
      ><header class="art-header">
        <span class="art-title" :title="file.path">{{ file.path.split('/').pop() }}</span
        ><span v-if="chain.length > 1" class="art-stepper"
          ><button
            type="button"
            class="art-icon-btn"
            aria-label="Previous version"
            :disabled="!earlier.length"
            @click="step(earlier.at(-1)?.n)"
          >
            ‹</button
          ><span class="art-version-label">v{{ shownN }}{{ shownN === head ? '' : ' (old)' }}</span
          ><button
            type="button"
            class="art-icon-btn"
            aria-label="Next version"
            :disabled="shownN === head"
            @click="
              step(
                chain.find((v) => v.n > shownN)?.n === head
                  ? undefined
                  : chain.find((v) => v.n > shownN)?.n,
              )
            "
          >
            ›
          </button></span
        >
        <template v-if="previewText !== undefined && chain.length > 1 && tab === 'preview'"
          ><button
            type="button"
            class="art-icon-btn art-diff-toggle"
            :class="{ 'is-on': diffing }"
            :aria-expanded="picker"
            @click="diffing ? (picker = !picker) : (diffing = true)"
          >
            {{
              diffing
                ? comparison
                  ? `Comparing to v${comparison.n}`
                  : 'Comparing — nothing earlier'
                : 'Show changes'
            }}</button
          ><button
            v-if="diffing"
            type="button"
            class="art-icon-btn art-diff-exit"
            @click="
              diffing = false;
              picker = false;
            "
          >
            Hide changes
          </button>
          <div v-if="picker" class="art-compare-list" role="listbox" aria-label="Compare against">
            <button
              v-for="v in [...earlier].reverse()"
              :key="v.n"
              type="button"
              role="option"
              class="art-compare-option"
              :aria-selected="v.n === comparison?.n"
              :disabled="v.size > DIFF_MAX_BYTES"
              @click="
                comparePin = v.n;
                picker = false;
              "
            >
              Version {{ v.n }}{{ v.size > DIFF_MAX_BYTES ? ' · Too large to compare' : '' }}
            </button>
          </div></template
        >
        <button type="button" class="quiet-action art-download" @click="download">Download</button>
      </header>
      <div class="artifact-pane-body">
        <p
          v-if="versions.error.value || (content.error.value && !tooLarge)"
          class="nbp-repl-error"
          role="alert"
        >
          {{ versions.error.value?.message || content.error.value?.message }}
        </p>
        <p v-else-if="!version || (!content.data.value && !tooLarge)" class="nbp-empty">
          {{
            versions.isPending.value || content.isPending.value
              ? 'Loading…'
              : 'This saved version is unavailable.'
          }}
        </p>
        <div v-else class="viz-root">
          <div class="viz-toolbar">
            <div class="viz-tabs" role="tablist" aria-label="Artifact">
              <button
                v-for="t in tabs"
                :key="t.id"
                type="button"
                role="tab"
                class="prov-tab"
                :class="{ 'is-active': tab === t.id }"
                :aria-selected="tab === t.id"
                @click="tab = t.id"
              >
                {{ t.label }}
              </button>
            </div>
            <div class="viz-actions">
              <button
                v-if="shownN < head && !project.archived_at"
                type="button"
                class="quiet-action"
                :disabled="busy"
                @click="restore"
              >
                Restore this version
              </button>
            </div>
          </div>
          <div class="viz-body">
            <template v-if="tab === 'preview'"
              ><div v-if="tooLarge" class="nbp-empty">This file is too large to preview.</div>
              <div v-else-if="diffing" data-testid="artifact-diff">
                <p v-if="diff.message" class="nbp-empty">{{ diff.message }}</p>
                <CodeBlock v-else :code="diff.text ?? ''" lang="diff" />
              </div>
              <div v-if="!tooLarge && content.data.value" :hidden="diffing">
                <ArtifactViewer
                  :key="`${file.id}:${shownN}`"
                  :blob="content.data.value"
                  :path="file.path"
                  @text="
                    (text, isPartial) => {
                      previewText = text;
                      partial = isPartial;
                    }
                  "
                /></div
            ></template>
            <ProvenancePanel
              v-else
              :key="`${file.id}:${shownN}`"
              :project="project"
              :version="version"
              :tab="tab"
            />
            <p v-if="error" role="alert" class="nbp-repl-error">{{ error }}</p>
          </div>
        </div>
      </div></template
    >
  </section>
</template>
<style scoped>
.art-header {
  position: relative;
  flex-wrap: wrap;
}
.artifact-pane .art-title {
  font:
    700 13px/normal 'Inter Variable',
    Inter,
    -apple-system,
    BlinkMacSystemFont,
    'Segoe UI',
    sans-serif;
}
.artifact-pane .art-download,
.artifact-pane .art-download:hover:not(:disabled) {
  margin-left: auto;
  color: var(--file-download-accent);
}
.artifact-pane .art-download {
  font:
    14px/normal 'Inter Variable',
    Inter,
    -apple-system,
    BlinkMacSystemFont,
    'Segoe UI',
    sans-serif;
  padding: 1px 6px;
}
.art-compare-list {
  position: absolute;
  top: 100%;
  right: 0;
  z-index: 5;
  background: var(--fill-menu);
}
</style>
