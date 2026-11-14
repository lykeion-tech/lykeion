<script setup lang="ts">
import { ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import type { DecisionItem } from '../../api/types';

// A machine's wait, which its owner resolves once the cause is dealt with, or
// a study whose runs its machines kept failing, answered through its options.
const props = defineProps<{ item: DecisionItem }>();
const client = useQueryClient();
const busy = ref(false);
const error = ref('');

async function resolve() {
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/decisions/${props.item.id}/resolve`);
    await client.invalidateQueries({ queryKey: keys.decisionsAll(props.item.workspace_id) });
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
      v-if="item.kind === 'waiting_for_machine' && !item.resolved_at"
      class="button"
      :disabled="busy"
      @click="resolve"
    >
      Resolve
    </button>
  </div>
</template>
<style scoped src="../../ui/resources.css"></style>
