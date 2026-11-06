<script setup lang="ts">
import { ref } from 'vue';
import AssistantMessage from '../AssistantMessage.vue';
import CodeBlock from '../CodeBlock.vue';
defineProps<{ text: string }>();
const raw = ref(false);
</script>
<template>
  <div class="artifact-markdown">
    <div class="viz-caption">
      <button type="button" class="quiet-action" :aria-pressed="raw" @click="raw = !raw">
        Raw
      </button>
    </div>
    <CodeBlock v-if="raw" :code="text" lang="markdown" />
    <div v-else class="lyk-reading">
      <AssistantMessage class="markdown-body" :text="text" prose />
    </div>
  </div>
</template>
<style scoped>
.viz-caption {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}
.artifact-markdown .viz-caption .quiet-action {
  padding: 6px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  color: var(--text);
  font: inherit;
  cursor: pointer;
}
.artifact-markdown .viz-caption .quiet-action:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
