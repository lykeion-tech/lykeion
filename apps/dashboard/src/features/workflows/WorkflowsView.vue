<script setup lang="ts">
import { computed, ref } from 'vue';
import Icon from '../../ui/Icon.vue';
import { useWorkflows } from '../../api/workflows';
import { byDomain } from '../../lib/workflows';

const search = ref('');
const workflows = useWorkflows();
const groups = computed(() => byDomain(workflows.data.value?.workflows ?? [], search.value));
</script>
<template>
  <section class="directory-page workflows-page">
    <p v-if="workflows.error.value" class="inline-error" role="alert">
      {{ workflows.error.value.message }}
    </p>
    <template v-if="workflows.data.value">
      <p class="workflows-caption">
        From Terminal-Bench-Science {{ workflows.data.value.release }}
      </p>
      <div class="agent-toolbar">
        <label class="agent-search"
          ><Icon name="search" :size="14" /><input
            v-model="search"
            placeholder="Search Workflows..."
            aria-label="Search Workflows"
        /></label>
      </div>
      <section v-for="group in groups" :key="group.domain" class="workflow-group">
        <h2>
          <span aria-hidden="true">{{ group.emoji }}</span> {{ group.label }}
        </h2>
        <div class="workflow-cards">
          <RouterLink
            v-for="w in group.workflows"
            :key="w.slug"
            :to="'/workflows/' + w.slug"
            class="workflow-card"
          >
            <span aria-hidden="true">{{ group.emoji }}</span>
            <strong>{{ w.name }}</strong>
            <small>{{ w.description }}</small>
          </RouterLink>
        </div>
      </section>
      <p v-if="!groups.length" class="workflows-caption">No Workflows match.</p>
    </template>
  </section>
</template>
<style src="../../ui/directory.css" />
<style scoped>
.workflows-page {
  padding-bottom: 32px;
}
.workflows-caption {
  color: var(--muted);
  margin: 0 0 12px;
}
.workflow-group {
  margin-top: 24px;
}
.workflow-group h2 {
  margin: 0 0 10px;
  font-weight: 600;
}
.workflow-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 12px;
}
.workflow-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  color: var(--text);
  text-decoration: none;
}
.workflow-card:hover {
  background: var(--fill-card);
}
.workflow-card small {
  color: var(--muted);
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
</style>
