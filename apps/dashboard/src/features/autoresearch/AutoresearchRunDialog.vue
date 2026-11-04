<script setup lang="ts">
import { defineAsyncComponent, defineComponent, h, computed, ref } from 'vue';
import { useAutoresearchRun } from '../../api/queries';
import Modal from '../../ui/Modal.vue';
import Icon from '../../ui/Icon.vue';
const formattingFailed = ref(false);
const PlainRecordedText = defineComponent({
  props: { content: { type: String, default: '' } },
  setup: (text) => () => h('div', { class: 'recorded-text-fallback' }, text.content),
});
const RecordedMarkdown = defineAsyncComponent({
  loader: () => import('../../ui/RecordedMarkdown.vue'),
  loadingComponent: PlainRecordedText,
  errorComponent: PlainRecordedText,
  delay: 0,
  onError: (_error, _retry, fail) => {
    formattingFailed.value = true;
    fail();
  },
});

const props = defineProps<{ runId: string }>();
const emit = defineEmits<{ close: [] }>();
const detail = useAutoresearchRun(() => props.runId);
const run = computed(() => detail.data.value?.run);
const label = (value: string) => value.charAt(0).toUpperCase() + value.slice(1);
const waiting = computed(() => ['queued', 'running', 'blocked'].includes(run.value?.status ?? ''));
</script>
<template>
  <Modal title="Autoresearch run" class="autoresearch-run-modal" @close="emit('close')">
    <section class="run-detail">
      <header class="run-head">
        <div class="run-title">
          <Icon name="zap" :size="20" />
          <h2>{{ detail.data.value?.autoresearch_title ?? 'Autoresearch run' }}</h2>
          <button class="icon-button" aria-label="Close run" @click="emit('close')">
            <Icon name="close" :size="18" />
          </button>
        </div>
        <template v-if="run">
          <div class="run-meta">
            <span class="run-badge" :class="{ failed: run.status === 'failed' }">{{
              label(run.status)
            }}</span>
            <span>{{
              detail.data.value?.execution_mode === 'run_only' ? 'Run only' : 'Create a study'
            }}</span>
            <span>·</span><span>{{ label(run.source) }}</span>
          </div>
          <p class="run-planned">Planned · {{ new Date(run.planned_at).toLocaleString() }}</p>
        </template>
      </header>
      <div class="run-body">
        <p v-if="detail.error.value" class="inline-error" role="alert">
          {{ detail.error.value.message }}
          <button class="text-button" :disabled="detail.isFetching.value" @click="detail.refetch()">
            Retry
          </button>
        </p>
        <p v-if="detail.isPending.value && !detail.error.value" role="status">
          Loading Autoresearch run…
        </p>
        <template v-if="run">
          <p v-if="run.reason" class="run-reason">{{ run.reason }}</p>
          <template v-if="run.report">
            <div id="run-report">
              <p v-if="formattingFailed" class="formatting-notice" role="status">
                Markdown formatting is unavailable; showing recorded text.
              </p>
              <div v-if="formattingFailed" class="recorded-text-fallback">{{ run.report }}</div>
              <RecordedMarkdown v-else :content="run.report" />
            </div>
          </template>
          <div v-else class="run-empty">
            <Icon name="file" :size="28" />
            <b>{{ waiting ? 'Waiting for a report' : 'No report was submitted' }}</b>
            <p>
              {{
                waiting
                  ? 'A submitted report will appear here. Execution status is shown above.'
                  : 'This run has no submitted report. Execution status is shown above.'
              }}
            </p>
          </div>
        </template>
      </div>
      <footer class="run-foot">
        <span>{{
          run?.report
            ? run.status === 'running'
              ? 'Report available · Run still in progress'
              : 'Full report · As submitted by the agent'
            : 'Run status and report availability are separate'
        }}</span>
        <button class="button" @click="emit('close')">Close</button>
      </footer>
    </section>
  </Modal>
</template>
<style scoped>
.recorded-text-fallback {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.formatting-notice {
  color: var(--muted);
  font-size: 12px;
}
.autoresearch-run-modal {
  width: 760px;
}
.run-detail {
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
}
.run-head {
  padding: 22px 24px 18px;
}
.run-title {
  display: flex;
  align-items: center;
  gap: 14px;
}
.run-title > .icon {
  color: var(--muted);
  flex-shrink: 0;
}
.run-title h2 {
  flex: 1;
  font-size: 18px;
  font-weight: 600;
  margin: 0;
  overflow-wrap: anywhere;
}
.run-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
  font-size: 12px;
  color: var(--muted);
}
.run-badge {
  background: var(--fill-chip);
  border-radius: 5px;
  padding: 4px 7px;
  font-size: 11px;
}
.run-badge.failed {
  background: var(--error-bg);
  color: var(--error-text);
}
.run-planned {
  margin: 10px 0 0;
  color: var(--muted);
  font-size: 12px;
}
.run-body {
  padding: 24px;
  min-height: 300px;
  min-width: 0;
  overflow: auto;
  flex: 1;
  font-size: 13px;
}
.run-body :deep(.markdown-body h1) {
  font-size: 20px;
}
.run-reason {
  border: 1px solid var(--error-border);
  border-radius: 7px;
  background: var(--error-bg);
  color: var(--error-text);
  padding: 10px 12px;
  margin: 0 0 18px;
  font-size: 12px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.run-empty {
  min-height: 245px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  color: var(--muted);
  gap: 12px;
}
.run-empty b {
  font-size: 14px;
  font-weight: 500;
}
.run-empty p {
  font-size: 12px;
  line-height: 1.6;
  max-width: 380px;
  margin: 0;
}
.run-foot {
  padding: 14px 24px;
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}
.run-foot span {
  font-size: 11px;
  color: var(--muted);
}
</style>
