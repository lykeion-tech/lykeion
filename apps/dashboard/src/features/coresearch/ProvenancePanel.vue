<script setup lang="ts">
import { computed } from 'vue';
import type { FileVersion, ProjectRow } from './types';
import {
  useCoresearchRuns,
  useCoresearchRun,
  useCoresearchFiles,
  useCoresearchContent,
  useProducingTurn,
} from './api';
import { langForPath } from './tool-detail';
import CodeBlock from './CodeBlock.vue';
import TranscriptTurn from './TranscriptTurn.vue';
import { ref, watch } from 'vue';
const props = defineProps<{ project: ProjectRow; version: FileVersion; tab: string }>();
const runs = useCoresearchRuns(() => props.project.id),
  files = useCoresearchFiles(() => props.project.id);
const producing = computed(() => runs.data.value?.find((r) => r.id === props.version.run_id));
const run = useCoresearchRun(
  () => props.project.id,
  () => producing.value?.key ?? '',
);
const script = useCoresearchContent(
  () => props.project.id,
  () => (props.tab === 'code' ? (run.data.value?.script_file_id ?? '') : ''),
  () => run.data.value?.script_n ?? 0,
);
const scriptText = ref('');
watch(
  () => script.data.value,
  async (blob, _, cleanup) => {
    let live = true;
    cleanup(() => (live = false));
    scriptText.value = '';
    if (blob) {
      const text = await blob.text();
      if (live) scriptText.value = text;
    }
  },
  { immediate: true },
);
const path = computed(
  () =>
    files.data.value?.find((f) => f.id === run.data.value?.script_file_id)?.path ??
    'Script unavailable',
);
const turn = useProducingTurn(
  () => props.project.id,
  () => (props.tab === 'transcript' ? (props.version.agent_run_id ?? '') : ''),
);
</script>
<template>
  <section class="prov-content" data-testid="provenance-panel">
    <template v-if="tab === 'transcript'"
      ><p v-if="!version.agent_run_id" class="prov-empty">Transcript unavailable for this save.</p>
      <p v-else-if="turn.isPending.value" class="nbp-empty">Loading the record…</p>
      <div v-else-if="turn.data.value" class="prov-transcript">
        <TranscriptTurn
          :project-id="project.id"
          :chat-id="turn.data.value.turn.chat_id"
          :turn="turn.data.value.turn"
          :replies="turn.data.value.replies"
        />
      </div>
      <p v-else class="prov-empty">The producing turn is unavailable.</p></template
    >
    <template v-else-if="!version.run_id"
      ><p class="prov-empty">
        {{ version.restored_from ? `Restored from v${version.restored_from}. ` : ''
        }}{{ version.author_user_id ? 'Saved by a member. ' : '' }}No recorded execution provenance
        for this save.
      </p></template
    >
    <p v-else-if="runs.isPending.value || (!!producing && run.isPending.value)" class="nbp-empty">
      Loading the record…
    </p>
    <p v-else-if="!run.data.value" class="prov-empty">Recorded execution unavailable.</p>
    <template v-else-if="tab === 'code'"
      ><div class="prov-script">
        <p class="viz-meta">{{ path }} v{{ run.data.value.script_n }}</p>
        <p v-if="script.error.value" class="prov-empty">Script unavailable.</p>
        <p v-else-if="script.isPending.value" class="nbp-empty">Reading…</p>
        <CodeBlock v-else :code="scriptText" :lang="langForPath(path)" line-numbers /></div
    ></template>
    <template v-else-if="tab === 'log'"
      ><div class="prov-log">
        <p class="viz-meta">stdout</p>
        <pre class="code-text raw-text">{{
          run.data.value.facts?.stdout_tail || 'No stdout recorded.'
        }}</pre>
        <p class="viz-meta">stderr</p>
        <pre class="code-text raw-text">{{
          run.data.value.facts?.stderr_tail || 'No stderr recorded.'
        }}</pre>
      </div></template
    >
  </section>
</template>
