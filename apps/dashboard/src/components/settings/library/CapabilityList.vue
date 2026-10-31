<script setup lang="ts">
import Icon from '../../../ui/Icon.vue';
import { ref } from 'vue';
const menu = ref<HTMLDetailsElement | null>(null);
function closeMenu() {
  if (menu.value) menu.value.open = false;
}
function onFocusOut(event: FocusEvent) {
  if (!menu.value?.contains(event.relatedTarget as Node | null)) closeMenu();
}
withDefaults(
  defineProps<{
    query: string;
    field?: string;
    fields?: string[];
    loading: boolean;
    error?: string;
    count: number;
    noun?: string;
  }>(),
  { noun: 'skills' },
);
defineEmits<{ 'update:query': [value: string]; 'update:field': [value: string]; retry: [] }>();
</script>
<template>
  <div class="library-filters">
    <label class="capability-search"
      ><Icon name="search" :size="16" /><input
        type="search"
        :aria-label="`Search ${noun}`"
        :placeholder="`Search ${noun}…`"
        :value="query"
        @input="$emit('update:query', ($event.target as HTMLInputElement).value)"
    /></label>
    <details
      v-if="fields"
      ref="menu"
      class="field-menu"
      @focusout="onFocusOut"
      @keydown.esc="closeMenu"
    >
      <summary class="all-control" aria-label="Scientific field">
        {{ field || 'All' }} <Icon name="down" :size="12" />
      </summary>
      <div class="field-options" aria-label="Scientific fields">
        <button
          v-for="option in ['', ...fields]"
          :key="option"
          type="button"
          :aria-current="field === option ? 'true' : undefined"
          @click="
            $emit('update:field', option);
            closeMenu();
          "
        >
          {{ option || 'All' }}
        </button>
      </div>
    </details>
    <span v-else class="all-control" aria-label="Current scope: All"
      >All <Icon name="down" :size="12"
    /></span>
  </div>
  <p v-if="loading" role="status">Loading {{ noun }}…</p>
  <div v-else-if="error" role="alert">
    <p>{{ error }}</p>
    <button class="button" @click="$emit('retry')">Try again</button>
  </div>
  <p v-else-if="!count" class="library-empty">
    {{
      query || field
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
.field-menu {
  position: relative;
  max-width: 60%;
  flex: none;
}
.field-menu summary {
  cursor: pointer;
  list-style: none;
  text-align: right;
}
.field-menu summary::-webkit-details-marker {
  display: none;
}
.field-options {
  position: absolute;
  z-index: 20;
  right: 0;
  top: calc(100% + 4px);
  width: max-content;
  max-width: min(90vw, 420px);
  max-height: min(50vh, 360px);
  overflow: auto;
  scrollbar-width: none;
  background: var(--modal);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 4px;
  box-shadow: 0 8px 24px #0003;
}
.field-options::-webkit-scrollbar {
  display: none;
}
.field-options button {
  display: block;
  width: 100%;
  padding: 7px 9px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.field-options button:hover,
.field-options button:focus-visible {
  background: var(--selected);
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
input:focus-visible {
  outline: none;
  box-shadow: none;
}
input::-webkit-search-cancel-button {
  display: none;
}
.library-empty {
  color: var(--muted);
  font-size: 12px;
}
.library-empty {
  padding: 24px 0;
}
</style>
