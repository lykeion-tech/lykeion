<script setup lang="ts">
import Icon from './Icon.vue';

defineProps<{ newTo: string; newLabel: string; searchLabel: string }>();
const emit = defineEmits<{
  new: [event: MouseEvent];
  search: [];
}>();
</script>
<template>
  <div class="rail-actions">
    <button
      type="button"
      class="nav-item palette-trigger"
      :aria-label="searchLabel"
      aria-keyshortcuts="Meta+K Control+K"
      @click="emit('search')"
    >
      <Icon name="search" :size="16" />Search…<kbd class="push-right">⌘K</kbd>
    </button>
    <!-- Captured, so a listener that takes the click over sees it before the
         router's own handler prevents the default. -->
    <RouterLink
      :to="newTo"
      class="nav-item"
      :aria-label="newLabel"
      exact-active-class="selected"
      aria-keyshortcuts="C"
      @click.capture="emit('new', $event)"
      ><Icon name="edit" :size="16" />New<kbd class="push-right">C</kbd></RouterLink
    >
  </div>
</template>
<style scoped>
.rail-actions {
  flex: none;
  margin-bottom: 8px;
}
</style>
