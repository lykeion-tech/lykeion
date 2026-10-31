<script setup lang="ts">
import { computed } from 'vue';
import type { FileRow } from './types';
const props = defineProps<{
  files: FileRow[];
  limit?: number;
  activeId?: string;
  loading?: boolean;
  error?: string;
}>();
const emit = defineEmits<{ open: [file: FileRow]; seeAll: [] }>();
const shown = computed(() =>
  props.limit === undefined
    ? props.files
    : props.files.filter((f, i) => i < props.limit! || f.id === props.activeId),
);
</script>
<template>
  <div class="tsb-files">
    <p v-if="error" class="tsb-files-error" role="alert">{{ error }}</p>
    <div v-if="!files.length" class="tsb-tasks-empty">
      {{
        loading
          ? 'Loading files…'
          : error
            ? 'Files could not be read.'
            : 'No files yet — saved files are kept here.'
      }}
    </div>
    <div v-else class="tsb-files-group">
      <div
        v-for="file in shown"
        :key="file.id"
        class="tsb-file"
        :class="{ 'is-active': file.id === activeId }"
      >
        <button
          type="button"
          class="tsb-file-open"
          :aria-current="file.id === activeId ? 'true' : undefined"
          :title="file.path"
          @click="emit('open', file)"
        >
          <span class="tsb-file-glyph" aria-hidden="true"
            ><svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path
                d="M9 2H4a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V6L9 2Z M9 2v4h4"
                stroke="currentColor"
                stroke-width="1.2"
                stroke-linejoin="round"
              /></svg></span
          ><span class="tsb-file-name">{{ file.path.split('/').at(-1) }}</span>
        </button>
      </div>
    </div>
    <button
      v-if="shown.length < files.length"
      type="button"
      class="tsb-files-all"
      :aria-label="`See all ${files.length} files`"
      @click="emit('seeAll')"
    >
      See all · {{ files.length }}
    </button>
  </div>
</template>
