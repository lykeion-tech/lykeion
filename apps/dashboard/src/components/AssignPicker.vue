<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, toRaw } from 'vue';
import Icon from './Icon.vue';
import { useAgents, useMe, useTeams } from '../api/queries';
import { matchesQuery } from '../lib/search';
import { stages, type Picked, type Range } from '../lib/studies';

// The Assign and Stages pills of a study or an Autoresearch. A study is worked
// by a Team or a Team set, and the built-in Teams work every stage nobody
// picked a Team for. An Autoresearch (agents) may instead name an agent.
// Teams are the built-in ones and the ones the researcher has added.
const props = defineProps<{
  agents?: boolean;
  lastSet?: Picked | null;
  menuBelow?: boolean;
}>();
const assignee = defineModel<Picked | null>({ required: true });
const range = defineModel<Range>('range', { required: true });
const emit = defineEmits<{ open: []; picked: [last: boolean] }>();

const agentList = useAgents();
const me = useMe();
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
  (store.data.value ?? []).filter((t) => t.added || t.source === 'shipped'),
);
const stageName = (s: string) => (s ? s[0]!.toUpperCase() + s.slice(1) : '');
// The owner of the study or Autoresearch is the researcher, so a private agent
// is offered only to its owner.
const assignableAgents = computed(() =>
  props.agents
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
  if (!a) return props.agents ? '' : 'Built-in Teams';
  if (!('teams' in a)) return agentList.data.value?.find((x) => x.id === a.id)?.name ?? 'Agent';
  if (isSet.value) return 'Your Team set';
  return addedTeams.value.find((t) => t.id === a.teams[teamColumns.value[0]!])?.name ?? 'Team';
});
// A Team with no stage label has no column until the researcher picks one.
const unstaged = computed(() => teamColumns.value.includes(''));
defineExpose({ name, unstaged, teamColumns, set });

const matchingTeams = computed(() =>
  addedTeams.value.filter((t) => matchesQuery(t.name, query.value)),
);
const matchingAgents = computed(() =>
  assignableAgents.value.filter((a) => matchesQuery(a.name, query.value)),
);
const showSets = computed(() => matchesQuery('team sets new set your last set', query.value));
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
  if (!root.value?.contains(event.target as Node)) closeMenu();
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
        :aria-label="name ? `Assignee: ${name}` : 'Unassigned'"
        @click="openMenu('assignee')"
      >
        <span v-if="name" class="avatar">{{ name[0] }}</span
        ><span>{{ name || 'Unassigned' }}</span>
      </button>
      <div
        v-if="menu === 'assignee'"
        :class="['composer-menu', 'stage-menu', { below: menuBelow }]"
        role="menu"
      >
        <input v-model="query" placeholder="Assign to…" aria-label="Search assignees" />
        <button v-if="!agents" type="button" role="menuitem" @click="assign(null)">
          <Icon name="bot" :size="14" /><span>Built-in Teams</span>
        </button>
        <p v-if="matchingAgents.length" class="menu-group">Agents</p>
        <button
          v-for="a in matchingAgents"
          :key="a.id"
          type="button"
          role="menuitem"
          @click="assign({ type: 'agent', id: a.id })"
        >
          <Icon name="bot" :size="14" /><span>{{ a.name }}</span>
        </button>
        <p v-if="matchingTeams.length" class="menu-group">Teams</p>
        <button
          v-for="t in matchingTeams"
          :key="t.id"
          type="button"
          role="menuitem"
          @click="assign({ type: 'team', teams: { [t.stage]: t.id } })"
        >
          <Icon name="bot" :size="14" /><span>{{ t.name }}</span
          ><small v-if="t.stage">Built for {{ stageName(t.stage) }}</small
          ><small v-else>No stage label</small>
        </button>
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
          <select v-model="setSlots[s]" :aria-label="`${stageName(s)} Team`">
            <option value="">{{ agents ? 'No Team' : 'Built-in Team' }}</option>
            <option v-for="t in addedTeams" :key="t.id" :value="t.id">{{ t.name }}</option>
          </select>
        </label>
        <label v-if="gaps"
          >Agent for the other stages
          <select v-model="setAgent" aria-label="Agent for the other stages">
            <option value="">Choose an agent</option>
            <option v-for="a in assignableAgents" :key="a.id" :value="a.id">{{ a.name }}</option>
          </select>
        </label>
        <button type="button" class="button primary compact" :disabled="!setReady" @click="useSet">
          Use this set
        </button>
      </div>
    </div>
    <div v-if="assignee || !agents" class="property-anchor">
      <button
        type="button"
        class="property-pill"
        :aria-label="
          unstaged
            ? 'Stages: choose a stage'
            : `Stages: ${stageName(range.from)}${range.from === range.to ? '' : ' → ' + stageName(range.to)}`
        "
        :disabled="assignee?.type === 'team' && !unstaged"
        @click="openMenu('stages')"
      >
        <Icon name="circle" :size="14" />{{
          unstaged
            ? 'Choose a stage'
            : stageName(range.from) + (range.from === range.to ? '' : ' → ' + stageName(range.to))
        }}
      </button>
      <div
        v-if="menu === 'stages' && unstaged"
        :class="['composer-menu', 'stage-menu', 'set-menu', { below: menuBelow }]"
        role="menu"
      >
        <label
          >Stage
          <select
            aria-label="Stage"
            value=""
            @change="pickColumn(($event.target as HTMLSelectElement).value)"
          >
            <option value="" disabled>Choose a stage</option>
            <option v-for="s in stages" :key="s" :value="s">{{ stageName(s) }}</option>
          </select>
        </label>
      </div>
      <div
        v-else-if="menu === 'stages'"
        :class="['composer-menu', 'stage-menu', 'set-menu', { below: menuBelow }]"
        role="menu"
      >
        <label
          >From
          <select
            :value="range.from"
            aria-label="From stage"
            @change="pickStage('from', ($event.target as HTMLSelectElement).value)"
          >
            <option v-for="s in stages" :key="s" :value="s">{{ stageName(s) }}</option>
          </select>
        </label>
        <label
          >To
          <select
            :value="range.to"
            aria-label="To stage"
            @change="pickStage('to', ($event.target as HTMLSelectElement).value)"
          >
            <option v-for="s in stages" :key="s" :value="s">{{ stageName(s) }}</option>
          </select>
        </label>
      </div>
    </div>
  </div>
</template>
<style scoped src="./composer-menu.css"></style>
<style scoped>
.assign-picker {
  display: contents;
}
.stage-menu.below {
  top: calc(100% + 6px);
  bottom: auto;
}
</style>
