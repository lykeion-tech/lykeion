<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { readFormatText } from '../format-text';
import { tableFromDelimited, TABLE_FULL_MAX_BYTES, TABLE_HEAD_BYTES } from '../csv-head';
import { langForPath } from '../tool-detail';
import CodeBlock from '../CodeBlock.vue';
import MarkdownViewer from './MarkdownViewer.vue';
import JsonViewer from './JsonViewer.vue';
import ImageViewer from './ImageViewer.vue';
import PdfDocumentPreview from './PdfDocumentPreview.vue';
import NotebookViewer from './NotebookViewer.vue';
import SequenceViewer from './SequenceViewer.vue';
import NpyViewer from './NpyViewer.vue';
import FitsViewer from './FitsViewer.vue';
import OfficeViewer from './OfficeViewer.vue';
import WorkbookViewer from './WorkbookViewer.vue';
import DicomViewer from './DicomViewer.vue';
import NiftiViewer from './NiftiViewer.vue';
import MatrixMarketViewer from './MatrixMarketViewer.vue';
import ChemicalViewer from './ChemicalViewer.vue';
import MassXmlViewer from './MassXmlViewer.vue';
import NpzViewer from './NpzViewer.vue';
import SpectrumViewer from './SpectrumViewer.vue';
import AlignmentTreeViewer from './AlignmentTreeViewer.vue';
import JsonLinesViewer from './JsonLinesViewer.vue';
import { scientificTable } from './scientific-text';
const props = defineProps<{ blob: Blob; path: string }>();
const emit = defineEmits<{ text: [text: string | undefined, partial: boolean] }>();
const text = ref<string>(),
  partial = ref(false),
  url = ref(''),
  pending = ref(true),
  error = ref('');
const ext = computed(() => props.path.split('.').pop()?.toLowerCase() ?? '');
const medicalImage = computed(
  () => ['dcm', 'dicom'].includes(ext.value) || props.blob.type === 'application/dicom',
);
const pdf = computed(() => ext.value === 'pdf');
const npy = computed(() => ext.value === 'npy');
const niftiGzip = computed(() => props.path.toLowerCase().endsWith('.nii.gz'));
const nifti = computed(() => ext.value === 'nii' || niftiGzip.value);
const fits = computed(() => ['fits', 'fit', 'fts'].includes(ext.value));
const office = computed(() =>
  ext.value === 'docx' || ext.value === 'pptx' ? ext.value : undefined,
);
const npz = computed(() => ext.value === 'npz');
const sequence = computed(() =>
  ['fasta', 'fa', 'faa', 'fna', 'ffn', 'fastq', 'fq'].includes(ext.value),
);
const alignmentTree = computed(() =>
  ['aln', 'clustal', 'sto', 'stk', 'phy', 'phylip', 'nwk', 'newick', 'tree', 'nhx'].includes(
    ext.value,
  ),
);
const bioTable = computed(() =>
  text.value === undefined ? undefined : scientificTable(text.value, ext.value, partial.value),
);
const image = computed(() =>
  ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico'].includes(ext.value),
);
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
        bmp: 'image/bmp',
        ico: 'image/x-icon',
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
      image.value
        ? new Blob([props.blob], { type: mime.value })
        : pdf.value
          ? new Blob([props.blob], { type: 'application/pdf' })
          : props.blob,
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
      if (
        pdf.value ||
        medicalImage.value ||
        nifti.value ||
        fits.value ||
        npy.value ||
        npz.value ||
        office.value ||
        ext.value === 'xlsx'
      )
        return;
      const got = await readFormatText(
        props.blob,
        ext.value === 'ipynb'
          ? 8 * 1024 * 1024
          : ['csv', 'tsv'].includes(ext.value)
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
  <DicomViewer v-else-if="medicalImage" :blob="blob" />
  <NiftiViewer v-else-if="nifti" :blob="blob" :compressed="niftiGzip" />
  <FitsViewer v-else-if="fits" :blob="blob" />
  <OfficeViewer v-else-if="office" :blob="blob" :format="office" />
  <WorkbookViewer v-else-if="ext === 'xlsx'" :blob="blob" />
  <NpyViewer v-else-if="npy" :blob="blob" />
  <NpzViewer v-else-if="npz" :blob="blob" />
  <PdfDocumentPreview v-else-if="pdf" :src="url" :path="path" />
  <ImageViewer v-else-if="image" :src="url" :path="path" />
  <div v-else-if="error || text === undefined" class="viewer-failure">
    <p class="prov-empty">{{ error || 'No preview for this file.' }}</p>
    <a class="quiet-action" :href="url" :download="path.split('/').pop()">Download</a>
  </div>
  <AlignmentTreeViewer v-else-if="alignmentTree" :text="text" :extension="ext" :partial="partial" />
  <SpectrumViewer
    v-else-if="['mgf', 'jdx', 'dx'].includes(ext)"
    :text="text"
    :extension="ext"
    :partial="partial"
  />
  <MassXmlViewer
    v-else-if="['mzml', 'mzxml'].includes(ext)"
    :text="text"
    :extension="ext"
    :partial="partial"
  />
  <ChemicalViewer
    v-else-if="['pdb', 'ent', 'sdf', 'mol', 'cif', 'mmcif'].includes(ext)"
    :text="text"
    :extension="ext"
    :partial="partial"
  />
  <MatrixMarketViewer v-else-if="ext === 'mtx'" :text="text" :partial="partial" />
  <SequenceViewer v-else-if="sequence" :text="text" :extension="ext" :partial="partial" />
  <figure
    v-else-if="bioTable"
    class="viz-figure artifact-table"
    aria-label="Scientific table preview"
  >
    <div v-if="bioTable.partial" class="viz-caption">First part only</div>
    <details v-if="bioTable.metadata.length" class="viz-caption">
      <summary>Header metadata</summary>
      <pre>{{ bioTable.metadata.join('\n') }}</pre>
    </details>
    <div class="table-scroll">
      <table class="data-table">
        <thead>
          <tr>
            <th v-for="(cell, i) in bioTable.header" :key="i">{{ cell }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, i) in bioTable.rows" :key="i">
            <td v-for="(_, j) in bioTable.header" :key="j">{{ row[j] ?? '' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </figure>
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
  <JsonLinesViewer v-else-if="['jsonl', 'ndjson'].includes(ext)" :text="text" :partial="partial" />
  <NotebookViewer v-else-if="ext === 'ipynb'" :text="text" :partial="partial" />
  <MarkdownViewer v-else-if="['md', 'markdown'].includes(ext)" :text="text" />
  <JsonViewer v-else-if="ext === 'json' && !partial" :text="text" />
  <div v-else class="artifact-text">
    <div v-if="partial" class="viz-caption">First part only</div>
    <CodeBlock :code="text" :lang="langForPath(path) || 'text'" line-numbers />
  </div>
</template>
<style scoped>
.artifact-table,
.artifact-text {
  margin: 0;
  min-width: 0;
}
.table-scroll {
  overflow: auto;
  max-width: 100%;
}
.data-table {
  border-collapse: collapse;
  width: 100%;
  font-size: 13px;
}
.data-table th,
.data-table td {
  border: 1px solid var(--border);
  padding: 6px 8px;
  text-align: left;
  white-space: nowrap;
}
.col-type {
  color: var(--muted);
  margin-right: 8px;
  font-size: 11px;
}
.viz-caption {
  color: var(--muted);
  padding: 6px 0;
  font-size: 12px;
}
.viewer-failure {
  padding: 12px;
}
</style>
