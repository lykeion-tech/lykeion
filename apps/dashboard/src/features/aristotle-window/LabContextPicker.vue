<script setup lang="ts">
import { computed, ref } from 'vue';
import { useCard, useCards, useNotebookRecords } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { studyDisplayTitle } from '../../lib/studies';
import { labRecordTitle } from './api';
import { ChevronLeft, FolderOpen } from '@lucide/vue';
const emit = defineEmits<{ pick: [value: { ref: string; title: string }] }>();
const cards = useCards(),
  browse = ref(''),
  search = ref('');
const study = useCard(browse),
  records = useNotebookRecords(browse);
const matches = (title: string) => title.toLowerCase().includes(search.value.trim().toLowerCase());
const studies = computed(() =>
  (cards.data.value ?? []).filter((c) => matches(studyDisplayTitle(c.title))),
);
const choices = computed(() =>
  (records.data.value ?? [])
    .map((r) => ({
      ref: 'record:' + r.id,
      title: labRecordTitle(r, study.data.value, workspaceId.value),
    }))
    .filter((r) => r.title && matches(r.title)),
);
function selectStudy(id: string) {
  browse.value = id;
  search.value = '';
}
</script>
<template>
  <label class="label"
    >Add context<input
      v-model="search"
      type="search"
      :placeholder="browse ? 'Find a record or file' : 'Find a study'"
      aria-label="Search context"
  /></label>
  <template v-if="browse">
    <button @click="selectStudy('')"><ChevronLeft :size="14" /> All studies</button>
    <button
      v-if="study.data.value"
      @click="
        emit('pick', { ref: 'study:' + browse, title: studyDisplayTitle(study.data.value.title) })
      "
    >
      Use this study
    </button>
    <button v-for="r in choices" :key="r.ref" @click="emit('pick', r)">{{ r.title }}</button>
    <button
      v-if="records.isError.value || study.isError.value"
      @click="
        records.refetch();
        study.refetch();
      "
    >
      Retry loading records
    </button>
    <small v-else-if="records.isFetching.value">Loading records…</small>
    <small v-else-if="!choices.length">No matching records or files.</small>
  </template>
  <template v-else>
    <div v-for="c in studies" :key="c.id" class="study-row">
      <button @click="emit('pick', { ref: 'study:' + c.id, title: studyDisplayTitle(c.title) })">
        {{ studyDisplayTitle(c.title) }}
      </button>
      <button
        :aria-label="'Browse records and files in ' + studyDisplayTitle(c.title)"
        title="Browse records and files"
        @click="selectStudy(c.id)"
      >
        <FolderOpen :size="16" />
      </button>
    </div>
    <button v-if="cards.isError.value" @click="cards.refetch()">Retry loading studies</button>
    <small v-else-if="!studies.length">No matching studies.</small>
  </template>
</template>
<style scoped>
.label {
  display: grid;
  gap: 5px;
  padding: 5px;
  color: var(--muted);
  font-size: 12px;
}
input {
  border: 1px solid var(--border);
  background: var(--input);
  color: var(--text);
  padding: 7px;
  border-radius: 7px;
}
.study-row {
  display: flex;
}
.study-row button:first-child {
  flex: 1;
}
small {
  display: block;
  padding: 6px;
  color: var(--muted);
}
</style>
