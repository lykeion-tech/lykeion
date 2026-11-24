<script setup lang="ts">
import Icon from '../../ui/Icon.vue';

defineProps<{ icon: 'tasks' | 'chat'; title: string }>();
</script>

<template>
  <div class="project-empty">
    <Icon :name="icon" :size="36" />
    <h2>{{ title }}</h2>
  </div>
</template>

<style scoped>
.project-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  color: var(--muted);
  padding: 32px;
  text-align: center;
}
.project-empty h2 {
  font-size: 14px;
  font-weight: 400;
  margin: 0;
}
</style>
