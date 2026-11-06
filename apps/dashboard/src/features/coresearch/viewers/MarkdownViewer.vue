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
    <CodeBlock v-if="raw" :code="text" lang="markdown" /><AssistantMessage
      v-else
      :text="text"
      prose
    />
  </div>
</template>
<style scoped>
.viz-caption {
  margin-bottom: 12px;
}
.viz-caption button {
  padding: 6px 4px;
}
</style>
