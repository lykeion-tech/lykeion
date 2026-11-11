<script setup lang="ts">
import { computed } from 'vue';
import PaletteModal from '../../ui/PaletteModal.vue';
import { useDecisionRows } from './decisionRows';
import { decisionGroupOf, decisionGroups, timeAgo } from '../../lib/studies';
import type { PaletteEntry } from '../../lib/search';

const emit = defineEmits<{ close: [] }>();
const rows = useDecisionRows();
const groupName = (id: string) =>
  decisionGroups.find((group) => group.id === id)?.label ?? 'Decisions';
const entries = computed<PaletteEntry[]>(() =>
  rows.shown.value.map((item) => {
    const title = rows.titleOf(item);
    const line = rows.line(item);
    const group = item.archived_at ? 'Archived' : groupName(decisionGroupOf(item.kind));
    return {
      id: item.id,
      kind: 'decision',
      label: title || line,
      title: title || line,
      subtitle: line,
      path: `/decisions/${item.id}`,
      search: [group, title, line, item.text, item.context].filter(Boolean).join(' '),
      rows: [
        { label: 'Group', value: group },
        { label: 'Status', value: item.status.charAt(0).toUpperCase() + item.status.slice(1) },
        { label: 'Raised', value: timeAgo(item.created_at) },
      ],
    };
  }),
);
</script>

<template>
  <PaletteModal
    :entries="entries"
    :loading="rows.pending.value"
    :failure="rows.error.value"
    label="Search decisions"
    list-label="Decisions"
    @close="emit('close')"
  />
</template>
