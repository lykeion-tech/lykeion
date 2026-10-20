<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { addTeam, useTeam, useTeams } from '../api/queries';
import type { TeamMember } from '../api/types';
import { matchesQuery } from '../lib/search';

const props = defineProps<{ id?: string }>();
const client = useQueryClient();
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
const stages = ['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'];
const everything = useTeams('', '');
const teams = useTeams(domain, stage);
const rows = computed(() =>
  (teams.data.value ?? [])
    .filter((team) => matchesQuery(`${team.name} ${team.manifest.description}`, search.value))
    .sort((a, b) => (newestFirst.value ? -1 : 1) * a.created_at.localeCompare(b.created_at)),
);
const detail = useTeam(() => props.id ?? '');
const domains = computed(() =>
  [...new Set((everything.data.value ?? []).map((t) => t.domain).filter(Boolean))].sort(),
);
const busy = ref(false);
const error = ref('');
const created = ref<string[] | null>(null);
watch([workspaceId, () => props.id], () => {
  error.value = '';
  created.value = null;
  closeFilter();
});

const label = (s: string) => `${s[0]!.toUpperCase()}${s.slice(1)}`;
const builtFor = (s: string) => (s ? `Built for ${label(s)}` : 'Built for a set of stages');
const day = (at: string) =>
  new Date(at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
const studies = (n: number) => `${n} ${n === 1 ? 'study' : 'studies'}`;
const connectors = (m: TeamMember) =>
  (m.connectors ?? []).map((c) => c.catalogue || c.url || c.name).join(', ');

async function act(work: () => Promise<unknown>) {
  if (busy.value) return;
  const workspace = workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    await work();
  } catch (e) {
    if (workspaceId.value === workspace) error.value = (e as Error).message;
  } finally {
    busy.value = false;
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.me }),
      client.invalidateQueries({ queryKey: keys.packs(workspace) }),
      client.invalidateQueries({ queryKey: keys.agents(workspace) }),
    ]);
  }
}

const add = (id: string) =>
  act(async () => {
    created.value = (await addTeam(id)).map((a) => a.name);
  });
</script>
<template>
  <section class="directory-page" :class="{ 'directory-table-page team-directory': !id }">
    <Teleport to="#page-header-actions">
      <RouterLink v-if="!id" to="/teams/new" class="button primary"
        ><Icon name="plus" :size="14" />New Team</RouterLink
      >
    </Teleport>
    <p v-if="teams.error.value" class="inline-error" role="alert">
      {{ teams.error.value.message }}
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <template v-if="id">
      <RouterLink to="/teams" class="text-button">← Teams</RouterLink>
      <div v-if="detail.data.value" class="research-detail">
        <header>
          <h1>{{ detail.data.value.manifest.name }}</h1>
          <button
            v-if="!everything.data.value?.find((t) => t.id === id)?.added"
            class="button primary"
            :disabled="busy"
            @click="add(id)"
          >
            Add Team
          </button>
          <span v-else class="muted">Added</span>
        </header>
        <p v-if="created" class="description" role="status">
          Created {{ created.length }} {{ created.length === 1 ? 'agent' : 'agents' }} in your
          <RouterLink to="/agents">Agents</RouterLink> page
        </p>
        <p class="description">{{ detail.data.value.manifest.description }}</p>
        <p class="description">
          {{ detail.data.value.manifest.domain }} ·
          {{ builtFor(detail.data.value.manifest.stage) }} ·
          {{ detail.data.value.manifest.steps || 12 }} steps per column
        </p>
        <h2>Leader</h2>
        <p class="description">
          <b>{{ detail.data.value.manifest.leader.name }}</b> ·
          {{ detail.data.value.manifest.leader.agent.harness }} ·
          {{ detail.data.value.manifest.leader.role }}
        </p>
        <h2>Members</h2>
        <ul class="team-members">
          <li v-for="m in detail.data.value.manifest.members" :key="m.name">
            <b>{{ m.name }}</b> · {{ m.agent.harness }} · {{ m.role }}
            <small v-if="m.skills?.length">Skills: {{ m.skills.join(', ') }}</small>
            <small v-if="m.connectors?.length">Connectors: {{ connectors(m) }}</small>
          </li>
        </ul>
        <h2>Auditor</h2>
        <p class="description">
          <b>{{ detail.data.value.manifest.auditor.name }}</b> ·
          {{ detail.data.value.manifest.auditor.agent.harness }} ·
          {{ detail.data.value.manifest.auditor.role }}
        </p>
        <h2>Criteria</h2>
        <ul class="team-members">
          <li>Does the work cite where its facts came from?</li>
          <li>Does it answer what the researcher asked?</li>
          <li>Does it say what it couldn't do?</li>
          <li v-for="c in detail.data.value.manifest.criteria" :key="c">{{ c }}</li>
        </ul>
        <h2>Versions</h2>
        <table class="team-versions" :aria-label="detail.data.value.manifest.name + ' versions'">
          <tbody>
            <tr v-for="v in [...detail.data.value.versions].reverse()" :key="v.id">
              <td>
                <b>v{{ v.version }}</b>
                <code :title="v.sha256">{{ v.sha256.slice(0, 4) }}…{{ v.sha256.slice(-4) }}</code>
              </td>
              <td>
                {{ v.source === 'shipped' ? 'Shipped with Lykeion' : 'Uploaded by you' }} ·
                {{ day(v.created_at) }}
              </td>
              <td>{{ studies(v.pinned_studies) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else-if="detail.isError.value" class="muted">No such Team.</p>
    </template>
    <template v-else>
      <div class="agent-toolbar team-toolbar">
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
      <div class="team-table-scroll">
        <table class="directory-data-table directory-aligned-table team-table" aria-label="Teams">
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Domain</th>
              <th scope="col">Stage</th>
              <th scope="col">Agents</th>
              <th scope="col">Version</th>
              <th scope="col">Availability</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in rows" :key="t.id">
              <td>
                <RouterLink :to="'/teams/' + t.id" class="team-name"
                  ><Icon name="network" :size="18" /><span>{{ t.name }}</span></RouterLink
                >
              </td>
              <td>{{ t.domain || '—' }}</td>
              <td>{{ t.stage ? label(t.stage) : 'A set of stages' }}</td>
              <td>{{ t.manifest.members.length + 2 }}</td>
              <td>{{ t.version }}</td>
              <td>{{ t.added ? 'Added' : 'Not added' }}</td>
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
    </template>
  </section>
</template>
<style src="./directory.css" />
<style src="./directory-table.css" />
<style scoped>
.team-directory {
  padding: 4px 24px 32px;
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
  padding-right: 0;
}
.team-table th:last-child,
.team-table td:last-child {
  text-align: right;
}
.team-table-scroll {
  overflow-x: auto;
}
.team-name {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  min-height: 24px;
  color: var(--text);
}
.team-name > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.team-name > svg {
  flex: none;
  color: var(--muted);
}
.team-table td:not(:first-child) {
  color: var(--muted);
}
.team-members {
  margin: 0;
  padding-left: 18px;
  font-size: 13px;
}
.team-members small {
  display: block;
  color: var(--muted);
}
h2 {
  margin: 20px 0 0;
  font-size: 13px;
  font-weight: 500;
}
.team-versions {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.team-versions td {
  padding: 10px 16px 10px 0;
  border-top: 1px solid var(--border);
  color: var(--muted);
}
.team-versions td:first-child {
  color: var(--text);
}
.team-versions code {
  margin-left: 6px;
}
</style>
