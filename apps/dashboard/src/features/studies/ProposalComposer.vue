<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../../api/client';
import { keys } from '../../api/keys';
import { useAgents, useMe, useResearches, useStudyProposals, useTeams } from '../../api/queries';
import { workspaceId } from '../../api/session';
import type { StudyProposal, StudyProposalContent } from '../../api/types';
import { stages, columnName } from '../../lib/studies';
import Modal from '../../ui/Modal.vue';
import ModelSwitcher from '../coresearch/cowrite/ModelSwitcher.vue';
import '../coresearch/l2.css';

const props = defineProps<{ proposal: StudyProposal }>();
const emit = defineEmits<{ close: []; started: [cardId: string] }>();
const client = useQueryClient();
const proposals = useStudyProposals();
const projects = useResearches();
const agents = useAgents();
const teams = useTeams('', '');
const me = useMe();
const workspace = workspaceId.value;
const saved = ref(props.proposal);
const id = saved.value.id;
const operationId = crypto.randomUUID();
const description = ref(saved.value.description);
const stage = ref<StudyProposalContent['stage']>(saved.value.stage);
function assignment(p: StudyProposal) {
  return p.assignee.type === 'agent'
    ? 'agent:' + p.assignee.agent_id
    : 'team:' + p.assignee.pack_id;
}
const selected = ref(assignment(saved.value));
const project = ref(saved.value.project_id ?? '');
const busy = ref(false);
const error = ref('');
let active = true;
onUnmounted(() => {
  active = false;
});
const editable = computed(() => me.data.value?.id === saved.value.owner_id);
const stageOptions = stages.map((value) => ({ value, label: columnName(value) }));
const projectOptions = computed(() => [
  { value: '', label: 'No project' },
  ...(projects.data.value ?? []).map((p) => ({ value: p.id, label: p.name })),
]);
const latest = computed(() => proposals.data.value?.find((p) => p.id === id));
const newer = computed(() => latest.value && latest.value.revision > saved.value.revision);
const changed = computed(
  () =>
    saved.value.description !== description.value ||
    saved.value.stage !== stage.value ||
    assignment(saved.value) !== selected.value ||
    (saved.value.project_id ?? '') !== project.value,
);
const options = computed(() => [
  ...(agents.data.value ?? [])
    .filter(
      (a) =>
        a.stage === stage.value &&
        !a.archived_at &&
        a.built_in_key !== 'aristotle' &&
        (a.visibility === 'workspace' || a.owner_id === me.data.value?.id),
    )
    .map((a) => ({ value: 'agent:' + a.id, label: a.name })),
  ...(teams.data.value ?? [])
    .filter((t) => (t.added || t.source === 'shipped') && (!t.stage || t.stage === stage.value))
    .map((t) => ({ value: 'team:' + t.id, label: t.name })),
]);
const valid = computed(
  () => description.value.trim() && options.value.some((o) => o.value === selected.value),
);
const assigneeOptions = computed(() => [
  { value: '', label: 'Choose an agent or Team', disabled: true },
  ...(selected.value && !options.value.some((option) => option.value === selected.value)
    ? [
        {
          value: selected.value,
          label: `Unavailable ${selected.value.startsWith('agent:') ? 'agent' : 'Team'}`,
          disabled: true,
        },
      ]
    : []),
  ...options.value,
]);
function changeStage(value: string) {
  stage.value = value as StudyProposalContent['stage'];
  if (!options.value.some((o) => o.value === selected.value)) selected.value = '';
}
function loadLatest() {
  const p = latest.value;
  if (!p) return;
  saved.value = p;
  description.value = p.description;
  stage.value = p.stage;
  selected.value = assignment(p);
  project.value = p.project_id ?? '';
  error.value = '';
}
function beforeClose() {
  if (busy.value) return false;
  if (!editable.value || !changed.value) return true;
  if (newer.value || !valid.value) return confirm('Close without saving your changes?');
  return submit(false);
}
async function submit(start: boolean) {
  if (
    !editable.value ||
    busy.value ||
    newer.value ||
    !valid.value ||
    workspace !== workspaceId.value
  )
    return false;
  const path = '/api/study-proposals/' + id;
  const current = () => active && workspace === workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    let card: string | undefined;
    if (changed.value) {
      const [type, target] = selected.value.split(':');
      saved.value = await api<StudyProposal>(
        'PUT',
        path,
        {
          revision: saved.value.revision,
          // This form doesn't edit these, and a save replaces the whole proposal.
          title: saved.value.title,
          priority: saved.value.priority,
          description: description.value,
          stage: stage.value,
          project_id: project.value || null,
          assignee: type === 'agent' ? { type, agent_id: target } : { type, pack_id: target },
        },
        undefined,
        { workspace },
      );
      await client.invalidateQueries({ queryKey: keys.proposals(workspace) });
    }
    if (start && current()) {
      const receipt = await api<{ card_id: string }>(
        'POST',
        path + '/start',
        { operation_id: operationId, revision: saved.value.revision },
        undefined,
        { workspace },
      );
      card = receipt.card_id;
      await Promise.all([
        client.invalidateQueries({ queryKey: keys.proposals(workspace) }),
        client.invalidateQueries({ queryKey: keys.cards(workspace) }),
      ]);
    }
    if (!current()) return false;
    if (card) emit('started', card);
    return true;
  } catch (e) {
    if (current()) {
      if (e instanceof ApiError && e.status === 409) void proposals.refetch();
      error.value = (e as Error).message;
    }
    return false;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <Modal
    v-slot="{ close }"
    title="To-do"
    :before-close="beforeClose"
    @close="emit('close')"
  >
    <form class="proposal-form" @submit.prevent="submit(true)">
      <h2>To-do</h2>
      <fieldset :disabled="!editable || busy">
        <label>Description<textarea v-model="description" required rows="5" autofocus /></label>
        <div class="proposal-field">
          <span>Stage</span>
          <div class="coresearch agent-model-picker">
            <ModelSwitcher
              :model-value="stage"
              :models="stageOptions"
              label="Stage"
              :disabled="!editable || busy"
              open-up
              @update:model-value="changeStage"
            />
          </div>
        </div>
        <div class="proposal-field">
          <span>Assignee</span>
          <div class="coresearch agent-model-picker">
            <ModelSwitcher
              v-model="selected"
              :models="assigneeOptions"
              label="Assignee"
              :disabled="!editable || busy"
              open-up
            />
          </div>
        </div>
        <div class="proposal-field">
          <span>Project</span>
          <div class="coresearch agent-model-picker">
            <ModelSwitcher
              v-model="project"
              :models="projectOptions"
              label="Project"
              :disabled="!editable || busy"
              open-up
            />
          </div>
        </div>
      </fieldset>
      <p v-if="newer" role="alert">
        This proposal changed.
        <button type="button" class="text-button" @click="loadLatest">Load latest revision</button>
      </p>
      <p v-if="error" role="alert">{{ error }}</p>
      <footer>
        <button type="button" class="button compact" :disabled="busy" @click="close">Close</button>
        <button
          v-if="editable"
          class="button primary compact"
          :disabled="busy || !!newer || !valid"
        >
          Start study
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.proposal-form {
  padding: 20px;
  display: grid;
  gap: 16px;
  width: 100%;
}
h2 {
  margin: 0;
  font-size: 17px;
}
fieldset {
  display: grid;
  gap: 12px;
  margin: 0;
  padding: 0;
  border: 0;
  min-width: 0;
}
label,
.proposal-field {
  display: grid;
  gap: 5px;
  font-size: 13px;
  min-width: 0;
}
textarea {
  width: 100%;
  padding: 8px;
  background: var(--input);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  resize: vertical;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
}
</style>
