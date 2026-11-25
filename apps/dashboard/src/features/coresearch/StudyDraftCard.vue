<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useTeams } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { startCoresearchStudy, type CoresearchStudyDraft } from '../../api/coresearch-study';
import { columnName } from '../../lib/studies';
import { teamDisplayParts } from '../../lib/team-name';
import { useStudyComposer } from '../../state/study-composer';

const props = defineProps<{
  draft: CoresearchStudyDraft;
  researchId?: string | null;
  studyId?: string;
  studyDeleted?: boolean;
}>();
const client = useQueryClient();
const { openComposer } = useStudyComposer();
const teams = useTeams('', '');
const team = computed(() =>
  teams.data.value?.find((t) => t.source === 'shipped' && t.stage === props.draft.stage),
);
const launched = ref('');
const busy = ref(false);
const error = ref('');
const study = computed(() => props.studyId || launched.value);
const started = computed(() => props.studyDeleted || !!study.value);

function review() {
  const assignment = team.value
    ? { type: 'team' as const, teams: { [props.draft.stage]: team.value.id } }
    : undefined;
  openComposer(assignment, props.researchId ?? undefined, undefined, props.draft);
}

async function start() {
  if (busy.value || started.value || !team.value) return;
  const draft = props.draft;
  const workspace = workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    const id = await startCoresearchStudy(client, workspace, draft, {
      title: draft.title,
      description: draft.description,
      stage: draft.stage,
      priority: 'none',
      assignee: { type: 'team', pack_id: team.value.id },
      project_id: props.researchId ?? null,
    });
    if (workspaceId.value === workspace && props.draft.id === draft.id) launched.value = id;
  } catch (e) {
    if (workspaceId.value === workspace && props.draft.id === draft.id)
      error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="study-draft-card" :aria-label="started ? 'Study proposal' : 'Study draft'">
    <div class="study-draft-body">
      <p class="study-draft-label">{{ started ? 'Study proposal' : 'Study draft' }}</p>
      <h3>{{ draft.title }}</h3>
      <p>{{ draft.description }}</p>
      <p v-if="!started" class="study-draft-details">
        <span>{{ columnName(draft.stage) }}</span>
        <span v-if="team">{{ teamDisplayParts(team.name).label }}</span>
      </p>
    </div>
    <div class="study-draft-actions">
      <span v-if="studyDeleted">Study deleted</span>
      <RouterLink v-else-if="study" :to="`/studies/${study}`" class="button primary compact"
        >Open study</RouterLink
      >
      <template v-else>
        <button
          type="button"
          class="button primary compact"
          :disabled="busy || !team"
          @click="start"
        >
          {{ busy ? 'Starting…' : 'Start study' }}
        </button>
        <button type="button" class="button compact" :disabled="busy" @click="review">
          Edit draft
        </button>
      </template>
    </div>
    <p v-if="error" class="study-draft-error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.study-draft-card {
  margin: 12px 0 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--input);
  color: var(--text);
  overflow-wrap: anywhere;
}
.study-draft-body {
  padding: 14px;
}
.study-draft-body h3 {
  margin: 4px 0 6px;
  font-size: 16px;
}
.study-draft-body p {
  margin: 0;
  white-space: pre-wrap;
}
.study-draft-label,
.study-draft-details {
  color: var(--muted);
  font-size: 12px;
}
.study-draft-body .study-draft-details {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 10px;
}
.study-draft-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 9px 14px;
  border-top: 1px solid var(--border);
}
.study-draft-error {
  margin: 0;
  padding: 0 14px 10px;
  color: var(--error-text);
}
</style>
