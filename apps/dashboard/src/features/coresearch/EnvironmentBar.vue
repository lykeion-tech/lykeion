<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { projectPath, keys, useCoresearchEnvironment } from './api';
import type { ProjectRow, ChatRow } from './types';
const props = defineProps<{
  project: ProjectRow;
  chat: ChatRow;
  owner: boolean;
  disabled?: boolean;
}>();
const environment = useCoresearchEnvironment(() => props.project.id),
  client = useQueryClient();
const python = ref(props.project.python_version ?? ''),
  busy = ref(false),
  error = ref('');
const setup = computed(
  () =>
    [...(environment.data.value?.setups ?? [])].sort((a, b) =>
      b.created_at.localeCompare(a.created_at),
    )[0],
);
const inProgress = computed(
  () => !!setup.value && ['queued', 'running'].includes(setup.value.status),
);
const kind = computed(() =>
  !setup.value
    ? 'unreported'
    : ['failed', 'cancelled'].includes(setup.value.status)
      ? 'failed'
      : inProgress.value
        ? 'progress'
        : 'ready',
);
async function perform() {
  if (busy.value || props.disabled || inProgress.value) return;
  const ws = workspaceId.value,
    p = props.project.id,
    c = props.chat.id;
  busy.value = true;
  error.value = '';
  try {
    await api(
      'POST',
      `${projectPath(p)}/environment`,
      {
        chat_id: c,
        python: python.value.trim() || null,
        conda:
          [...(environment.data.value?.setups ?? [])]
            .filter((item) => item.status === 'ok')
            .sort((a, b) => b.created_at.localeCompare(a.created_at))[0]?.conda ?? [],
      },
      undefined,
      { workspace: ws },
    );
    await client.invalidateQueries({ queryKey: keys.coresearchEnvironment(ws, p) });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="envbar" data-testid="environment-bar">
    <div class="envbar-row">
      <span class="envbar-trigger-text">{{
        project.python_version ? `Python ${project.python_version}` : 'Python version not recorded'
      }}</span>
      <p class="envbar-state" :class="`envbar-state--${kind}`" role="status">
        <span class="envbar-state-label">{{
          setup
            ? setup.status
            : environment.isPending.value
              ? 'Loading environment…'
              : environment.error.value
                ? 'Environment unavailable'
                : 'No setup recorded'
        }}</span>
      </p>
      <div v-if="owner" class="envbar-actions">
        <button
          type="button"
          class="envbar-btn envbar-btn--primary"
          :disabled="disabled || busy || inProgress"
          @click="perform"
        >
          Set up
        </button>
      </div>
    </div>
    <div
      v-if="inProgress"
      class="envbar-progress"
      role="progressbar"
      aria-label="Environment setup progress"
    >
      <div class="envbar-progress__indicator" />
    </div>
    <details v-if="setup || owner" class="envbar-details" :open="setup?.status === 'failed'">
      <summary class="envbar-summary">Full details</summary>
      <div class="envbar-details-body">
        <p v-if="setup" class="envbar-packages">
          <span class="envbar-detail-head">Requested</span>
          {{ setup.python ? `Python ${setup.python}` : 'No Python change' }} ·
          {{ setup.conda.length ? setup.conda.join(', ') : 'No conda packages' }}
        </p>
        <pre v-if="setup?.log" class="envbar-log">{{ setup.log }}</pre>
        <p v-if="setup?.failure_reason" class="envbar-excerpt">{{ setup.failure_reason }}</p>
        <div v-if="owner" class="flex flex-col gap-2">
          <label class="type-small envbar-python"
            >Python version<input
              v-model="python"
              class="envbar-trigger"
              aria-label="Setup Python version"
              placeholder="Leave blank to keep recorded version"
              :disabled="disabled || busy"
            />
          </label>
        </div>
      </div>
    </details>
    <p v-if="environment.error.value" class="envbar-excerpt" role="alert">
      {{ environment.error.value.message }}
    </p>
    <p v-if="error" class="envbar-excerpt" role="alert">{{ error }}</p>
  </div>
</template>
<style scoped>
.envbar-python {
  display: flex;
  align-items: center;
  gap: 8px;
}
.envbar-python input {
  width: 90px;
  max-width: 100%;
}
</style>
