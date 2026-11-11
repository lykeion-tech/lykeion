<script setup lang="ts">
import Icon from './Icon.vue';

defineProps<{ newTo: string; newLabel: string; searchLabel: string }>();
const query = defineModel<string>({ default: '' });
const emit = defineEmits<{
  new: [event: MouseEvent];
}>();
</script>
<template>
  <div class="rail-actions">
    <RouterLink
      :to="newTo"
      class="nav-item"
      :aria-label="newLabel"
      exact-active-class="selected"
      @click="emit('new', $event)"
      ><Icon name="plus" :size="16" />New</RouterLink
    >
    <label class="nav-item rail-search">
      <Icon name="search" :size="16" />
      <input v-model="query" type="search" placeholder="Search" :aria-label="searchLabel" />
    </label>
  </div>
</template>
<style scoped>
.rail-actions {
  flex: none;
  margin-bottom: 8px;
}
.rail-search {
  cursor: text;
}
.rail-search input {
  flex: 1;
  min-width: 0;
  border: 0;
  background: transparent;
  padding: 0;
  font: inherit;
  color: var(--text);
  outline: none;
}
.rail-search:focus-within {
  background: var(--hover);
}
</style>
