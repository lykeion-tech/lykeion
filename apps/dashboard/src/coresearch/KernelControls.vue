<script setup lang="ts">
import { ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { workspaceId } from '../api/session';
import { chatPath, keys } from './api';
const props = defineProps<{ projectId: string; chatId: string; disabled?: boolean }>();
const client = useQueryClient(),
  busy = ref(false),
  error = ref('');
async function perform(op: 'interrupt' | 'restart') {
  if (busy.value || props.disabled) return;
  const ws = workspaceId.value,
    p = props.projectId,
    c = props.chatId;
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `${chatPath(p, c)}/kernel`, { op }, undefined, { workspace: ws });
    await client.invalidateQueries({ queryKey: keys.coresearchChats(ws, p) });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <span class="nbp-status-right"
    ><button
      type="button"
      class="nbp-status-action"
      :disabled="disabled || busy"
      @click="perform('interrupt')"
    >
      Interrupt</button
    ><button
      type="button"
      class="nbp-status-action"
      :disabled="disabled || busy"
      @click="perform('restart')"
    >
      Restart</button
    ><span v-if="error" role="alert" class="text-danger type-small">{{ error }}</span></span
  >
</template>
<style scoped>
.nbp-status-right {
  flex-wrap: wrap;
}
.nbp-status-action {
  white-space: nowrap;
}
</style>
