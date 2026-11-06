<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { RouterLink } from 'vue-router';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import type { AgentDefinition, AgentRun, Member, Runtime } from '../../api/types';
import AvatarDisplay from '../../ui/AvatarDisplay.vue';
import { agentDisplayName } from '../../lib/agent-name';

type Capabilities = {
  skills: string[] | null;
  teams: { id: string; skills: string[] | null }[];
  team_member: boolean;
};
const props = defineProps<{
  runs: AgentRun[];
  agents: AgentDefinition[];
  runtimes: Runtime[];
  teams: Record<string, { pack_id: string }>;
  owner?: Member;
}>();
const selected = ref<string | null>(null);
const participantKey = (run: AgentRun) => run.agent_id || `legacy:${run.agent_name || run.role}`;
const participants = computed(() => {
  const byId = new Map<string, AgentRun>();
  for (const run of props.runs) byId.set(participantKey(run), run);
  return [...byId.values()];
});
const active = computed(() =>
  participants.value.find((run) => participantKey(run) === selected.value),
);
const agent = computed(() => props.agents.find((item) => item.id === active.value?.agent_id));
const capabilities = useQuery({
  queryKey: computed(() => ['ws', workspaceId.value, 'agent-capabilities', selected.value]),
  queryFn: () =>
    api<Capabilities>('GET', `/api/agents/${encodeURIComponent(selected.value!)}/customize`),
  enabled: computed(() => !!workspaceId.value && !!selected.value && !!agent.value),
  retry: false,
});
const skills = computed(() => {
  const data = capabilities.data.value;
  if (!data || !active.value) return null;
  if (!data.team_member) return data.skills ?? [];
  const team = props.teams?.[active.value.board_column]?.pack_id;
  return data.teams.find((item) => item.id === team)?.skills ?? null;
});
function label(run: AgentRun) {
  return agentDisplayName(run.agent_name || run.role);
}
function runtime(run: AgentRun) {
  const machine = props.runtimes.find((item) => item.id === run.runtime_id);
  return machine
    ? `${run.harness} · ${machine.custom_name || machine.device_name}`
    : run.harness || 'Unavailable';
}
</script>
<template>
  <div v-if="participants.length || owner" class="participants" @keydown.esc="selected = null">
    <div class="participant-icons" aria-label="Study participants">
      <button
        v-if="owner"
        type="button"
        class="participant-icon avatar"
        :aria-label="`Show ${owner.name || owner.email} details`"
        :aria-expanded="selected === 'owner'"
        @click="selected = selected === 'owner' ? null : 'owner'"
      >
        <AvatarDisplay
          v-if="owner.avatar_url"
          :value="
            owner.avatar_url.startsWith('emoji:') ? owner.avatar_url.slice(6) : owner.avatar_url
          "
          :size="18"
        />
        <template v-else>{{ (owner.name || owner.email)[0] }}</template>
      </button>
      <button
        v-for="run in participants"
        :key="participantKey(run)"
        type="button"
        class="participant-icon avatar"
        :aria-label="`Show ${label(run)} details`"
        :aria-expanded="selected === participantKey(run)"
        @click="selected = selected === participantKey(run) ? null : participantKey(run)"
      >
        <AvatarDisplay
          v-if="agents.find((a) => a.id === run.agent_id)?.avatar_url"
          :value="agents.find((a) => a.id === run.agent_id)!.avatar_url.slice(6)"
          :size="18"
        />
        <template v-else>{{ label(run)[0] }}</template>
      </button>
    </div>
    <section
      v-if="selected === 'owner' && owner"
      class="participant-details"
      :aria-label="`${owner.name || owner.email} details`"
    >
      <strong>{{ owner.name || owner.email }}</strong>
      <dl>
        <div>
          <dt>Email</dt>
          <dd>{{ owner.email }}</dd>
        </div>
        <div>
          <dt>Role</dt>
          <dd>{{ owner.role }}</dd>
        </div>
      </dl>
    </section>
    <section
      v-else-if="active"
      class="participant-details"
      :aria-label="`${label(active)} details`"
    >
      <RouterLink v-if="agent" :to="`/agents/${active.agent_id}`" class="participant-name">{{
        label(active)
      }}</RouterLink>
      <strong v-else>{{ label(active) }}</strong>
      <dl>
        <div>
          <dt>Runtime</dt>
          <dd>{{ runtime(active) }}</dd>
        </div>
        <div>
          <dt>Model</dt>
          <dd>{{ active.model || 'Default' }}</dd>
        </div>
        <div>
          <dt>Skills (current)</dt>
          <dd>
            {{
              capabilities.isPending.value && agent
                ? 'Loading…'
                : capabilities.isError.value || !agent || skills === null
                  ? 'Unavailable'
                  : skills.length
                    ? skills.join(', ')
                    : 'None'
            }}
          </dd>
        </div>
      </dl>
    </section>
  </div>
</template>
<style scoped>
.participants {
  position: relative;
  margin-left: auto;
}
.participant-icons {
  display: flex;
  align-items: center;
}
.participant-icon {
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid var(--surface);
  cursor: pointer;
}
.participant-icon + .participant-icon {
  margin-left: -5px;
}
.participant-details {
  position: absolute;
  z-index: 10;
  right: 0;
  top: calc(100% + 6px);
  width: 240px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: 0 8px 24px #0002;
  font-size: 13px;
}
.participant-name {
  font-weight: 600;
  color: var(--text);
}
dl {
  margin: 10px 0 0;
}
dl div {
  display: grid;
  grid-template-columns: 95px 1fr;
  gap: 8px;
  margin-top: 8px;
}
dt {
  color: var(--muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
</style>
