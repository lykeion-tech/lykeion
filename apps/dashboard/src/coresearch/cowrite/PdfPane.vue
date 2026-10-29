<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { projectPath } from '../api';
import type { Build } from './types';
import {
  parseSynctex,
  forwardSearch,
  inverseSearch,
  pdfPointsToSynctex,
  synctexToPdfPoints,
} from './lib/manuscript';
import { PDF_ZOOM_PRESETS } from './lib/pdf-zoom';
import PdfViewer, { type PdfView } from './PdfViewer.vue';
import PaneIcon from './PaneIcon.vue';
const props = defineProps<{
  projectId: string;
  title: string;
  builds: Build[];
  noMachine?: boolean;
  errors?: number;
  disabled?: boolean;
  cursor?: { path: string; line: number };
}>();
const emit = defineEmits<{
  compile: [];
  undo: [];
  redo: [];
  tools: [];
  goToSource: [path: string, line: number];
}>();
const viewer = ref<InstanceType<typeof PdfViewer>>(),
  view = ref<PdfView>({ page: 1, pages: 0, zoom: 1, fit: true }),
  url = ref(''),
  failure = ref(''),
  missed = ref(''),
  draft = ref<string>(),
  zoomOpen = ref(false),
  zoomMenu = ref<HTMLElement>(),
  zoomRoot = ref<HTMLElement>(),
  zoomStyle = ref({ top: '0px', left: '0px' });
const ordered = computed(() => [...props.builds].sort((a, b) => b.seq - a.seq)),
  shown = computed(() => ordered.value.find((b) => b.status === 'ok' && b.has_pdf)),
  going = computed(() => ordered.value.find((b) => ['queued', 'running'].includes(b.status)));
const fileTitle = computed(
  () =>
    props.title
      // eslint-disable-next-line no-control-regex -- control characters are not allowed in file names
      .replace(/[\u0000-\u001f<>:"/\\|?*]/g, '_')
      .trim()
      .replace(/[. ]+$/, '') || 'paper',
);
const head = computed(() => {
  const latest = ordered.value[0],
    showing = shown.value ? ` · showing #${shown.value.seq}` : '';
  return going.value
    ? ''
    : props.noMachine
      ? `no machine online${showing}`
      : !latest
        ? 'Not built yet'
        : latest.status === 'ok'
          ? ''
          : latest.status === 'failed'
            ? `Failed #${latest.seq}${props.errors === undefined ? '' : ` · ${props.errors} error${props.errors === 1 ? '' : 's'}`}${showing}`
            : `Aborted #${latest.seq}${showing}`;
});
let asked = 0;
watch(
  () => [props.projectId, workspaceId.value, shown.value?.id],
  async (_, __, cleanup) => {
    const controller = new AbortController();
    let ownURL = '',
      cancelled = false;
    const build = shown.value,
      workspace = workspaceId.value,
      project = props.projectId;
    cleanup(() => {
      cancelled = true;
      controller.abort();
      void nextTick(() => {
        if (ownURL) URL.revokeObjectURL(ownURL);
      });
    });
    url.value = '';
    failure.value = '';
    view.value = { page: 1, pages: 0, zoom: 1, fit: true };
    asked++;
    if (!build) return;
    try {
      const blob = await api<Blob>(
        'GET',
        `${projectPath(project)}/builds/${build.seq}.pdf`,
        undefined,
        undefined,
        { workspace, signal: controller.signal, response: 'blob' },
      );
      if (cancelled) return;
      ownURL = URL.createObjectURL(blob);
      url.value = ownURL;
    } catch (e) {
      if (!cancelled) failure.value = e instanceof Error ? e.message : String(e);
    }
  },
  { immediate: true },
);
watch(
  () => [props.cursor?.path, props.cursor?.line, shown.value?.id],
  () => {
    missed.value = '';
    asked++;
  },
);
async function go(source: boolean) {
  const build = shown.value,
    cursor = props.cursor,
    at = viewer.value?.visiblePoint();
  if (!build || !build.has_synctex || (source ? !at : !cursor)) return;
  const mine = ++asked,
    project = props.projectId,
    workspace = workspaceId.value;
  missed.value = '';
  try {
    const blob = await api<Blob>(
      'GET',
      `${projectPath(project)}/builds/${build.seq}.synctex.gz`,
      undefined,
      undefined,
      { workspace, response: 'blob' },
    );
    const text = await new Response(
      blob.stream().pipeThrough(new DecompressionStream('gzip')),
    ).text();
    if (mine !== asked) return;
    const records = parseSynctex(text);
    if (source) {
      const found = inverseSearch(records, at!.page, pdfPointsToSynctex(at!.y));
      if (found) emit('goToSource', found.file, found.line);
      else
        missed.value = `nothing on page ${at!.page} of build #${build.seq} names a line of the source`;
    } else {
      const found = forwardSearch(records, cursor!.path, cursor!.line);
      if (!found || found.page < 1 || found.page > (view.value.pages || build.pages || Infinity))
        missed.value = `line ${cursor!.line} of ${cursor!.path} is not in build #${build.seq}`;
      else viewer.value?.scrollTo({ page: found.page, y: synctexToPdfPoints(found.y) });
    }
  } catch {
    if (mine === asked) missed.value = `build #${build.seq}'s SyncTeX could not be read`;
  }
}
async function toggleZoom() {
  zoomOpen.value = !zoomOpen.value;
  await nextTick();
  if (!zoomOpen.value) return;
  zoomMenu.value?.showPopover?.();
  const a = zoomRoot.value?.getBoundingClientRect(),
    b = zoomMenu.value?.getBoundingClientRect();
  if (a && b)
    zoomStyle.value = {
      top: `${Math.max(8, Math.min(a.bottom + 6, window.innerHeight - b.height - 8))}px`,
      left: `${Math.max(8, a.right - b.width)}px`,
    };
}
function setZoom(z: number | 'fit') {
  viewer.value?.setZoom(z);
  zoomOpen.value = false;
}
function pageGo() {
  const n = parseInt(draft.value ?? '');
  if (Number.isFinite(n)) viewer.value?.goToPage(n);
  draft.value = undefined;
}
function keys(e: KeyboardEvent) {
  if (!(e.metaKey || e.ctrlKey) || !view.value.pages) return;
  if (e.key === '0') setZoom('fit');
  else if (e.key === '=' || e.key === '+') viewer.value?.zoomBy(1);
  else if (e.key === '-') viewer.value?.zoomBy(-1);
  else return;
  e.preventDefault();
}
</script>
<template>
  <div class="ms-pane" data-testid="pdf-pane" @keydown="keys">
    <div class="ms-pane-head">
      <div class="ms-compile">
        <button
          type="button"
          class="ms-compile-run"
          :disabled="!!going || disabled"
          @click="emit('compile')"
        >
          <span v-if="going" class="ms-compile-spin" aria-hidden="true" /><PaneIcon
            v-else
            name="Refresh"
            width="15"
            height="15"
          />{{ going ? 'Building…' : 'Compile' }}
        </button>
      </div>
      <span class="ms-pane-state" role="status" data-testid="pdf-head">{{ head }}</span
      ><button
        v-if="!shown || !view.pages"
        type="button"
        class="ms-zoom-head ms-tools"
        @click="emit('tools')"
      >
        <PaneIcon name="Tools" width="14" height="14" />Tools
      </button>
      <div v-else class="ms-view" role="group" aria-label="Page and zoom">
        <span class="ms-page"
          ><input
            class="ms-page-field"
            inputmode="numeric"
            :aria-label="`Page, of ${view.pages}`"
            :value="draft ?? String(view.page).padStart(2, '0')"
            @focus="($event.target as HTMLInputElement).select()"
            @input="draft = ($event.target as HTMLInputElement).value.replace(/\D/g, '')"
            @blur="pageGo"
            @keydown.enter="($event.target as HTMLInputElement).blur()"
            @keydown.escape="
              draft = undefined;
              ($event.target as HTMLInputElement).blur();
            "
          /><span class="ms-page-of">of {{ String(view.pages).padStart(2, '0') }}</span></span
        ><button type="button" class="ms-zoom-head ms-tools" @click="emit('tools')">
          <PaneIcon name="Tools" width="14" height="14" />Tools
        </button>
        <div ref="zoomRoot" class="ms-zoom">
          <button
            type="button"
            class="ms-zoom-head"
            aria-haspopup="menu"
            :aria-expanded="zoomOpen"
            aria-label="Zoom"
            @click="toggleZoom"
          >
            <PaneIcon name="Expand" width="14" height="14" /><span>{{
              view.fit ? 'Zoom to fit' : `${Math.round(view.zoom * 100)}%`
            }}</span
            ><PaneIcon
              name="ChevronDown"
              width="14"
              height="14"
              class="ms-zoom-chev"
              :class="{ 'is-open': zoomOpen }"
            />
          </button>
          <div
            v-if="zoomOpen"
            ref="zoomMenu"
            popover="auto"
            :style="zoomStyle"
            class="ms-zoom-menu"
            role="menu"
            aria-label="Zoom"
            @toggle="($event as ToggleEvent).newState === 'closed' && (zoomOpen = false)"
          >
            <button type="button" role="menuitem" class="ms-zoom-item" @click="setZoom('fit')">
              Zoom to fit<kbd>⌘0</kbd></button
            ><button
              type="button"
              role="menuitem"
              class="ms-zoom-item"
              :disabled="view.zoom >= 4"
              @click="
                viewer?.zoomBy(1);
                zoomOpen = false;
              "
            >
              Zoom in<kbd>⌘+</kbd></button
            ><button
              type="button"
              role="menuitem"
              class="ms-zoom-item"
              :disabled="view.zoom <= 0.5"
              @click="
                viewer?.zoomBy(-1);
                zoomOpen = false;
              "
            >
              Zoom out<kbd>⌘−</kbd>
            </button>
            <div class="ms-zoom-sep" role="separator" />
            <button
              v-for="z in PDF_ZOOM_PRESETS"
              :key="z"
              type="button"
              role="menuitemradio"
              :aria-checked="!view.fit && Math.abs(view.zoom - z) < 1e-6"
              class="ms-zoom-item"
              @click="setZoom(z)"
            >
              {{ z * 100 }}%
            </button>
          </div>
        </div>
        <a
          class="ms-view-step ms-view-icon"
          :href="url"
          :download="`${fileTitle}.pdf`"
          aria-label="Download the PDF"
          title="Download the PDF"
          ><PaneIcon name="Download" width="17" height="17"
        /></a>
      </div>
    </div>
    <p class="ms-pane-note" :class="{ 'is-empty': !missed }" role="status">{{ missed }}</p>
    <div class="ms-pdf-body">
      <PdfViewer
        v-if="url"
        :key="url"
        ref="viewer"
        :src="url"
        :path="`${fileTitle}.pdf`"
        @view="view = $event"
        ><button type="button" class="pdf-pager-step" aria-label="Undo" @click="emit('undo')">
          ↺</button
        ><button type="button" class="pdf-pager-step" aria-label="Redo" @click="emit('redo')">
          ↻</button
        ><button
          v-if="shown?.has_synctex"
          type="button"
          class="pdf-pager-step"
          aria-label="Go to the source"
          title="Go to the source of what is on screen"
          @click="go(true)"
        >
          <PaneIcon name="ChevronLeft" width="14" height="14" /></button
        ><template v-if="cursor && shown?.has_synctex"
          ><button type="button" class="pdf-pager-go" @click="go(false)">Go to PDF</button
          ><button
            type="button"
            class="pdf-pager-step"
            aria-label="Go to the cursor's place in the PDF"
            title="Go to the cursor's place in the PDF"
            @click="go(false)"
          >
            <PaneIcon name="ChevronRight" width="14" height="14" /></button></template
      ></PdfViewer>
      <p v-else-if="failure" class="ms-pane-empty" role="alert">{{ failure }}</p>
      <p v-else-if="!shown" class="ms-pane-empty">Nothing to show yet — compile the paper.</p>
    </div>
  </div>
</template>
