<script setup lang="ts">
import { ref } from 'vue';
import PdfViewer, { type PdfView } from '../cowrite/PdfViewer.vue';
defineProps<{ src: string; path: string }>();
const viewer = ref<InstanceType<typeof PdfViewer>>();
const view = ref<PdfView>({ page: 1, pages: 0, zoom: 1, fit: true });
</script>
<template>
  <div class="document-pdf">
    <PdfViewer ref="viewer" :src="src" :path="path" :max-pages="300" @view="view = $event">
      <button
        type="button"
        aria-label="Previous page"
        :disabled="view.page <= 1"
        @click="viewer?.goToPage(view.page - 1)"
      >
        ‹
      </button>
      <span>{{ view.page }} / {{ view.pages }}</span>
      <button
        type="button"
        aria-label="Next page"
        :disabled="view.page >= view.pages"
        @click="viewer?.goToPage(view.page + 1)"
      >
        ›
      </button>
      <button type="button" aria-label="Zoom out" @click="viewer?.zoomBy(-1)">−</button>
      <button type="button" aria-label="Zoom in" @click="viewer?.zoomBy(1)">+</button>
      <button type="button" @click="viewer?.setZoom('fit')">Fit</button>
    </PdfViewer>
    <a class="pdf-download" :href="src" :download="path.split('/').pop()">Download PDF</a>
  </div>
</template>
<style scoped>
.document-pdf {
  height: min(75vh, 800px);
  min-height: 320px;
  display: flex;
  flex-direction: column;
}
.document-pdf .pdf-download {
  align-self: flex-end;
  padding: 6px 0;
  font-size: 12px;
}
.document-pdf :deep(.artifact-pdf) {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  margin: 0;
}
.document-pdf :deep(.pdf-stage) {
  overflow: auto;
  flex: 1;
  min-height: 0;
  background: var(--bg-subtle, #eee);
}
.document-pdf :deep(.pdf-page) {
  position: relative;
  margin: 12px auto;
  background: white;
}
.document-pdf :deep(.pdf-canvas) {
  display: block;
  width: 100%;
  height: auto;
}
.document-pdf :deep(.pdf-link) {
  position: absolute;
}
.document-pdf :deep(.pdf-text-layer .markedContent) {
  display: contents;
}
.document-pdf :deep(.pdf-text-layer) {
  position: absolute;
  inset: 0;
  overflow: hidden;
  opacity: 1;
  line-height: 1;
  text-size-adjust: none;
  --total-scale-factor: var(--scale-factor, 1);
  --text-scale-factor: calc(var(--total-scale-factor) * var(--min-font-size, 1));
}
.document-pdf :deep(.pdf-text-layer span),
.document-pdf :deep(.pdf-text-layer br) {
  color: transparent;
  position: absolute;
  white-space: pre;
  cursor: text;
  transform-origin: 0 0;
  user-select: text;
}
.document-pdf :deep(.pdf-text-layer span) {
  font-size: calc(var(--text-scale-factor) * var(--font-height, 0));
  transform: rotate(var(--rotate, 0deg)) scaleX(var(--scale-x, 1))
    scale(calc(1 / var(--min-font-size, 1)));
}
.document-pdf :deep(.pdf-text-layer ::selection) {
  background: color-mix(in srgb, AccentColor, transparent 50%);
  color: transparent;
}
.document-pdf :deep(.pdf-pager) {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 12px;
  padding: 8px;
}
</style>
