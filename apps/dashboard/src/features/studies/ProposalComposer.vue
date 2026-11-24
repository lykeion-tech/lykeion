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

const props = defineProps<{ proposal?: StudyProposal; projectId?: string; initialDescription?: string; initialStage?: StudyProposalContent['stage']; initialAssignee?: string }>();
const emit = defineEmits<{ close: []; saved: []; started: [cardId: string] }>();
const client = useQueryClient();
const proposals = useStudyProposals();
const projects = useResearches();
const agents = useAgents();
const teams = useTeams('', '');
const me = useMe();
const workspace = workspaceId.value;
const saved = ref(props.proposal);
const id = saved.value?.id ?? crypto.randomUUID();
const operationId = crypto.randomUUID();
const description = ref(saved.value?.description ?? props.initialDescription ?? '');
const stage = ref<StudyProposalContent['stage']>(saved.value?.stage ?? props.initialStage ?? 'grounding');
function assignment(p?: StudyProposal) {
  if (!p) return '';
  return p.assignee.type === 'agent'
    ? 'agent:' + p.assignee.agent_id
    : 'team:' + p.assignee.pack_id;
}
const selected = ref(saved.value ? assignment(saved.value) : props.initialAssignee ?? '');
const project = ref(saved.value?.project_id ?? props.projectId ?? '');
const busy = ref(false);
const error = ref('');
let active = true;
onUnmounted(() => {
  active = false;
});
const editable = computed(() => !saved.value || me.data.value?.id === saved.value.owner_id);
const latest = computed(() => proposals.data.value?.find((p) => p.id === id));
const newer = computed(
  () => saved.value && latest.value && latest.value.revision > saved.value.revision,
);
const changed = computed(
  () =>
    !saved.value ||
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
function changeStage(event: Event) {
  stage.value = (event.target as HTMLSelectElement).value as StudyProposalContent['stage'];
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
async function act(action: 'save' | 'discard' | 'start') {
  if (!editable.value || busy.value || newer.value || workspace !== workspaceId.value) return;
  if (action === 'save' && !valid.value) return;
  if (action === 'start' && (!saved.value || changed.value)) return;
  if (action === 'discard' && (!saved.value || !confirm('Discard this proposal?'))) return;
  const path = '/api/study-proposals/' + id;
  const revision = saved.value?.revision ?? 0;
  busy.value = true;
  error.value = '';
  try {
    let card: string | undefined;
    if (action === 'save') {
      const [type, target] = selected.value.split(':');
      await api(
        'PUT',
        path,
        {
          revision,
          // This form doesn't edit these, and a save replaces the whole proposal.
          title: saved.value?.title,
          priority: saved.value?.priority,
          description: description.value,
          stage: stage.value,
          project_id: project.value || null,
          assignee: type === 'agent' ? { type, agent_id: target } : { type, pack_id: target },
        },
        undefined,
        { workspace },
      );
    } else if (action === 'discard') {
      await api('DELETE', path + '?revision=' + revision, undefined, undefined, { workspace });
    } else {
      const receipt = await api<{ card_id: string }>(
        'POST',
        path + '/start',
        { operation_id: operationId, revision },
        undefined,
        { workspace },
      );
      card = receipt.card_id;
    }
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.proposals(workspace) }),
      ...(card ? [client.invalidateQueries({ queryKey: keys.cards(workspace) })] : []),
    ]);
    if (active && workspace === workspaceId.value) {
      if (card) emit('started', card);
      else if (action === 'save') emit('saved');
      else emit('close');
    }
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) void proposals.refetch();
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <Modal title="Study proposal" :before-close="() => !busy" @close="emit('close')">
    <form class="proposal-form" @submit.prevent="act('save')">
      <h2>Study proposal</h2>
      <fieldset :disabled="!editable || busy">
        <label>Description<textarea v-model="description" required rows="5" autofocus /></label>
        <label
          >Stage<select :value="stage" @change="changeStage">
            <option v-for="s in stages" :key="s" :value="s">{{ columnName(s) }}</option>
          </select></label
        >
        <label
          >Assignee<select v-model="selected" required>
            <option disabled value="">Choose an agent or Team</option>
            <option
              v-if="selected && !options.some((o) => o.value === selected)"
              :value="selected"
              disabled
            >
              Unavailable {{ selected.startsWith('agent:') ? 'agent' : 'Team' }}
            </option>
            <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select></label
        >
        <label
          >Project<select v-model="project">
            <option value="">No project</option>
            <option v-for="p in projects.data.value" :key="p.id" :value="p.id">{{ p.name }}</option>
          </select></label
        >
      </fieldset>
      <p v-if="newer" role="alert">
        This proposal changed.
        <button type="button" class="text-button" @click="loadLatest">Load latest revision</button>
      </p>
      <p v-if="error" role="alert">{{ error }}</p>
      <footer>
        <button type="button" class="button compact" :disabled="busy" @click="emit('close')">
          Close
        </button>
        <template v-if="editable">
          <button
            v-if="saved"
            type="button"
            class="button compact"
            :disabled="busy || !!newer"
            @click="act('discard')"
          >
            Discard
          </button>
          <button
            v-if="saved"
            type="button"
            class="button compact"
            :disabled="busy || !!newer || changed || !valid"
            @click="act('start')"
          >
            Start study
          </button>
          <button class="button primary compact" :disabled="busy || !!newer || !changed || !valid">
            {{ saved ? 'Save changes' : 'Save to To-do' }}
          </button>
        </template>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.proposal-form {
  padding: 20px;
  display: grid;
  gap: 16px;
  width: min(460px, 85vw);
}
h2 {
  margin: 0;
  font-size: 17px;
}
fieldset {
  display: grid;
  gap: 12px;
  padding: 0;
  border: 0;
  min-width: 0;
}
label {
  display: grid;
  gap: 5px;
  font-size: 13px;
}
textarea,
select {
  width: 100%;
  padding: 8px;
  background: var(--input);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
}
textarea {
  resize: vertical;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
}
</style>
