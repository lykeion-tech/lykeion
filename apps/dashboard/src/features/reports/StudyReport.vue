<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, toRef, watch } from 'vue';
import { ApiError } from '../../api/client';
import { useStudyReport } from '../../api/queries';
import { studyDisplayTitle } from '../../lib/studies';
import Icon from '../../ui/Icon.vue';
import SurfacePane from '../../ui/SurfacePane.vue';
import { lazyRecordedMarkdown } from '../../ui/lazy-recorded-markdown';
import ReportGraph from './ReportGraph.vue';
import ReportPane from './ReportPane.vue';
import { handInPhrases, kindName, noticesOf, overviewOf, placeLinks, statusOf } from './report-sections';
import { shownReport } from './shown-report';

const RecordedMarkdown = lazyRecordedMarkdown();
const props = defineProps<{ cardId: string }>();
const query = useStudyReport(toRef(props, 'cardId'));
const report = computed(() => query.data.value);
const noReport = computed(
  () => query.error.value instanceof ApiError && query.error.value.status === 409,
);
const validationSummary = computed(() => {
  if (!report.value) return '';
  const counts = overviewOf(report.value);
  const statuses = (['supported', 'caveated', 'refuted', 'inconclusive', 'invalid'] as const)
    .filter((status) => counts[status] > 0);
  return statuses.map((status) => {
    const label = statusOf(status, 'finding').word;
    return statuses.length === 1 ? label : `${counts[status]} ${label.toLowerCase()}`;
  }).join(' · ');
});

const selected = ref<string | null>(null);
const graphSelection = ref(0);
const hovered = ref<string | null>(null);
const expanded = ref(false);
watch(
  () => props.cardId,
  () => {
    selected.value = null;
    graphSelection.value = 0;
    expanded.value = false;
  },
);

watch(
  () => report.value && { id: report.value.card_id, title: studyDisplayTitle(report.value.title) },
  (now) => {
    if (now?.id !== shownReport.value?.id || now?.title !== shownReport.value?.title)
      shownReport.value = now ?? null;
  },
  { immediate: true },
);
onUnmounted(() => {
  if (shownReport.value?.id === props.cardId) shownReport.value = null;
});

// The agent name reads "member · Team"; only the Team is shown.
const handInBy = computed(() => {
  const h = report.value?.hand_in;
  if (!h) return '';
  return [
    h.agent_name.split(' · ').slice(1).join(' · ') || h.agent_name,
    new Date(h.at).toLocaleString(),
  ]
    .filter(Boolean)
    .join(' · ');
});

// The hand-in folds to 260px; the button shows only when the text is taller.
const handInBody = ref<HTMLElement>();
const overflows = ref(false);
let observer: ResizeObserver | undefined;
watch(handInBody, (el) => {
  observer?.disconnect();
  if (!el) return;
  observer = new ResizeObserver(() => (overflows.value = el.offsetHeight > 260));
  observer.observe(el);
});
onUnmounted(() => observer?.disconnect());

const notices = computed(() => (report.value ? noticesOf(report.value) : []));
// The hand-in reads as Markdown. Each judged link is found again in the
// rendered text by its phrase and occurrence, and wrapped as a button only
// where the phrase lies whole in one text node outside code and links;
// otherwise it stays plain. Wrapping never changes the text.
const phrases = computed(() => {
  const r = report.value;
  return r?.hand_in && r.links.length ? handInPhrases(r.hand_in.text, r.links) : [];
});
const linksKey = computed(() => JSON.stringify(phrases.value));
const nodeOf = (id: string) => report.value?.nodes.find((n) => n.id === id);
function linkHandIn(body: HTMLElement) {
  const md = body.querySelector<HTMLElement>('.markdown-body, .recorded-text-fallback');
  if (!md || md.querySelector('button.ref') || !phrases.value.length) return;
  const walker = document.createTreeWalker(md, NodeFilter.SHOW_TEXT);
  const texts: Text[] = [];
  while (walker.nextNode()) texts.push(walker.currentNode as Text);
  const cuts = placeLinks(
    texts.map((t) => ({
      text: t.data,
      linkable: !t.parentElement?.closest('code, pre, a, button'),
    })),
    phrases.value,
  );
  // Later cuts first, so earlier offsets in the same text node stay valid.
  cuts.sort((a, b) => a.index - b.index || b.start - a.start);
  for (const c of cuts) {
    const n = nodeOf(c.node);
    if (!n) continue;
    const t = texts[c.index]!;
    t.splitText(c.end);
    const phrase = t.splitText(c.start);
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'ref';
    button.dataset.node = c.node;
    button.setAttribute('aria-label', `${phrase.data}, ${kindName(n.kind)}: ${n.label}`);
    button.setAttribute('aria-pressed', String(selected.value === c.node));
    phrase.replaceWith(button);
    button.append(phrase);
  }
}
let linker: MutationObserver | undefined;
watch(handInBody, (el) => {
  linker?.disconnect();
  if (!el) return;
  // The Markdown renders after it loads; link it whenever its text appears.
  linker = new MutationObserver(() => linkHandIn(el));
  linker.observe(el, { childList: true, subtree: true });
  linkHandIn(el);
});
onUnmounted(() => linker?.disconnect());
watch(selected, (id) => {
  for (const b of handInBody.value?.querySelectorAll<HTMLElement>('button.ref') ?? [])
    b.setAttribute('aria-pressed', String(b.dataset.node === id));
});
function clickRef(e: MouseEvent) {
  const b = (e.target as Element).closest<HTMLElement>('button.ref[data-node]');
  if (b?.dataset.node) selectRef(b.dataset.node);
}
function selectRef(id: string) {
  graphSelection.value = 0;
  selected.value = id;
  paneOpen.value = true;
}

function selectGraph(id: string) {
  selected.value = id;
  graphSelection.value++;
  paneOpen.value = true;
}

const paneOpen = ref(false);
const paneFocused = ref(false);
// The pane has a close button of its own once it is maximized or, on a narrow
// screen, an overlay (the same 980px line SurfacePane uses); the page's toggle
// would then sit on top of it, so it steps aside.
const narrow = ref(typeof window !== 'undefined' && window.innerWidth <= 980);
const measure = () => (narrow.value = window.innerWidth <= 980);
onMounted(() => window.addEventListener('resize', measure));
onUnmounted(() => window.removeEventListener('resize', measure));
const showToggle = computed(() => !(paneOpen.value && (paneFocused.value || narrow.value)));
</script>

<template>
  <section class="study-report" aria-label="Study report">
    <div class="report-main">
      <!-- The page has no header of its own until the report loads. -->
      <h1 v-if="!report" class="sr-only">Study report</h1>
      <p v-if="noReport" class="report-empty">This study has no report.</p>
      <p v-else-if="query.isError.value" class="inline-error" role="alert">
        {{ query.error.value?.message }}
        <button class="text-button" :disabled="query.isFetching.value" @click="query.refetch()">
          Retry
        </button>
      </p>
      <p v-else-if="!report" class="report-empty" role="status">Loading report…</p>
      <template v-else>
        <header class="report-header">
          <div class="report-heading">
            <h1>{{ studyDisplayTitle(report.title) }}</h1>
            <p v-if="validationSummary" class="type-caption">Validation · {{ validationSummary }}</p>
            <p v-if="report.hand_in" class="type-caption hand-in-by">{{ handInBy }}</p>
          </div>
        </header>
        <section class="hand-in" aria-label="Hand-in report">
          <template v-if="report.hand_in">
            <h2 class="type-caption">Recorded agent hand-in</h2>
            <div class="hand-in-text" :class="{ folded: overflows && !expanded }">
              <div ref="handInBody" @click="clickRef">
                <RecordedMarkdown :key="linksKey" :content="report.hand_in.text" />
              </div>
            </div>
            <button
              v-if="overflows"
              class="text-button"
              :aria-expanded="expanded"
              @click="expanded = !expanded"
            >
              {{ expanded ? 'Show less' : 'Show the full hand-in' }}
            </button>
          </template>
          <p v-else class="hand-in-none">The agents left no hand-in report.</p>
        </section>
        <p v-for="notice in notices" :key="notice" class="report-notice type-caption" role="status">
          {{ notice }}
        </p>
        <div class="report-graph-frame">
          <ReportGraph
            :report="report"
            :selected="selected"
            :hovered="hovered"
            @select="selectGraph"
          />
        </div>
      </template>
    </div>
    <button
      v-if="report && showToggle"
      class="icon-button report-pane-toggle"
      :aria-pressed="paneOpen"
      aria-label="Toggle report pane"
      @click="paneOpen = !paneOpen"
    >
      <Icon name="panel" />
    </button>
    <SurfacePane
      v-if="report"
      v-model:open="paneOpen"
      v-model:focused="paneFocused"
      floating
      single
      hide-title
      :active="null"
      :surfaces="[]"
      width-key="lykeion.report-pane-width"
    >
      <ReportPane
        class="report-pane-body"
        :report="report"
        :selected="selected"
        :graph-selection="graphSelection"
        @select="selected = $event"
        @hover="hovered = $event"
      />
    </SurfacePane>
  </section>
</template>

<style scoped>
.study-report :deep(.surface-header) {
  border-bottom: 0;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
.study-report {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
  min-width: 0;
}
.report-main {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: 20px 24px 32px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.report-header {
  padding-right: 40px;
  display: flex;
  align-items: flex-start;
  gap: 16px;
}
.report-heading {
  flex: 1;
  min-width: 0;
}
.report-heading h1 {
  margin: 0;
  font-size: 20px;
  line-height: 1.35;
  font-weight: 650;
  overflow-wrap: anywhere;
}
.report-heading p {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 4px 0 0;
  color: var(--muted);
}
.report-pane-toggle {
  position: absolute;
  top: 10px;
  right: 20px;
  z-index: 5;
}
.hand-in {
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  padding: 16px 20px;
}
.hand-in-none {
  margin: 0;
  color: var(--muted);
}
.hand-in-text {
  font-size: 14px;
  line-height: 1.65;
  overflow-wrap: anywhere;
}
.hand-in-text.folded {
  max-height: 260px;
  overflow: hidden;
  -webkit-mask-image: linear-gradient(to bottom, #000 75%, transparent);
  mask-image: linear-gradient(to bottom, #000 75%, transparent);
}
.hand-in-text :deep(.ref) {
  display: inline;
  padding: 0;
  font: inherit;
  color: var(--primary-text);
  text-align: inherit;
  text-decoration: underline;
  text-decoration-color: var(--border);
  text-underline-offset: 3px;
  border-radius: 3px;
}
.hand-in-text :deep(.ref:hover),
.hand-in-text :deep(.ref[aria-pressed='true']) {
  background: var(--hover);
}
.hand-in-text :deep(.ref:focus-visible) {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}
.report-notice {
  margin: 0;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  color: var(--muted);
}
.report-graph-frame {
  position: relative;
  flex: none;
  height: 460px;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
}
.report-pane-body {
  padding: 16px 20px;
}
.report-empty {
  color: var(--muted);
  padding: 40px 0;
  text-align: center;
}
@media (max-width: 700px) {
  .report-main {
    padding: 16px 12px 40px;
  }
  .report-graph-frame {
    height: 360px;
  }
}
</style>
