<script setup lang="ts">
import Icon from '../../Icon.vue';
withDefaults(
  defineProps<{ query: string; loading: boolean; error?: string; count: number; noun?: string }>(),
  { noun: 'skills' },
);
defineEmits<{ 'update:query': [value: string]; retry: [] }>();
</script>
<template>
  <div class="library-filters">
    <label class="capability-search"
      ><Icon name="search" :size="16" /><input
        type="search"
        :aria-label="`Search ${noun}`"
        :placeholder="`Search ${noun}…`"
        :value="query"
        @input="$emit('update:query', ($event.target as HTMLInputElement).value)" /></label
    ><span class="all-control" aria-label="Current scope: All"
      >All <Icon name="down" :size="12"
    /></span>
  </div>
  <p class="library-caption">Workspace library</p>
  <p v-if="loading" role="status">Loading {{ noun }}…</p>
  <div v-else-if="error" role="alert">
    <p>{{ error }}</p>
    <button class="button" @click="$emit('retry')">Try again</button>
  </div>
  <p v-else-if="!count" class="library-empty">
    {{
      query
        ? `No ${noun} match your search.`
        : noun === 'skills'
          ? 'No skills yet. Add a skill to your workspace.'
          : `No ${noun} yet.`
    }}
  </p>
  <div v-else class="capability-rows"><slot /></div>
</template>
<style scoped>
.library-filters {
  display: flex;
  gap: 8px;
  margin-bottom: 20px;
}
.library-filters .capability-search {
  flex: 1;
  margin: 0;
}
.all-control {
  display: flex;
  align-items: center;
  gap: 6px;
  background: var(--input);
  color: var(--text);
  border: 0;
  border-radius: 6px;
  padding: 8px 10px;
}
.capability-search {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--input);
  border-radius: 7px;
  padding: 0 10px;
  margin: 0 0 20px;
  height: 34px;
  color: var(--muted);
}
input {
  flex: 1;
  width: 100%;
  background: transparent;
  border: 0;
  outline: 0;
  color: var(--text);
  font: inherit;
  min-width: 0;
}
.capability-search:focus-within {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
.library-caption,
.library-empty {
  color: var(--muted);
  font-size: 12px;
}
.library-caption {
  margin: 0 0 6px;
}
.library-empty {
  padding: 24px 0;
}
</style>
