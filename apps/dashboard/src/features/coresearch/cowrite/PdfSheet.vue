<script setup lang="ts">
import { ref, watchEffect } from 'vue';
import type { PDFDocumentProxy, TextLayer } from 'pdfjs-dist';
import { loadPdfjs, pageLinks, rasterWidth, type PageLink } from './lib/pdf-render';
const props = defineProps<{
  n: number;
  path: string;
  doc: PDFDocumentProxy;
  widthPx?: number;
  size: { width: number; height: number };
  near: boolean;
}>();
const emit = defineEmits<{ drawn: [n: number, height: number]; failure: [err: unknown] }>();
const canvas = ref<HTMLCanvasElement>(),
  text = ref<HTMLElement>(),
  links = ref<PageLink[]>([]);
let drawnAt: number | undefined;
watchEffect((onCleanup) => {
  const doc = props.doc,
    n = props.n,
    width = props.widthPx,
    near = props.near,
    c = canvas.value,
    container = text.value;
  if (!near || !c || (drawnAt === width && width !== undefined)) return;
  let cancelled = false,
    renderTask: { cancel: () => void } | undefined,
    layer: TextLayer | undefined;
  onCleanup(() => {
    cancelled = true;
    renderTask?.cancel();
    layer?.cancel();
  });
  void (async () => {
    try {
      const pdfjs = await loadPdfjs(),
        drawing = await doc.getPage(n);
      if (cancelled) return;
      const base = drawing.getViewport({ scale: 1 }),
        shown = c.parentElement?.clientWidth || base.width,
        dpr = window.devicePixelRatio || 1;
      const viewport = drawing.getViewport({ scale: rasterWidth(shown, dpr) / base.width });
      c.width = viewport.width;
      c.height = viewport.height;
      const task = drawing.render({
        canvas: c,
        viewport,
        annotationMode: pdfjs.AnnotationMode.DISABLE,
      });
      renderTask = task;
      await task.promise;
      if (cancelled) return;
      const onScreen = drawing.getViewport({ scale: shown / base.width });
      if (container) {
        try {
          container.replaceChildren();
          container.style.setProperty('--scale-factor', String(onScreen.scale));
          const content = await drawing.getTextContent();
          if (cancelled) return;
          layer = new pdfjs.TextLayer({
            textContentSource: content,
            container,
            viewport: onScreen,
          });
          await layer.render();
          if (cancelled) return;
        } catch {
          if (cancelled) return;
          container.replaceChildren();
        }
      }
      const found = await pageLinks(doc, n, onScreen);
      if (cancelled) return;
      links.value = found;
      drawnAt = width;
      emit('drawn', n, base.height);
    } catch (e) {
      if (!cancelled) emit('failure', e);
    }
  })();
});
</script>
<template>
  <div
    class="pdf-page"
    :data-page="n"
    :style="{
      width: widthPx === undefined ? undefined : `${widthPx}px`,
      aspectRatio: `${size.width} / ${size.height}`,
    }"
  >
    <canvas ref="canvas" class="pdf-canvas" :aria-label="`Page ${n} of ${path}`" />
    <div ref="text" class="pdf-text-layer" />
    <slot /><a
      v-for="(link, i) in links"
      :key="i"
      class="pdf-link"
      :href="link.href"
      target="_blank"
      rel="noopener noreferrer"
      :style="{
        left: `${link.left * 100}%`,
        top: `${link.top * 100}%`,
        width: `${link.width * 100}%`,
        height: `${link.height * 100}%`,
      }"
    />
  </div>
</template>
