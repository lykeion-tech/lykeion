<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useMe } from '../../../api/queries';
import {
  emptyResearchProfile,
  saveResearchProfile,
  updateResearchProfileCache,
  useResearchProfile,
} from '../../../api/research-profile';
import ResearchProfileFields from './ResearchProfileFields.vue';

const client = useQueryClient();
const me = useMe();
const profile = useResearchProfile();
const editor = ref(emptyResearchProfile());
const baseline = ref(emptyResearchProfile());
const busy = ref(false);
const error = ref('');
const status = ref('');
let disposed = false;
onUnmounted(() => (disposed = true));

const equal = (a: typeof editor.value, b: typeof editor.value) =>
  a.identity === b.identity &&
  a.links.length === b.links.length &&
  a.links.every((link, index) => link === b.links[index]);
const dirty = computed(() => !equal(editor.value, baseline.value));
watch(
  profile.data,
  (data) => {
    const draft = data?.draft ?? emptyResearchProfile();
    if (!dirty.value) editor.value = draft;
    baseline.value = draft;
  },
  { immediate: true },
);

async function saveDraft() {
  const account = me.data.value?.id;
  if (!account || busy.value || disposed || !dirty.value) return;
  busy.value = true;
  error.value = '';
  status.value = '';
  const current = () => !disposed && me.data.value?.id === account;
  try {
    const result = await saveResearchProfile(editor.value, { updatePublished: true });
    updateResearchProfileCache(client, account, result, result.workspace_ids);
    if (!current()) return;
    editor.value = result.draft ?? emptyResearchProfile();
    status.value = result.workspace_ids.length
      ? 'Profile updated in shared workspaces.'
      : 'Profile saved privately.';
  } catch (e) {
    if (current() && (e as Error).name !== 'AbortError') error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
</script>

<template>
  <section class="research-profile-panel" aria-label="Research profile">
    <h3>Research profile</h3>
    <p v-if="profile.isPending.value" class="muted" role="status">Loading your research profile…</p>
    <p v-else-if="profile.error.value" class="inline-error" role="alert">
      {{ profile.error.value.message }}
      <button class="text-button" type="button" @click="profile.refetch()">Retry</button>
    </p>
    <template v-else>
      <form class="profile-card" aria-label="Edit research profile" @submit.prevent="saveDraft">
        <ResearchProfileFields v-model:profile="editor" :disabled="busy" hide-background-label />
        <button type="submit" class="button primary" :disabled="busy || !dirty">
          Save profile
        </button>
      </form>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <p v-if="status" class="muted" role="status">{{ status }}</p>
    </template>
  </section>
</template>

<style scoped>
.research-profile-panel {
  display: grid;
  gap: 12px;
  margin-top: 28px;
}
.research-profile-panel h3,
.research-profile-panel p {
  margin: 0;
}
.research-profile-panel h3 {
  font-size: 14px;
  font-weight: 600;
}
.profile-card {
  display: grid;
  gap: 16px;
  min-width: 0;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--input);
}
.profile-card :deep(textarea) {
  background: var(--fill-raised);
}
.research-profile-panel > p {
  font-size: 13px;
}
.profile-card .button {
  justify-self: start;
}
</style>
