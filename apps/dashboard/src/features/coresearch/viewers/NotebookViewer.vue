<script setup lang="ts">
import { computed } from 'vue';
import { notebookPreview } from './notebook-preview';
import MarkdownViewer from './MarkdownViewer.vue';
import CodeBlock from '../CodeBlock.vue';
const props = defineProps<{ text: string; partial: boolean }>();
const notebook = computed(() => (props.partial ? undefined : notebookPreview(props.text)));
</script>
<template>
  <div v-if="notebook" class="notebook-preview" aria-label="Jupyter notebook">
    <article v-for="(cell, i) in notebook.cells" :key="i" class="notebook-preview-cell">
      <MarkdownViewer v-if="cell.type === 'markdown'" :text="cell.source" />
      <CodeBlock
        v-else
        :code="cell.source"
        :lang="cell.type === 'code' ? notebook.language : 'text'"
        line-numbers
      />
      <template v-for="(output, j) in cell.outputs" :key="j">
        <img v-if="output.kind === 'image'" :src="output.src" alt="Notebook output" />
        <div v-else-if="output.kind === 'table'" class="notebook-table">
          <table>
            <tbody>
              <tr v-for="(row, k) in output.rows" :key="k">
                <td v-for="(value, n) in row" :key="n">{{ value }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <pre v-else class="notebook-output">{{ output.text }}</pre>
      </template>
    </article>
    <p v-if="!notebook.cells.length">This notebook has no cells.</p>
  </div>
  <p v-else role="status">This notebook is invalid or too large to preview.</p>
</template>
<style scoped>
.notebook-preview-cell {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 12px;
  overflow: hidden;
}
.notebook-preview img {
  display: block;
  max-width: 100%;
  height: auto;
}
.notebook-table {
  overflow: auto;
  padding: 12px;
}
.notebook-table td {
  border: 1px solid var(--border);
  padding: 4px 8px;
}
.notebook-output {
  padding: 12px;
  overflow: auto;
  white-space: pre-wrap;
}
</style>
