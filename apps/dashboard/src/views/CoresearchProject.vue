<script setup lang="ts">
import { useCoresearchProject } from '../coresearch/api';
import '../coresearch/l2.css';
import '../coresearch/l2-tokens.css';
const props = defineProps<{ projectId: string; chatId?: string }>();
const project = useCoresearchProject(() => props.projectId);
</script>
<template>
  <section class="coresearch flex min-h-0 flex-1 flex-col p-5">
    <p v-if="project.error.value" role="alert">{{ project.error.value.message }}</p>
    <template v-else-if="project.data.value">
      <h1 class="type-title">{{ project.data.value.name }}</h1>
      <p class="type-small text-fg-subtle">
        The project is saved. The Coresearch workspace is not available in this build yet.
      </p>
    </template>
    <p v-else role="status">Loading project…</p>
    <RouterLink to="/coresearch">Back to Coresearch</RouterLink>
  </section>
</template>
