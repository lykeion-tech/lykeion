<script setup lang="ts">
import TeamName from '../features/teams/TeamName.vue';
import CompactSelect from './CompactSelect.vue';
import { teamDisplayParts } from '../lib/team-name';
import { computed, nextTick, onMounted, onUnmounted, ref, toRaw } from 'vue';
import Icon from './Icon.vue';
import AvatarDisplay from './AvatarDisplay.vue';
import Aristotle from '../features/coresearch/Aristotle.vue';
import CliIcon from '../features/coresearch/CliIcon.vue';
import { backendLabel } from '../lib/backends';
import { useAgents, useMe, useRuntimes, useTeams } from '../api/queries';
import type { StoreTeam } from '../api/types';
import { matchesQuery } from '../lib/search';
import { stages, type Picked, type Range } from '../lib/studies';

// The Assign and Stages pills of a study or an Autoresearch. A study is worked
// by a Team or a Team set, and the built-in Teams work every stage nobody
// picked a Team for; with oneAgent it may instead be worked by one agent alone.
// An Autoresearch (agents) may name an agent, alone or beside its Teams.
// Teams are the built-in ones and the ones the researcher has added.
const props = defineProps<{
  agents?: boolean;
  oneAgent?: boolean;
  // A Workflow study is worked by one agent over a fixed stage.
  agentOnly?: boolean;
  lastSet?: Picked | null;
  menuBelow?: boolean;
  buttonLabel?: string;
  defaultLabel?: string;
}>();
const assignee = defineModel<Picked | null>({ required: true });
const range = defineModel<Range>('range', { required: true });
const emit = defineEmits<{ open: []; picked: [last: boolean] }>();

const agentList = useAgents();
const me = useMe();
const runtimes = useRuntimes();
const store = useTeams('', '');
const root = ref<HTMLElement>();
const menu = ref('');
const query = ref('');
const setSlots = ref<Record<string, string>>(Object.fromEntries(stages.map((s) => [s, ''])));
const setAgent = ref('');
// The Team set last picked here; while it is the assignee the study runs end
// to end, even with one Team in it.
const setPick = ref<Picked | null>(null);
const set = computed(() => !!assignee.value && toRaw(assignee.value) === toRaw(setPick.value));

const addedTeams = computed(() =>
  props.agentOnly ? [] : (store.data.value ?? []).filter((t) => t.added || t.source === 'shipped'),
);
const stageName = (s: string) => (s ? s[0]!.toUpperCase() + s.slice(1) : '');
// The owner of the study or Autoresearch is the researcher, so a private agent
// is offered only to its owner.
const assignableAgents = computed(() =>
  props.agents || props.oneAgent
    ? (agentList.data.value ?? []).filter(
        (a) => !a.archived_at && (a.visibility === 'workspace' || a.owner_id === me.data.value?.id),
      )
    : [],
);
const teamColumns = computed(() =>
  assignee.value?.type === 'team' ? Object.keys(assignee.value.teams) : [],
);
const isSet = computed(
  () => assignee.value?.type === 'team' && (set.value || range.value.from !== range.value.to),
);
// An Autoresearch must name its assignee; a study left alone gets built-in Teams.
const name = computed(() => {
  const a = assignee.value;
  if (!a) return props.agents ? '' : (props.defaultLabel ?? 'Built-in Teams');
  if (!('teams' in a)) return agentList.data.value?.find((x) => x.id === a.id)?.name ?? 'Agent';
  if (isSet.value) return 'Your Team set';
  return teamDisplayParts(
    addedTeams.value.find((t) => t.id === a.teams[teamColumns.value[0]!])?.name ?? 'Team',
  ).label;
});
const selectedAvatar = computed(() => {
  const a = assignee.value;
  if (!a) return '';
  if (a.type === 'agent')
    return agentList.data.value?.find((item) => item.id === a.id)?.avatar_url ?? '';
  if (isSet.value) return '';
  return (
    addedTeams.value.find((item) => item.id === a.teams[teamColumns.value[0]!])?.avatar_url ?? ''
  );
});
const selectedAgent = computed(() => {
  const a = assignee.value;
  return a?.type === 'agent' ? agentList.data.value?.find((item) => item.id === a.id) : undefined;
});
function cliOnline(harness: string | undefined) {
  return (
    !!harness &&
    !!me.data.value?.id &&
    (runtimes.data.value ?? []).some(
      (runtime) =>
        runtime.owner_id === me.data.value?.id &&
        runtime.harness === harness &&
        runtime.status === 'online' &&
        !runtime.unhealthy_reason,
    )
  );
}
function teamOnline(team: StoreTeam | undefined) {
  return !!team?.agent_clis?.length && team.agent_clis.every(cliOnline);
}
const selectedOnline = computed(() => {
  const a = assignee.value;
  if (a?.type === 'agent') return cliOnline(selectedAgent.value?.harness);
  if (a?.type === 'team') {
    const teams = Object.values(a.teams).map((id) =>
      addedTeams.value.find((team) => team.id === id),
    );
    const agentReady =
      !a.agent_id ||
      cliOnline(agentList.data.value?.find((agent) => agent.id === a.agent_id)?.harness);
    return teams.length > 0 && agentReady && teams.every(teamOnline);
  }
  if (!a && !props.agents)
    return stages.every((stage) =>
      teamOnline(
        addedTeams.value.find((team) => team.source === 'shipped' && team.stage === stage),
      ),
    );
  return false;
});
// A Team with no stage label has no column until the researcher picks one.
const unstaged = computed(() => teamColumns.value.includes(''));
defineExpose({ name, unstaged, teamColumns, set });

const matchingTeams = computed(() =>
  addedTeams.value.filter((t) => matchesQuery(t.name, query.value)),
);
const matchingBuiltInTeams = computed(() =>
  matchingTeams.value.filter((team) => team.source === 'shipped'),
);
const matchingCustomTeams = computed(() =>
  matchingTeams.value.filter((team) => team.source !== 'shipped'),
);
const matchingAgents = computed(() =>
  assignableAgents.value.filter((a) => matchesQuery(a.name, query.value)),
);
const showSets = computed(
  () => !props.agentOnly && matchesQuery('team sets new set your last set', query.value),
);
// A study's stages with no Team in the set get built-in Teams; an
// Autoresearch's get an agent.
const gaps = computed(() => !!props.agents && stages.some((s) => !setSlots.value[s]));
const setReady = computed(
  () => stages.some((s) => setSlots.value[s]) && (!gaps.value || !!setAgent.value),
);

async function openMenu(which: string) {
  const opening = menu.value !== which;
  menu.value = opening ? which : '';
  query.value = '';
  if (opening) emit('open');
  await nextTick();
  root.value?.querySelector<HTMLInputElement>('.composer-menu input')?.focus();
}
function closeMenu() {
  menu.value = '';
  query.value = '';
}
function assign(value: Picked | null, last = false, isSet = false) {
  assignee.value = value;
  setPick.value = isSet ? value : null;
  // One Team keeps its own stage; a set and any other assignee run end to end.
  const cols = value?.type === 'team' ? Object.keys(value.teams) : [];
  range.value =
    !isSet && cols.length === 1
      ? { from: cols[0]!, to: cols[0]! }
      : { from: 'grounding', to: 'validation' };
  emit('picked', last);
  closeMenu();
}
function pickStage(side: 'from' | 'to', value: string) {
  const next = { ...range.value, [side]: value };
  if (stages.indexOf(next.from as never) > stages.indexOf(next.to as never))
    range.value =
      side === 'from' ? { from: next.from, to: next.from } : { from: next.to, to: next.to };
  else range.value = next;
  if (side === 'to') closeMenu();
}
function useSet() {
  const teams = Object.fromEntries(
    stages.flatMap((s) => (setSlots.value[s] ? [[s, setSlots.value[s]]] : [])),
  );
  assign({ type: 'team', teams, ...(gaps.value ? { agent_id: setAgent.value } : {}) }, false, true);
}
function pickColumn(col: string) {
  const a = assignee.value;
  if (a?.type !== 'team' || !('teams' in a)) return;
  assignee.value = { ...a, teams: { [col]: a.teams[''] as string } };
  range.value = { from: col, to: col };
  closeMenu();
}
function outside(event: PointerEvent) {
  const target = event.target as HTMLElement;
  const picker = target.closest?.('.compact-select-menu');
  if (picker?.id && root.value?.querySelector(`[aria-controls="${CSS.escape(picker.id)}"]`)) return;
  if (!root.value?.contains(target)) closeMenu();
}
function keysDown(event: KeyboardEvent) {
  if (event.key === 'Escape' && menu.value) {
    event.preventDefault();
    event.stopPropagation();
    closeMenu();
  }
}
onMounted(() => document.addEventListener('pointerdown', outside));
onUnmounted(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <div ref="root" class="assign-picker" @keydown="keysDown">
    <div class="property-anchor">
      <button
        type="button"
        class="property-pill assignee-pill"
        :aria-label="
          !assignee && defaultLabel ? defaultLabel : name ? `Assignee: ${name}` : 'Unassigned'
        "
        @click="openMenu('assignee')"
      >
        <span v-if="assignee || !defaultLabel" class="availability-icon">
          <AvatarDisplay
            v-if="name && !buttonLabel && selectedAvatar"
            :value="selectedAvatar.startsWith('emoji:') ? selectedAvatar.slice(6) : selectedAvatar"
            :size="14"
          />
          <span
            v-else-if="selectedAgent?.built_in_key === 'aristotle' && !buttonLabel"
            class="picker-aristotle"
            ><Aristotle still
          /></span>
          <Icon
            v-else-if="name && !buttonLabel && assignee?.type === 'team'"
            name="network"
            :size="14"
          />
          <Icon
            v-else-if="name && !buttonLabel && selectedAgent?.source === 'built_in'"
            name="bot"
            :size="14"
          />
          <span v-else-if="name && !buttonLabel" class="avatar">{{ name[0] }}</span>
          <span
            v-if="selectedOnline"
            class="availability-dot"
            role="img"
            :aria-label="selectedAgent ? 'CLI online' : 'Team CLIs online'"
            :title="selectedAgent ? 'CLI online' : 'Team CLIs online'"
          ></span>
        </span>
        <span>{{ buttonLabel || name || 'Unassigned' }}</span>
      </button>
      <div
        v-if="menu === 'assignee'"
        :class="['composer-menu', 'stage-menu', 'assignee-menu', { below: menuBelow }]"
        role="menu"
      >
        <input v-model="query" placeholder="Assign to…" aria-label="Search assignees" />
        <p v-if="agents && matchingAgents.length" class="menu-group">Agents</p>
        <button
          v-for="a in agents ? matchingAgents : []"
          :key="a.id"
          type="button"
          role="menuitem"
          @click="assign({ type: 'agent', id: a.id })"
        >
          <span class="availability-icon"
            ><AvatarDisplay v-if="a.avatar_url" :value="a.avatar_url.slice(6)" :size="14" />
            <span v-else-if="a.built_in_key === 'aristotle'" class="picker-aristotle"
              ><Aristotle still
            /></span>
            <Icon v-else-if="a.source === 'built_in' || a.built_in_key" name="bot" :size="14" />
            <span v-else class="avatar" aria-hidden="true">{{ a.name[0] }}</span
            ><span
              v-if="cliOnline(a.harness)"
              class="availability-dot"
              role="img"
              aria-label="CLI online"
              title="CLI online"
            ></span></span
          ><span>{{ a.name }}</span
          ><span
            v-if="a.harness === 'claude' || a.harness === 'codex'"
            class="picker-cli"
            role="img"
            :aria-label="backendLabel(a.harness)"
            :title="backendLabel(a.harness)"
            ><CliIcon :cli="a.harness"
          /></span>
        </button>
        <template
          v-for="group in [
            { label: 'Built-in Teams', teams: matchingBuiltInTeams },
            { label: 'Custom Teams', teams: matchingCustomTeams },
          ]"
          :key="group.label"
        >
          <p v-if="group.teams.length" class="menu-group">{{ group.label }}</p>
          <button
            v-for="t in group.teams"
            :key="t.id"
            type="button"
            role="menuitem"
            @click="assign({ type: 'team', teams: { [t.stage]: t.id } })"
          >
            <span class="availability-icon"
              ><AvatarDisplay v-if="t.avatar_url" :value="t.avatar_url.slice(6)" :size="14" /><Icon
                v-else
                name="network"
                :size="14" /><span
                v-if="teamOnline(t)"
                class="availability-dot"
                role="img"
                aria-label="Team CLIs online"
                title="Team CLIs online"
              ></span></span
            ><span><TeamName :name="t.name" /></span
            ><span
              v-if="t.leader_cli"
              class="picker-cli"
              role="img"
              :aria-label="backendLabel(t.leader_cli)"
              :title="`Team leader: ${backendLabel(t.leader_cli)}`"
              ><CliIcon :cli="t.leader_cli" /></span
            ><small v-if="group.label !== 'Built-in Teams' && t.stage"
              >Built for {{ stageName(t.stage) }}</small
            ><small v-else-if="group.label !== 'Built-in Teams'">No stage label</small>
          </button>
        </template>
        <p v-if="showSets" class="menu-group">Team sets</p>
        <button
          v-if="showSets && lastSet"
          type="button"
          role="menuitem"
          @click="assign(lastSet, true, true)"
        >
          <Icon name="bot" :size="14" /><span>Your last set</span
          ><small>{{ Object.keys((lastSet as { teams: object }).teams).length }} Teams</small>
        </button>
        <button v-if="showSets" type="button" role="menuitem" @click="menu = 'set'">
          <Icon name="plus" :size="14" /><span>New set…</span>
        </button>
        <template v-if="oneAgent">
          <p v-if="matchingAgents.length" class="menu-group">Agents</p>
          <button
            v-for="a in matchingAgents"
            :key="a.id"
            type="button"
            role="menuitem"
            @click="assign({ type: 'agent', id: a.id })"
          >
            <span class="availability-icon"
              ><AvatarDisplay v-if="a.avatar_url" :value="a.avatar_url.slice(6)" :size="14" />
              <span v-else-if="a.built_in_key === 'aristotle'" class="picker-aristotle"
                ><Aristotle still
              /></span>
              <Icon v-else-if="a.source === 'built_in' || a.built_in_key" name="bot" :size="14" />
              <span v-else class="avatar" aria-hidden="true">{{ a.name[0] }}</span
              ><span
                v-if="cliOnline(a.harness)"
                class="availability-dot"
                role="img"
                aria-label="CLI online"
                title="CLI online"
              ></span></span
            ><span>{{ a.name }}</span
            ><span
              v-if="a.harness === 'claude' || a.harness === 'codex'"
              class="picker-cli"
              role="img"
              :aria-label="backendLabel(a.harness)"
              :title="backendLabel(a.harness)"
              ><CliIcon :cli="a.harness"
            /></span>
          </button>
        </template>
        <p v-if="!matchingAgents.length && !matchingTeams.length">No matches</p>
      </div>
      <div
        v-if="menu === 'set'"
        :class="['composer-menu', 'stage-menu', 'set-menu', { below: menuBelow }]"
        role="dialog"
        aria-label="New Team set"
      >
        <label v-for="s in stages" :key="s"
          >{{ stageName(s) }}
          <CompactSelect
            v-model="setSlots[s]"
            :label="`${stageName(s)} Team`"
            :width="240"
            :options="[
              { value: '', label: agents ? 'No Team' : 'Built-in Team' },
              ...addedTeams.map((team) => ({
                value: team.id,
                label: teamDisplayParts(team.name).label,
              })),
            ]"
          />
        </label>
        <label v-if="gaps"
          >Agent for the other stages
          <CompactSelect
            v-model="setAgent"
            label="Agent for the other stages"
            :width="240"
            :options="[
              { value: '', label: 'Choose an agent' },
              ...assignableAgents.map((agent) => ({ value: agent.id, label: agent.name })),
            ]"
          />
        </label>
        <button type="button" class="button primary compact" :disabled="!setReady" @click="useSet">
          Use this set
        </button>
      </div>
    </div>
    <div v-if="assignee || !agents" class="property-anchor">
      <button
        type="button"
        class="property-pill stages-pill"
        :aria-label="
          unstaged
            ? 'Stages: choose a stage'
            : `Stages: ${stageName(range.from)}${range.from === range.to ? '' : ' → ' + stageName(range.to)}`
        "
        :disabled="agentOnly || (assignee?.type === 'team' && !unstaged)"
        @click="openMenu('stages')"
      >
        <Icon name="circle" :size="14" />
        <span
          >Stages:
          {{
            unstaged
              ? 'Choose a stage'
              : stageName(range.from) + (range.from === range.to ? '' : ' → ' + stageName(range.to))
          }}</span
        >
      </button>
      <div
        v-if="menu === 'stages' && unstaged"
        :class="['composer-menu', 'stage-menu', 'set-menu', { below: menuBelow }]"
        role="menu"
      >
        <label
          >Stage
          <CompactSelect
            model-value=""
            label="Stage"
            :width="200"
            :options="[
              { value: '', label: 'Choose a stage', disabled: true },
              ...stages.map((stage) => ({ value: stage, label: stageName(stage) })),
            ]"
            @update:model-value="pickColumn"
          />
        </label>
      </div>
      <div
        v-else-if="menu === 'stages'"
        :class="['composer-menu', 'stage-menu', 'set-menu', { below: menuBelow }]"
        role="menu"
      >
        <label
          >From
          <CompactSelect
            :model-value="range.from"
            label="From stage"
            :width="200"
            :options="stages.map((stage) => ({ value: stage, label: stageName(stage) }))"
            @update:model-value="pickStage('from', $event)"
          />
        </label>
        <label
          >To
          <CompactSelect
            :model-value="range.to"
            label="To stage"
            :width="200"
            :options="stages.map((stage) => ({ value: stage, label: stageName(stage) }))"
            @update:model-value="pickStage('to', $event)"
          />
        </label>
      </div>
    </div>
  </div>
</template>
<style scoped src="./composer-menu.css"></style>
<style scoped>
.stage-menu.assignee-menu {
  width: 300px;
  max-width: min(340px, calc(100vw - 32px));
}
.assign-picker {
  display: contents;
}
.stages-pill > .icon {
  flex: none;
}
.stages-pill > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.picker-aristotle {
  width: 14px;
  height: 14px;
  flex: none;
  overflow: hidden;
}
.picker-cli {
  flex: none;
  display: inline-flex;
  align-items: center;
  font-size: 14px;
  color: var(--muted);
}
.availability-icon {
  position: relative;
  display: inline-flex;
  flex: none;
  align-items: center;
}
.assignee-pill > .availability-icon {
  overflow: visible;
}
.availability-dot {
  position: absolute;
  right: -4px;
  bottom: -1px;
  display: inline-block;
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #39a35a;
}
.picker-aristotle :deep(.aristotle-art) {
  width: 14px;
  height: 14px;
}
.stage-menu.below {
  top: calc(100% + 6px);
  bottom: auto;
}
</style>
