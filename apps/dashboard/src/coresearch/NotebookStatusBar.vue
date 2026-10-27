<script setup lang="ts">
import type { ChatRow } from './types';
import KernelControls from './KernelControls.vue';
defineProps<{
  projectId: string;
  chat: ChatRow;
  count: number;
  owner: boolean;
  disabled?: boolean;
}>();
</script>
<template>
  <div class="nbp-status" data-testid="notebook-status">
    <span class="nbp-status-fact"
      >Kernel ·
      <span
        class="nbp-status-state"
        :class="`nbp-status-state--${chat.kernel_state === 'busy' ? 'running' : chat.kernel_state}`"
        >{{ chat.kernel_state }}</span
      ></span
    ><span class="nbp-status-right"
      ><KernelControls
        v-if="owner"
        :project-id="projectId"
        :chat-id="chat.id"
        :disabled="disabled"
      /><span class="nbp-status-count">{{ count === 1 ? '1 cell' : `${count} cells` }}</span></span
    >
  </div>
</template>
<style scoped>
.nbp-status,
.nbp-status-right {
  flex-wrap: wrap;
}
.nbp-status-count {
  white-space: nowrap;
}
</style>
