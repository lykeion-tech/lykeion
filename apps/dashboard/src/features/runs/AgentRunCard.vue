<script setup lang="ts">
import { computed, ref } from 'vue';
import type { AgentRun, Runtime } from '../../api/types';
import { useCard, useMembers } from '../../api/queries';
import { backendLabel } from '../../lib/backends';
import RunTurn from './RunTurn.vue';
import { columnName, roleLabel, systemRun, timeAgo } from '../../lib/studies';
import { agentDisplayName, agentNameWithoutCli } from '../../lib/agent-name';

const props = defineProps<{
  run: AgentRun;
  study: string;
  position: number;
  runtime?: Runtime | null;
  studyId: string;
}>();
const emit = defineEmits<{ notebook: [run: string] }>();
const active = computed(() => ['dispatched', 'running'].includes(props.run.status));
const open = ref(active.value);
const members = useMembers();
const studyCard = useCard(() => props.studyId);
// Only what the run and its machine's daemon recorded; nothing is filled in.
const environment = computed(() => {
  const r = props.run;
  const machine = props.runtime;
  const ownerId = r.machine_owner_id || machine?.owner_id;
  const owner = members.data.value?.find((m) => m.user_id === ownerId);
  const ownerName = owner?.name || owner?.email || (ownerId ? 'Former member' : '');
  const machineName = machine ? machine.custom_name || machine.device_name : r.machine_name;
  const machineLabel = machineName ? `${machineName}${ownerName ? ` (${ownerName})` : ''}` : '';
  return [
    { key: 'Machine', value: machineLabel },
    { key: 'Runtime', value: backendLabel(r.harness) },
    { key: 'Model', value: r.model },
    { key: 'Language', value: machine?.language ?? '' },
    {
      key: 'Platform',
      value: machine?.os && machine.arch ? `${machine.os} / ${machine.arch}` : (machine?.os ?? ''),
    },
    { key: 'Kernel', value: machine?.kernel ?? '' },
    { key: 'Stage', value: columnName(r.board_column) },
  ].filter((e) => e.value);
});
const name = computed(() => agentDisplayName(props.run.agent_name || roleLabel(props.run.role)));
const state = computed(
  () =>
    ({
      queued: 'Queued',
      dispatched: 'Working',
      running: 'Working',
      completed: 'Done',
      failed: 'Failed',
      cancelled: 'Cancelled',
    })[props.run.status],
);
// The run's hand-in answer, else the latest thing the agent wrote, as plain
// text for a three-line preview. The runs listing carries both, so a card
// never reads the run's messages.
const preview = computed(() => {
  const text = props.run.answer || props.run.latest_text;
  if (text)
    return text
      .replace(/```[\s\S]*?(```|$)/g, ' ')
      .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/[#>*_`|]+/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
  if (props.run.status === 'queued')
    return props.run.queue_reason ? `Queued: ${props.run.queue_reason.replace(/_/g, ' ')}` : '';
  // The objector and the rerun are Lykeion's own runs: their failure is
  // told plainly, never as the harness's raw error.
  if (systemRun(props.run.role))
    return props.run.status === 'failed'
      ? `Did not run${props.run.failure_reason ? ` (${props.run.failure_reason.replace(/_/g, ' ')})` : ''}`
      : '';
  return props.run.error;
});
const lastActive = computed(
  () => props.run.last_activity_at ?? props.run.started_at ?? props.run.created_at,
);
</script>
<template>
  <article class="agent-card" :data-status="run.status">
    <button
      class="run-header"
      :aria-label="`${name} (${state}), Run ${position}`"
      :aria-expanded="open"
      :aria-controls="`run-transcript-${run.id}`"
      @click="open = !open"
    >
      <span class="fold-caret" aria-hidden="true">{{ open ? '⌄' : '›' }}</span>
      <span class="avatar agent">
        {{ name[0] }}
        <span v-if="active" class="live-dot" :title="state" />
      </span>
      <span class="run-identity">
        <span class="agent-name">{{
          agentNameWithoutCli(run.agent_name || roleLabel(run.role), run.harness)
        }}</span>
        <span class="run-meta"
          ><template v-if="run.model">{{ run.model }} · </template
          ><time :datetime="lastActive">{{ timeAgo(lastActive) }}</time></span
        >
      </span>
    </button>
    <template v-if="!open">
      <p class="preview">{{ preview }}</p>
      <button class="show-transcript" @click="open = true">▸ show transcript</button>
    </template>
    <div v-else :id="`run-transcript-${run.id}`" class="open-transcript">
      <RunTurn :run="run" />
      <p class="run-environment" aria-label="Run environment">
        <span v-for="e in environment" :key="e.key"
          ><span class="environment-label">{{ e.key }}:</span> {{ e.value }}</span
        >
      </p>
      <p v-if="studyCard.data.value?.machine_label" class="study-machine">
        Runs on: {{ studyCard.data.value.machine_label }}
      </p>
      <p v-if="studyCard.data.value?.machine_notice" class="machine-notice" role="status">
        {{ studyCard.data.value.machine_notice }}
      </p>
      <button class="text-button notebook-link" @click="emit('notebook', run.id)">
        Open in Notebook
      </button>
    </div>
  </article>
</template>
<style scoped>
.agent-card {
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid var(--card-border);
  border-radius: 8px;
  box-shadow: 0 1px 2px #00000014;
  background: #ffffff16;
  font-size: 14px;
  line-height: 19px;
}
.run-header {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-width: 0;
  text-align: left;
  border-radius: 4px;
}
.run-header:focus-visible,
.show-transcript:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 4px;
}
.fold-caret {
  flex: none;
  width: 12px;
  color: var(--muted);
  font-size: 20px;
}
.avatar.agent {
  position: relative;
  flex: none;
  background: #fff3d6;
  color: #b5841b;
}
.live-dot {
  position: absolute;
  bottom: -1px;
  right: -1px;
  width: 8px;
  height: 8px;
  border: 2px solid var(--surface);
  border-radius: 50%;
  background: var(--primary);
}
.run-identity {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.agent-name {
  font-weight: 500;
}
.run-meta {
  color: var(--muted);
  font-size: 12px;
}
.preview {
  margin-top: 12px;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.show-transcript {
  margin-top: 10px;
  color: var(--muted);
  font-size: 12px;
}
.show-transcript:hover {
  color: var(--text);
}
.open-transcript {
  margin-top: 20px;
  min-width: 0;
}
.run-environment {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin-top: 18px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.environment-label {
  color: var(--text);
}
.study-machine,
.machine-notice {
  margin-top: 6px;
  color: var(--muted);
  font-size: 12px;
}
.notebook-link {
  margin-top: 12px;
  font-size: 12px;
}
</style>
