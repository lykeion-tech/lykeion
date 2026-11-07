<script setup lang="ts">
import { ref } from 'vue';
import Icon from '../../ui/Icon.vue';
import type { Picked } from '../../lib/attachments';
const props = defineProps<{ items: Picked[]; error: string; disabled?: boolean }>();
const emit = defineEmits<{ pick: [files: File[]]; remove: [key: number] }>();
const input = ref<HTMLInputElement>();
function choose(event: Event) {
  const field = event.target as HTMLInputElement;
  const files = Array.from(field.files ?? []);
  field.value = '';
  if (!props.disabled && files.length) emit('pick', files);
}
</script>
<template>
  <div class="file-picker">
    <div v-if="items.length" class="pending-files" aria-label="Files to attach">
      <span v-for="file in items" :key="file.key" class="pending-file">
        {{ file.name }}
        <span v-if="!file.id && !file.error" role="status"
          >{{ Math.round(file.progress * 100) }}%</span
        >
        <span v-if="file.error" role="alert">{{ file.error }}</span>
        <button
          type="button"
          :disabled="disabled"
          :aria-label="`Remove ${file.name}`"
          @click="emit('remove', file.key)"
        >
          ×
        </button>
      </span>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <button
      type="button"
      class="attach"
      aria-label="Attach files"
      :disabled="disabled"
      @click="input?.click()"
    >
      <Icon name="attachment" :size="15" />
    </button>
    <input ref="input" type="file" multiple hidden @change="choose" />
  </div>
</template>
<style scoped>
.file-picker {
  min-width: 0;
}
.attach {
  display: inline-grid;
  place-items: center;
  width: 26px;
  height: 26px;
  color: var(--muted);
  border-radius: 4px;
}
.attach:hover {
  color: var(--text);
  background: var(--hover);
}
.pending-files {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.pending-file {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.pending-file button {
  margin-left: 4px;
}
p {
  color: var(--error-text);
  font-size: 13px;
}
</style>
