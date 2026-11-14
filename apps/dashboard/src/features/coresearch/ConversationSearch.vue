<script setup lang="ts">
import { computed } from 'vue';
import PaletteModal from '../../ui/PaletteModal.vue';
import { useAristotle } from './aristotle';
import type { PaletteEntry } from '../../lib/search';

const emit = defineEmits<{ close: [] }>();
const state = useAristotle();
const entries = computed<PaletteEntry[]>(() =>
  (state.conversations.data.value ?? []).map((c) => {
    const subtitle = c.shared
      ? 'Shared paper'
      : c.preview || (c.is_welcome ? 'Your research companion' : 'New conversation');
    return {
      id: c.shared ? `shared:${c.project_id}` : c.id,
      kind: 'conversation',
      label: c.title,
      title: c.title,
      subtitle,
      path:
        c.mode === 'cowrite'
          ? `/coresearch/${c.project_id}/${c.chat_id}`
          : `/coresearch/conversations/${c.id}`,
      updated: c.shared ? c.updated_at : undefined,
      search: `${c.title} ${c.preview ?? ''}`,
    };
  }),
);
</script>

<template>
  <PaletteModal
    :entries="entries"
    :loading="state.conversations.isPending.value"
    :failure="state.bootstrap.error.value || state.conversations.error.value"
    label="Search conversations"
    list-label="Conversations"
    @close="emit('close')"
  />
</template>
