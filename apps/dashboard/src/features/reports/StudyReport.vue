<script setup lang="ts">
import { computed, onUnmounted, ref, toRef, watch } from 'vue';
import { ApiError } from '../../api/client';
import { useStudyReport } from '../../api/queries';
import { columnName, studyDisplayTitle } from '../../lib/studies';
import Icon from '../../ui/Icon.vue';
import SurfacePane from '../../ui/SurfacePane.vue';
import { lazyRecordedMarkdown } from '../../ui/lazy-recorded-markdown';
import ReportGraph from './ReportGraph.vue';
import ReportPane from './ReportPane.vue';
import { shownReport } from './shown-report';

const RecordedMarkdown = lazyRecordedMarkdown();
const props = defineProps<{ cardId: string }>();
const query = useStudyReport(toRef(props, 'cardId'));
const report = computed(() => query.data.value);
const noReport = computed(
  () => query.error.value instanceof ApiError && query.error.value.status === 409,
);

const selected = ref<string | null>(null);
const expanded = ref(false);
watch(
  () => props.cardId,
  () => {
    selected.value = null;
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

const finished = computed(() => {
  const d = new Date(report.value?.finished_at ?? '');
  return d.getFullYear() > 1 ? d.toLocaleDateString(undefined, { dateStyle: 'medium' }) : '';
});
const stages = computed(() => (report.value?.stages ?? []).map(columnName).join(' → '));
const handInBy = computed(() => {
  const h = report.value?.hand_in;
  return h ? [h.agent_name, new Date(h.at).toLocaleString()].filter(Boolean).join(' · ') : '';
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

const paneOpen = ref(typeof window === 'undefined' || window.innerWidth > 980);
const paneActive = ref<string | null>('report');
const paneFocused = ref(false);
const surfaces = [{ id: 'report', label: 'Report', icon: 'network' }];
</script>

<template>
  <section class="study-report" aria-label="Study report">
    <div class="report-main" :class="{ 'beside-pane': paneOpen && !paneFocused }">
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
            <h1>#{{ report.number }} {{ studyDisplayTitle(report.title) }}</h1>
            <p class="type-caption">
              <span v-if="finished">Finished {{ finished }}</span>
              <span v-if="stages">{{ stages }}</span>
            </p>
          </div>
          <RouterLink class="report-study-link" :to="'/studies/' + cardId"
            >Open the study</RouterLink
          >
          <button
            class="icon-button"
            :aria-pressed="paneOpen"
            aria-label="Toggle report pane"
            @click="paneOpen = !paneOpen"
          >
            <Icon name="panel" />
          </button>
        </header>
        <section class="hand-in" aria-label="Hand-in report">
          <h2>Hand-in report</h2>
          <template v-if="report.hand_in">
            <p class="type-caption hand-in-by">{{ handInBy }}</p>
            <div class="hand-in-text" :class="{ folded: overflows && !expanded }">
              <div ref="handInBody"><RecordedMarkdown :content="report.hand_in.text" /></div>
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
        <div class="report-graph-frame">
          <ReportGraph :report="report" :selected="selected" @select="selected = $event" />
        </div>
      </template>
    </div>
    <SurfacePane
      v-if="report"
      v-model:open="paneOpen"
      v-model:active="paneActive"
      v-model:focused="paneFocused"
      floating
      hide-title
      :surfaces="surfaces"
      width-key="lykeion.report-pane-width"
    >
      <ReportPane
        class="report-pane-body"
        :report="report"
        :selected="selected"
        @select="selected = $event"
      />
    </SurfacePane>
  </section>
</template>

<style scoped>
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
@media (min-width: 981px) {
  .report-main.beside-pane {
    padding-right: 396px;
  }
}
.report-header {
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
.report-study-link {
  flex: none;
  font-size: 13px;
  color: var(--primary-text);
  line-height: 28px;
}
.report-study-link:hover {
  text-decoration: underline;
}
.hand-in {
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  padding: 16px 20px;
}
.hand-in h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.hand-in-by,
.hand-in-none {
  margin: 4px 0 10px;
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
.report-graph-frame {
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
