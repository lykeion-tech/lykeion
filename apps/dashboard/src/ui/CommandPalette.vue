<script setup lang="ts">
import { computed } from 'vue';
import PaletteModal from './PaletteModal.vue';
import { useCards, useResearches, useWorkspace } from '../api/queries';
import { paletteEntries } from '../lib/search';

const emit = defineEmits<{ close: [] }>();
const studies = useCards();
const researches = useResearches();
const workspace = useWorkspace();
const entries = computed(() =>
  paletteEntries(
    researches.data.value ?? [],
    studies.data.value ?? [],
    workspace.data.value?.display_prefix ?? '',
  ),
);
</script>

<template>
  <PaletteModal
    :entries="entries"
    :loading="studies.isPending.value || researches.isPending.value"
    :failure="studies.error.value || researches.error.value"
    label="Search Researches and Studies"
    list-label="Researches and Studies"
    @close="emit('close')"
  />
</template>
