<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import type { Research } from '../../api/types';
import { matchesQuery } from '../../lib/search';
import Icon from '../../ui/Icon.vue';

const props = defineProps<{
  projects?: Research[];
  pending: boolean;
  failed: boolean;
  busy: boolean;
  currentProject?: string | null;
  backLabel: string;
}>();
const emit = defineEmits<{
  back: [];
  select: [project: string | null];
  create: [name: string];
  retry: [];
  resize: [];
}>();
const search = ref('');
const creating = ref(false);
const projectName = ref('');
const nameInput = ref<HTMLInputElement>();
const matches = computed(() =>
  (props.projects ?? []).filter((project) => matchesQuery(project.name, search.value.trim())),
);

async function createProject() {
  creating.value = true;
  projectName.value = search.value.trim();
  await nextTick();
  nameInput.value?.focus();
}
watch(
  [() => matches.value.length, () => props.pending, () => props.failed, creating],
  () => {
    emit('resize');
  },
  { flush: 'post' },
);
</script>

<template>
  <div class="project-picker">
    <header>
      <button
        type="button"
        class="icon-button"
        :aria-label="backLabel"
        :disabled="busy"
        @click="emit('back')"
      >
        <Icon name="left" :size="15" />
      </button>
      <strong>Add to project</strong>
    </header>
    <form
      v-if="creating"
      @submit.prevent="projectName.trim() && emit('create', projectName.trim())"
    >
      <input
        ref="nameInput"
        v-model="projectName"
        aria-label="Project name"
        placeholder="Project name"
        :disabled="busy"
      />
      <button type="submit" class="button compact" :disabled="busy || !projectName.trim()">
        Create and add
      </button>
    </form>
    <template v-else>
      <input
        v-model="search"
        aria-label="Search projects"
        placeholder="Search projects…"
        :disabled="busy"
      />
      <button type="button" :disabled="busy" @click="createProject">
        <Icon name="plus" :size="15" />New project
      </button>
      <div class="project-picker-divider" />
      <div class="project-picker-list">
        <p v-if="pending" role="status">Loading projects…</p>
        <p v-else-if="failed" role="alert">
          Could not load projects.
          <button type="button" @click="emit('retry')">Retry</button>
        </p>
        <p v-else-if="!matches.length">No matching projects</p>
        <button
          v-for="project in failed || pending ? [] : matches"
          :key="project.id"
          type="button"
          :aria-pressed="currentProject === project.id"
          :disabled="busy || currentProject === project.id"
          @click="emit('select', project.id)"
        >
          <span>{{ project.name }}</span>
          <Icon v-if="currentProject === project.id" name="check" :size="14" class="push-right" />
        </button>
      </div>
      <button v-if="currentProject" type="button" :disabled="busy" @click="emit('select', null)">
        Remove from project
      </button>
    </template>
  </div>
</template>

<style scoped>
.project-picker header,
.project-picker form {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 0;
}
.project-picker button:not(.button) {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 30px;
  width: 100%;
  padding: 4px 8px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: inherit;
  text-align: left;
}
.project-picker header button.icon-button {
  width: 25px;
  min-height: 25px;
  padding: 4px;
}
.project-picker button:hover:not(:disabled),
.project-picker button:focus-visible {
  background: var(--hover);
}
.project-picker button:disabled:not([aria-pressed='true']) {
  opacity: 0.6;
}
.project-picker input {
  box-sizing: border-box;
  min-width: 0;
  width: 100%;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--surface);
  color: var(--text);
  font: inherit;
}
.project-picker > input {
  display: block;
  margin: 4px 0;
}
.project-picker form .button {
  flex: none;
}
.project-picker-divider {
  height: 1px;
  margin: 4px 0;
  background: var(--border);
}
.project-picker-list {
  max-height: 210px;
  overflow: auto;
}
.project-picker-list p {
  margin: 5px 8px;
  color: var(--muted);
  line-height: 1.4;
  overflow-wrap: anywhere;
}
.project-picker-list button span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
