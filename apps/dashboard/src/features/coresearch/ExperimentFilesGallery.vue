<script setup lang="ts">
import { computed, ref } from 'vue';
import type { FileRow } from './types';
import ExperimentFileCard from './ExperimentFileCard.vue';
import Icon from '../../ui/Icon.vue';
const props = defineProps<{
  projectId: string;
  files: FileRow[];
  loading?: boolean;
  error?: string;
}>();
defineEmits<{ open: [file: FileRow] }>();
const search = ref(''),
  list = ref(false);
const filtered = computed(() => {
  const q = search.value.trim().toLocaleLowerCase();
  return q ? props.files.filter((f) => f.path.toLocaleLowerCase().includes(q)) : props.files;
});
</script>
<template>
  <div class="experiment-gallery">
    <div class="experiment-gallery-tools">
      <input v-model="search" aria-label="Search files" placeholder="Search files…" />
      <button type="button" aria-label="Grid view" :aria-pressed="!list" @click="list = false">
        <Icon name="board" :size="14" />
      </button>
      <button type="button" aria-label="List view" :aria-pressed="list" @click="list = true">
        <Icon name="list" :size="14" />
      </button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="!files.length && loading">Loading files…</p>
    <div v-else-if="!files.length && !error" class="experiment-empty">
      <Icon name="file" :size="30" />
      <h2>No files yet</h2>
      <p>Saved files are kept here.</p>
    </div>
    <p v-else-if="!files.length">Files could not be read.</p>
    <p v-else-if="!filtered.length">No files match.</p>
    <div v-else class="experiment-gallery-items" :class="{ 'is-list': list }">
      <ExperimentFileCard
        v-for="file in filtered"
        :key="file.id"
        :project-id="projectId"
        :file="file"
        :list="list"
        @open="$emit('open', $event)"
      />
    </div>
  </div>
</template>
<style scoped>
.experiment-gallery {
  display: flex;
  flex: 1;
  flex-direction: column;
  padding: 0 12px 12px;
}
.experiment-gallery-tools {
  display: flex;
  align-items: center;
  gap: 6px;
  border-bottom: 1px solid var(--border);
  padding-bottom: 10px;
}
.experiment-gallery-tools input {
  flex: 1;
  min-width: 0;
  background: transparent;
  border: 0;
  color: var(--text);
  padding: 8px 4px;
}
.experiment-gallery-tools input:focus {
  outline: none;
}
.experiment-gallery-tools input:focus-visible {
  box-shadow: 0 2px 0 var(--focus-ring);
}
.experiment-gallery-tools button {
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--muted);
  padding: 5px 7px;
  cursor: pointer;
}
.experiment-gallery-tools button:hover,
.experiment-gallery-tools button[aria-pressed='true'] {
  background: var(--hover);
  color: var(--text);
}
.experiment-gallery-items {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(160px, 100%), 1fr));
  gap: 10px;
  margin-top: 12px;
}
.experiment-gallery-items.is-list {
  grid-template-columns: 1fr;
  gap: 6px;
}
.experiment-gallery > p {
  color: var(--muted);
  font-size: 13px;
}
.experiment-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  color: var(--muted);
}
.experiment-empty h2 {
  margin: 12px 0 4px;
  color: var(--text);
  font-size: 18px;
}
.experiment-empty p {
  margin: 0;
  font-size: 13px;
}
</style>
