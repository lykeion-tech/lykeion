<script setup lang="ts">
import type { ProjectRow, ChatRow } from './types';
import ComputePanel from './cowrite/ComputePanel.vue';
defineProps<{ project: ProjectRow; chat: ChatRow; owner: boolean }>();
</script>
<template>
  <ComputePanel
    role="tabpanel"
    aria-label="Compute"
    :project-id="project.id"
    :title="project.name"
    :disabled="!!project.archived_at"
  />
</template>
