<script setup lang="ts">
import TeamName from '../components/TeamName.vue';
import { teamDisplayParts } from '../lib/team-name';
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import Icon from '../components/Icon.vue';
import { workspaceId } from '../api/session';
import { useTeams } from '../api/queries';
import { matchesQuery } from '../lib/search';

const search = ref('');
const newestFirst = ref(true);
const domain = ref('');
const stage = ref('');
const filterOpen = ref(false);
const filterAnchor = ref<HTMLElement>();
const filterTrigger = ref<HTMLButtonElement>();
function closeFilter(restoreFocus = false) {
  filterOpen.value = false;
  if (restoreFocus) filterTrigger.value?.focus();
}
async function toggleFilter() {
  if (filterOpen.value) return closeFilter(true);
  filterOpen.value = true;
  await nextTick();
  filterAnchor.value?.querySelector<HTMLSelectElement>('select')?.focus();
}
function outsideFilter(event: PointerEvent) {
  if (!filterAnchor.value?.contains(event.target as Node | null)) closeFilter();
}
function leaveFilter(event: FocusEvent) {
  if (!filterAnchor.value?.contains(event.relatedTarget as Node | null)) closeFilter();
}
onMounted(() => document.addEventListener('pointerdown', outsideFilter));
onUnmounted(() => document.removeEventListener('pointerdown', outsideFilter));
const stages = ['grounding', 'hypotheses', 'experimentation', 'validation'];
const everything = useTeams('', '');
const teams = useTeams(domain, stage);
const rows = computed(() =>
  (teams.data.value ?? [])
    .filter((team) =>
      matchesQuery(
        `${team.name} ${teamDisplayParts(team.name).label} ${team.manifest.description}`,
        search.value,
      ),
    )
    .sort((a, b) => (newestFirst.value ? -1 : 1) * a.created_at.localeCompare(b.created_at)),
);
const domains = computed(() =>
  [...new Set((everything.data.value ?? []).map((t) => t.domain).filter(Boolean))].sort(),
);
watch(workspaceId, () => closeFilter());
const label = (s: string) => `${s[0]!.toUpperCase()}${s.slice(1)}`;
</script>
<template>
  <section class="directory-page directory-table-page team-directory">
    <Teleport to="#page-header-actions">
      <RouterLink to="/teams/new" class="button primary directory-header-action"
        ><Icon name="plus" :size="14" />New Team</RouterLink
      >
    </Teleport>
    <p v-if="teams.error.value" class="inline-error" role="alert">
      {{ teams.error.value.message }}
    </p>
    <div v-if="everything.data.value?.length" class="agent-toolbar team-toolbar">
      <label class="agent-search"
        ><Icon name="search" :size="14" /><input
          v-model="search"
          placeholder="Search Teams..."
          aria-label="Search Teams"
      /></label>
      <div ref="filterAnchor" class="team-filter menu-anchor" @focusout="leaveFilter">
        <button
          ref="filterTrigger"
          class="button"
          :class="{ active: domain || stage }"
          aria-haspopup="dialog"
          :aria-expanded="filterOpen"
          aria-controls="team-filter-popover"
          @click="toggleFilter"
          @keydown.down.prevent="!filterOpen && toggleFilter()"
        >
          <Icon name="filter" :size="14" />Filter
        </button>
        <div
          v-if="filterOpen"
          id="team-filter-popover"
          role="dialog"
          aria-label="Filter Teams"
          class="team-filter-popover"
          @keydown.esc.stop.prevent="closeFilter(true)"
        >
          <label
            >Domain<select v-model="domain" aria-label="Domain">
              <option value="">All domains</option>
              <option v-for="d in domains" :key="d" :value="d">{{ d }}</option>
            </select></label
          >
          <label
            >Stage<select v-model="stage" aria-label="Stage">
              <option value="">All stages</option>
              <option v-for="s in stages" :key="s" :value="s">{{ label(s) }}</option>
            </select></label
          >
          <button
            v-if="domain || stage"
            class="text-button"
            @click="
              domain = '';
              stage = '';
            "
          >
            Clear filters
          </button>
        </div>
      </div>
      <button class="button agent-sort" @click="newestFirst = !newestFirst">
        <Icon :name="newestFirst ? 'down' : 'up'" :size="13" />Last updated
      </button>
    </div>
    <div v-if="everything.data.value?.length" class="team-table-scroll">
      <table class="directory-data-table directory-aligned-table team-table" aria-label="Teams">
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Domain</th>
            <th scope="col">Stage</th>
            <th scope="col">Agents</th>
            <th scope="col">Availability</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in rows" :key="t.id">
            <td>
              <RouterLink :to="'/teams/' + t.id" class="team-name"
                ><span class="team-icon"><Icon name="network" :size="18" /></span
                ><span class="team-label"><TeamName :name="t.name" /></span
              ></RouterLink>
            </td>
            <td>{{ t.domain || '—' }}</td>
            <td>{{ t.stage ? label(t.stage) : 'A set of stages' }}</td>
            <td>{{ t.manifest.members.length + 2 }}</td>
            <td>
              <span class="directory-last-value">{{
                t.source === 'shipped' ? 'Built in' : t.added ? 'Added' : 'Not added'
              }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="teams.isSuccess.value && search && !rows.length" class="agent-none">
      No matching Teams
    </p>
    <div
      v-if="teams.isSuccess.value && !search && !teams.data.value?.length"
      class="directory-empty"
    >
      <Icon name="network" :size="30" />
      <h2>No Teams yet</h2>
      <p>A Team is the agents that work each stage of your studies.</p>
      <RouterLink to="/teams/new" class="button"
        ><Icon name="plus" :size="14" />New Team</RouterLink
      >
    </div>
  </section>
</template>
<style src="./directory.css" />
<style src="./directory-table.css" />
<style scoped>
.team-directory {
  padding: 4px 4px 32px 20px;
}
.team-toolbar .agent-sort {
  margin-left: 0;
}
.team-filter {
  margin-left: auto;
}
.team-filter-popover {
  position: absolute;
  z-index: 40;
  top: calc(100% + 4px);
  right: 0;
  width: 240px;
  padding: 12px;
  background: var(--fill-menu);
  border: 1px solid #ffffff55;
  border-radius: 8px;
  box-shadow: 0 6px 16px #00000012;
}
.team-filter-popover label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 12px;
  color: var(--muted);
  font-size: 12px;
}
.team-filter-popover label:last-of-type {
  margin-bottom: 0;
}
.team-filter-popover select {
  width: 100%;
  border: 1px solid var(--border);
  background: var(--surface);
  border-radius: 6px;
  padding: 6px 8px;
  color: var(--text);
  font-size: 13px;
}
.team-filter-popover > button {
  margin-top: 12px;
}
.team-table tr {
  grid-template-columns: minmax(300px, 1fr) repeat(3, 120px) 128px;
  padding-right: 0;
}
.team-table th:not(:first-child) {
  text-align: center;
}
.team-table td:not(:first-child) {
  justify-content: center;
  text-align: center;
}
.team-table th:last-child {
  text-align: right;
}
.team-table td:last-child {
  justify-content: flex-end;
}
.team-table .directory-last-value {
  width: 70px;
  text-align: center;
}
.team-table-scroll {
  overflow-x: auto;
}
.team-table td:first-child .team-name {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  min-height: 24px;
  flex: 1;
  font-size: 13px;
  color: var(--text);
}
.team-name > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.team-icon {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  flex: none;
  color: var(--muted);
}
.team-table td {
  display: flex;
  align-items: center;
  font-size: 12px;
  color: var(--text);
}
</style>
