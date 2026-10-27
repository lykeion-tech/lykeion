<script setup lang="ts">
import { computed, ref } from 'vue';
import type { ActivityStep } from '../lib/run-transcript';
import { recordedContent } from '../lib/run-transcript';
import { langForPath, stripReadLineNumbers, parseWebResults } from './tool-detail';
import { boundedUnifiedDiff, DIFF_MAX_BYTES } from './diff';
import CodeBlock from './CodeBlock.vue';
const props = defineProps<{ step: ActivityStep; active: boolean }>();
const outputOpen = ref(false);
const input = computed(() => {
  const c = props.step.messages[0]?.content as Record<string, unknown> | undefined;
  if (!c || typeof c !== 'object') return {};
  const item = c.item as Record<string, unknown> | undefined;
  let value = c.input ?? item?.arguments ?? c;
  if (typeof value === 'string') {
    try {
      value = JSON.parse(value);
    } catch {
      return {};
    }
  }
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
});
const str = (key: string) =>
  typeof input.value[key] === 'string' ? (input.value[key] as string) : '';
const path = computed(() => str('file_path') || str('path'));
const language = computed(() => langForPath(path.value));
const diff = computed(() => {
  const old = str('old_string'),
    next = str('new_string');
  if (
    new TextEncoder().encode(old).length > DIFF_MAX_BYTES ||
    new TextEncoder().encode(next).length > DIFF_MAX_BYTES
  )
    return null;
  const result = boundedUnifiedDiff(old, next);
  return result.kind === 'diff' ? result.text : null;
});
const webResults = computed(() =>
  /search|lookup/i.test(props.step.messages[0]?.tool ?? '')
    ? parseWebResults(props.step.output)
    : null,
);
const read = computed(() => /^read(file)?$/i.test(props.step.messages[0]?.tool ?? ''));
const code = computed(() => props.step.notebook?.code || props.step.command || str('content'));
const raw = computed(() => props.step.messages.map((m) => recordedContent(m.content)).join('\n\n'));
</script>
<template>
  <CodeBlock
    v-if="read && step.output"
    :code="stripReadLineNumbers(step.output)"
    :lang="language"
  />
  <template v-else>
    <CodeBlock
      v-if="code"
      :code="code"
      :lang="step.notebook?.language ?? (step.command ? 'bash' : language)"
    />
    <template v-else-if="str('old_string') || str('new_string')"
      ><CodeBlock v-if="diff !== null" :code="diff" lang="diff" />
      <p v-else class="tool-detail-note">
        This edit is too large to compare inline. The full inputs are in Recorded payload.
      </p></template
    >
    <div v-else-if="step.detail" class="tool-detail-field">
      <span class="tool-detail-key">{{ path ? 'path' : 'input' }}</span
      ><span class="tool-detail-value">{{ step.detail }}</span>
    </div>
    <ul v-if="webResults" class="tool-results" data-testid="tool-results">
      <li v-for="(result, i) in webResults" :key="i" class="tool-result">
        <span class="tool-result-title">{{ result.title || result.url }}</span
        ><span class="tool-result-url">{{ result.url }}</span>
      </li>
    </ul>
    <div v-else-if="step.output" class="tool-output">
      <button
        type="button"
        class="tool-output-toggle"
        :aria-expanded="outputOpen"
        @click="outputOpen = !outputOpen"
      >
        <span class="tool-step-chevron" :class="{ 'tool-step-chevron--open': outputOpen }">›</span
        >{{ outputOpen ? 'Hide output' : 'Show output' }}</button
      ><template v-if="outputOpen"
        ><div class="tool-output-label">{{ step.command ? 'STDOUT' : 'Result' }}</div>
        <pre class="tool-output-pre" data-testid="tool-output">{{ step.output }}</pre>
      </template>
    </div>
  </template>
  <div v-if="!step.output" class="tool-detail-note">
    <p class="tool-detail-note-text">
      {{
        step.running
          ? active
            ? 'This step is still running.'
            : 'This step’s output was not captured.'
          : 'This step ran and produced no output.'
      }}
    </p>
  </div>
  <details class="tool-output">
    <summary class="tool-output-toggle">Recorded payload</summary>
    <CodeBlock :code="raw" lang="json" />
  </details>
</template>
