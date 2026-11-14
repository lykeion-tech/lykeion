<script setup lang="ts">
import { ref } from 'vue';
import Icon from '../../../ui/Icon.vue';
import AssistantMessage from '../AssistantMessage.vue';
import CodeBlock from '../CodeBlock.vue';
defineProps<{ text: string }>();
const raw = ref(false);
</script>
<template>
  <div class="artifact-markdown">
    <div class="viz-caption">
      <div class="view-modes" aria-label="Markdown display mode">
        <button
          type="button"
          aria-label="Rendered Markdown"
          title="Rendered Markdown"
          :aria-pressed="!raw"
          @click="raw = false"
        >
          <Icon name="eye" :size="16" />
        </button>
        <button
          type="button"
          aria-label="Markdown source"
          title="Markdown source"
          :aria-pressed="raw"
          @click="raw = true"
        >
          <Icon name="code" :size="16" />
        </button>
      </div>
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
.view-modes {
  display: flex;
  flex: none;
  background: var(--input);
  border-radius: 6px;
  padding: 2px;
}
.view-modes button {
  display: grid;
  place-items: center;
  border: 0;
  border-radius: 4px;
  padding: 5px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.view-modes button[aria-pressed='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 0 0 1px var(--border);
}
.view-modes button:focus-visible {
  outline: 2px solid var(--primary-text);
}
</style>
