<script setup lang="ts">
import { ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import type { InboxItem } from '../../api/types';
import { isMachineWait } from '../../lib/studies';

// A machine's wait, or a study whose run its machines kept failing: its
// owner resolves it once the cause is dealt with, which lets the machine take
// studies again or the run try again.
const props = defineProps<{ item: InboxItem }>();
const client = useQueryClient();
const busy = ref(false);
const error = ref('');

async function resolve() {
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/inbox/${props.item.id}/resolve`);
    await client.invalidateQueries({ queryKey: keys.inboxAll(props.item.workspace_id) });
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="conversation-empty">
    <Icon name="monitor" :size="38" />
    <p class="type-body">{{ item.text }}</p>
    <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
    <button
      v-if="isMachineWait(item) && !item.resolved_at"
      class="button"
      :disabled="busy"
      @click="resolve"
    >
      Resolve
    </button>
  </div>
</template>
<style scoped src="../../ui/resources.css"></style>
