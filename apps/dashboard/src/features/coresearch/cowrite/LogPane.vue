<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { Build } from './types';
import { parseTexLog } from './lib/manuscript';
import PaneIcon from './PaneIcon.vue';
import PaneViewPicker from './PaneViewPicker.vue';
const props = defineProps<{ build?: Build; log?: string; failed?: boolean }>();
const emit = defineEmits<{ jump: [path: string, line: number] }>();
const parsed = computed(() => parseTexLog(props.log ?? ''));
const view = ref('raw');
watch(
  () => [props.build?.id, props.log],
  () => (view.value = parsed.value.errors.length ? 'errors' : 'raw'),
  { immediate: true },
);
const views = [
  { id: 'raw', label: 'Raw logs' },
  { id: 'errors', label: 'Errors and warnings' },
];
</script>
<template>
  <div class="ms-pane" data-testid="log-pane">
    <header class="art-header">
      <span class="art-title"><PaneIcon name="Terminal" />Logs</span
      ><PaneViewPicker
        v-if="build"
        label="Log view"
        :views="views"
        :value="view"
        @change="view = $event"
      />
    </header>
    <p v-if="!build" class="ms-pane-empty">Nothing built yet — compile the paper to see its log.</p>
    <div v-else class="ms-log-body">
      <template v-if="view === 'raw'"
        ><p v-if="failed" class="ms-pane-empty">The log could not be read.</p>
        <p v-else-if="log === undefined" class="ms-pane-empty">Reading the log…</p>
        <pre v-else class="ms-log-text">{{ log }}</pre>
      </template>
      <p v-else-if="log === undefined" class="ms-pane-empty">
        {{ failed ? 'The log could not be read.' : 'Reading the log…' }}
      </p>
      <p v-else-if="!parsed.errors.length && !parsed.warnings" class="ms-pane-empty">
        No errors or warnings
      </p>
      <template v-else
        ><div v-for="(error, i) in parsed.errors" :key="i" class="ms-log-error">
          <button
            v-if="error.file !== undefined && error.line !== undefined"
            type="button"
            class="ms-log-where"
            @click="emit('jump', error.file, error.line)"
          >
            line {{ error.line }} · {{ error.file }}</button
          ><span v-else class="ms-log-where ms-log-where--nowhere">elsewhere in the run</span>
          <p class="ms-log-message">{{ error.message }}</p>
        </div>
        <p v-if="parsed.warnings" class="ms-log-warnings">
          {{ parsed.warnings }} warning{{ parsed.warnings === 1 ? '' : 's' }}
        </p></template
      >
    </div>
  </div>
</template>
