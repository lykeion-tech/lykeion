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
const ownedAgents = computed(() =>
  props.agents.filter((item) => item.owner_id === props.owner?.user_id),
);
const activeRuntime = computed(() =>
  props.runtimes.find((item) => item.id === active.value?.runtime_id),
);
// The machine the agent ran on is up and healthy.
const available = computed(
  () => activeRuntime.value?.status === 'online' && !activeRuntime.value.unhealthy_reason,
);
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
      <div class="participant-profile">
        <span class="profile-avatar avatar"
          ><AvatarDisplay
            v-if="owner.avatar_url"
            :value="
              owner.avatar_url.startsWith('emoji:') ? owner.avatar_url.slice(6) : owner.avatar_url
            "
            :size="30"
          /><template v-else>{{ (owner.name || owner.email)[0] }}</template></span
        >
        <div class="profile-identity">
          <strong>{{ owner.name || owner.email }}</strong>
          <span class="role-badge">{{ owner.role }}</span>
          <div class="muted">{{ owner.email }}</div>
        </div>
        <RouterLink class="details-link" :to="`/colleagues/${owner.user_id}`">Details →</RouterLink>
      </div>
      <div class="owned-agents" v-if="ownedAgents.length">
        <div class="muted">Agents ({{ ownedAgents.length }})</div>
        <RouterLink
          v-for="item in ownedAgents"
          :key="item.id"
          :to="`/agents/${item.id}`"
          class="owned-agent"
        >
          <span class="owned-avatar avatar"
            ><AvatarDisplay
              v-if="item.avatar_url"
              :value="
                item.avatar_url.startsWith('emoji:') ? item.avatar_url.slice(6) : item.avatar_url
              "
              :size="18"
            /><template v-else>{{ agentDisplayName(item.name)[0] }}</template></span
          >
          <span
            ><strong>{{ agentDisplayName(item.name) }}</strong
            ><small v-if="item.description">{{ item.description }}</small></span
          >
        </RouterLink>
      </div>
    </section>
    <section
      v-else-if="active"
      class="participant-details"
      :aria-label="`${label(active)} details`"
    >
      <div class="participant-profile">
        <span class="profile-avatar avatar"
          ><AvatarDisplay
            v-if="agent?.avatar_url"
            :value="
              agent.avatar_url.startsWith('emoji:') ? agent.avatar_url.slice(6) : agent.avatar_url
            "
            :size="30"
          /><template v-else>{{ label(active)[0] }}</template></span
        >
        <div class="profile-identity">
          <strong>{{ label(active) }}</strong>
          <div class="muted participant-status">
            <span class="status-dot" :class="{ inactive: !available }" aria-hidden="true" />{{
              available ? 'Active' : 'Inactive'
            }}
          </div>
        </div>
        <RouterLink v-if="agent" :to="`/agents/${active.agent_id}`" class="details-link"
          >Details →</RouterLink
        >
      </div>
      <p v-if="agent?.description" class="participant-description">{{ agent.description }}</p>
      <dl>
        <div>
          <dt>Runtime</dt>
          <dd>{{ runtime(active) }}</dd>
        </div>
        <div>
          <dt>Model</dt>
          <dd>{{ active.model || 'Default' }}</dd>
        </div>
        <div v-if="owner && agent?.owner_id === owner.user_id">
          <dt>Owner</dt>
          <dd>{{ owner.name || owner.email }}</dd>
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
      <div v-if="agent" class="participant-actions">
        <RouterLink :to="`/agents/${active.agent_id}`">Settings</RouterLink>
      </div>
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
  width: min(320px, calc(100vw - 32px));
  max-height: min(360px, 50vh);
  overflow-y: auto;
  box-sizing: border-box;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  box-shadow: 0 8px 24px #0002;
  font-size: 13px;
  font-weight: 400;
}
.participant-details strong {
  font-weight: 400;
}
.participant-status {
  display: flex;
  align-items: center;
  gap: 6px;
}
.status-dot {
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 50%;
  background: #39a35a;
}
.status-dot.inactive {
  background: var(--error);
}
.participant-profile {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}
.profile-avatar {
  width: 42px;
  height: 42px;
  flex: none;
  font-size: 16px;
}
.profile-identity {
  min-width: 0;
  flex: 1;
  overflow-wrap: anywhere;
}
.muted {
  color: var(--muted);
}
.role-badge {
  background: var(--fill-control);
  border-radius: 4px;
  padding: 2px 5px;
  font-size: 11px;
  text-transform: capitalize;
}
.details-link {
  color: var(--primary-text);
  white-space: nowrap;
}
.participant-description {
  color: var(--muted);
  margin: 14px 0 0;
}
.owned-agents {
  margin-top: 14px;
}
.owned-agent {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 8px;
}
.owned-agent > span:last-child {
  min-width: 0;
}
.owned-agent small {
  display: block;
  color: var(--muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.owned-avatar {
  width: 24px;
  height: 24px;
  flex: none;
}
.participant-actions {
  border-top: 1px solid var(--border);
  margin-top: 14px;
  padding-top: 12px;
}
.participant-actions a {
  display: inline-block;
  padding: 5px 10px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--fill-control);
}
dl {
  margin: 10px 0 0;
}
dl div {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
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
