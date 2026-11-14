<script lang="ts">
export interface PdfView {
  page: number;
  pages: number;
  zoom: number;
  fit: boolean;
}
</script>
<script setup lang="ts">
import { ref, shallowRef, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue';
import type { PDFDocumentLoadingTask, PDFDocumentProxy } from 'pdfjs-dist';
import { loadPdfjs } from './lib/pdf-render';
import { PX_PER_PT, fitScale, keepCentre, nextZoom } from './lib/pdf-zoom';
import PdfSheet from './PdfSheet.vue';
const intersectionSupported = typeof IntersectionObserver !== 'undefined';
const props = defineProps<{ src: string; path: string; maxPages?: number }>();
const emit = defineEmits<{ view: [view: PdfView] }>();
const stage = ref<HTMLDivElement>(),
  doc = shallowRef<PDFDocumentProxy>(),
  failure = ref(''),
  page = ref(1),
  pages = ref(0),
  sizes = ref<{ width: number; height: number }[]>([]),
  zoom = ref<number | 'fit'>('fit'),
  room = ref({ width: 0, height: 0 }),
  near = ref(new Set([1])),
  drawn = ref(new Map<number, number>()),
  target = ref<{ page: number; y: number }>(),
  marker = ref<HTMLElement[]>();
let resize: ResizeObserver | undefined,
  observer: IntersectionObserver | undefined,
  flash: ReturnType<typeof setTimeout> | undefined;
const shownZoom = computed(() =>
  zoom.value === 'fit'
    ? sizes.value[0]
      ? fitScale(room.value, sizes.value[0], 'page', 16)
      : 1
    : zoom.value,
);
const widthOf = (n: number) =>
  Math.round((sizes.value[n - 1]?.width ?? 0) * PX_PER_PT * shownZoom.value);
const sheets = () => Array.from(stage.value?.querySelectorAll<HTMLElement>('.pdf-page') ?? []);
function middle() {
  const box = stage.value?.getBoundingClientRect();
  if (!box) return;
  const y = box.top + box.height / 2;
  let best: { page: number; fraction: number; distance: number } | undefined;
  for (const sheet of sheets()) {
    const at = sheet.getBoundingClientRect();
    if (!at.height) continue;
    const distance = y < at.top ? at.top - y : y > at.bottom ? y - at.bottom : 0;
    if (!best || distance < best.distance)
      best = {
        page: Number(sheet.dataset.page),
        fraction: Math.min(1, Math.max(0, (y - at.top) / at.height)),
        distance,
      };
  }
  return best;
}
function goToPage(n: number) {
  const to = Math.max(1, Math.min(pages.value || 1, n));
  page.value = to;
  const sheet = sheets().find((s) => Number(s.dataset.page) === to),
    box = stage.value;
  if (sheet && box)
    box.scrollTop += sheet.getBoundingClientRect().top - box.getBoundingClientRect().top - 16;
}
let centre = { x: 0.5, y: 0 };
function scrolled() {
  const box = stage.value;
  if (!box?.scrollWidth || !box.scrollHeight) return;
  centre = {
    x: (box.scrollLeft + box.clientWidth / 2) / box.scrollWidth,
    y: (box.scrollTop + box.clientHeight / 2) / box.scrollHeight,
  };
  const at = middle();
  if (at) page.value = at.page;
}
watch(shownZoom, async () => {
  await nextTick();
  const box = stage.value;
  if (!box) return;
  const to = keepCentre(
    centre,
    { width: box.scrollWidth, height: box.scrollHeight },
    { width: box.clientWidth, height: box.clientHeight },
  );
  box.scrollLeft = to.left;
  box.scrollTop = to.top;
});
watch(
  [page, pages, shownZoom, zoom],
  () =>
    emit('view', {
      page: page.value,
      pages: pages.value,
      zoom: shownZoom.value,
      fit: zoom.value === 'fit',
    }),
  { immediate: true },
);
watch(
  () => props.src,
  (src, _, onCleanup) => {
    let cancelled = false,
      disposed = false,
      task: PDFDocumentLoadingTask | undefined;
    const dispose = () => {
      if (disposed) return;
      disposed = true;
      void task?.destroy();
    };
    onCleanup(() => {
      cancelled = true;
      dispose();
      observer?.disconnect();
    });
    doc.value = undefined;
    failure.value = '';
    page.value = 1;
    pages.value = 0;
    sizes.value = [];
    zoom.value = 'fit';
    drawn.value = new Map();
    target.value = undefined;
    near.value = new Set([1]);
    centre = { x: 0.5, y: 0 };
    void (async () => {
      try {
        const pdfjs = await loadPdfjs();
        if (cancelled) return;
        task = pdfjs.getDocument({ url: src, enableXfa: false });
        const loaded = await task.promise;
        if (cancelled) return dispose();
        if (props.maxPages && loaded.numPages > props.maxPages) {
          failure.value = `This PDF has more than ${props.maxPages} pages and cannot be previewed here.`;
          dispose();
          return;
        }
        const first = (await loaded.getPage(1)).getViewport({ scale: 1 });
        if (cancelled) return dispose();
        sizes.value = Array.from({ length: loaded.numPages }, () => ({
          width: first.width,
          height: first.height,
        }));
        pages.value = loaded.numPages;
        doc.value = loaded;
        await nextTick();
        if (cancelled) return;
        if (typeof IntersectionObserver !== 'undefined' && stage.value) {
          observer = new IntersectionObserver(
            (entries) => {
              const held = new Set(near.value);
              for (const e of entries) {
                const n = Number((e.target as HTMLElement).dataset.page);
                if (e.isIntersecting) held.add(n);
                else held.delete(n);
              }
              near.value = held;
            },
            { root: stage.value, rootMargin: '100% 0px' },
          );
          sheets().forEach((s) => observer!.observe(s));
        }
        for (let n = 2; n <= loaded.numPages; n++) {
          const v = (await loaded.getPage(n)).getViewport({ scale: 1 });
          if (cancelled) return;
          sizes.value[n - 1] = { width: v.width, height: v.height };
        }
      } catch (e) {
        dispose();
        if (!cancelled) failed(e);
      }
    })();
  },
  { immediate: true },
);
function failed(e: unknown) {
  const name = e instanceof Error ? e.name : '';
  failure.value =
    name === 'PasswordException'
      ? 'This PDF is password-protected.'
      : name === 'InvalidPDFException'
        ? 'This file is not a readable PDF.'
        : 'This PDF could not be displayed.';
}
watch(
  () => [target.value, target.value && drawn.value.has(target.value.page)],
  async () => {
    clearTimeout(flash);
    await nextTick();
    const el = marker.value?.[0],
      box = stage.value;
    if (!el || !box) return;
    box.scrollTop +=
      el.getBoundingClientRect().top - box.getBoundingClientRect().top - box.clientHeight / 2;
    flash = setTimeout(() => (target.value = undefined), 1200);
  },
);
onMounted(() => {
  const measure = () => {
    if (stage.value)
      room.value = { width: stage.value.clientWidth, height: stage.value.clientHeight };
  };
  measure();
  if (typeof ResizeObserver !== 'undefined' && stage.value) {
    resize = new ResizeObserver(measure);
    resize.observe(stage.value);
  }
});
onBeforeUnmount(() => {
  resize?.disconnect();
  observer?.disconnect();
  clearTimeout(flash);
});
defineExpose({
  goToPage,
  setZoom: (z: number | 'fit') => (zoom.value = z === 'fit' ? z : Math.max(0.5, Math.min(4, z))),
  zoomBy: (steps: -1 | 1) => (zoom.value = nextZoom(shownZoom.value, steps)),
  scrollTo: (at: { page: number; y: number }) => {
    goToPage(at.page);
    target.value = { page: page.value, y: at.y };
  },
  visiblePoint: () => {
    const at = middle(),
      size = at && sizes.value[at.page - 1];
    return at && size ? { page: at.page, y: at.fraction * size.height } : undefined;
  },
});
</script>
<template>
  <figure class="viz-figure artifact-pdf">
    <div ref="stage" class="pdf-stage pdf-stage--page" @scroll="scrolled">
      <p v-if="failure" class="viewer-error">{{ failure }}</p>
      <template v-else-if="doc"
        ><PdfSheet
          v-for="(size, i) in sizes"
          :key="i"
          :n="i + 1"
          :path="path"
          :doc="doc"
          :size="size"
          :width-px="widthOf(i + 1)"
          :near="!intersectionSupported || near.has(i + 1)"
          @drawn="(n, height) => drawn.set(n, height)"
          @failure="failed"
          ><div
            v-if="target?.page === i + 1 && drawn.has(i + 1)"
            ref="marker"
            class="pdf-marker"
            aria-hidden="true"
            :style="{ top: `${(target.y / drawn.get(i + 1)!) * 100}%` }" /></PdfSheet
      ></template>
      <div v-else class="pdf-page">
        <canvas class="pdf-canvas" />
        <div class="pdf-text-layer" />
      </div>
    </div>
    <div v-if="!failure" class="pdf-pager" role="group" aria-label="The page"><slot /></div>
  </figure>
</template>
