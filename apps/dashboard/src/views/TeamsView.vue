<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { addTeam, useTeam, useTeams } from '../api/queries';
import type { TeamMember } from '../api/types';

const props = defineProps<{ id?: string }>();
const client = useQueryClient();
const domain = ref('');
const stage = ref('');
const stages = ['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'];
const everything = useTeams('', '');
const teams = useTeams(domain, stage);
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
  <section class="directory-page" :class="{ 'directory-table-page': !id }">
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
      <div class="team-table-scroll">
        <table class="directory-data-table team-table" aria-label="Teams">
          <colgroup>
            <col class="name-column" />
            <col />
            <col />
            <col />
            <col />
            <col />
          </colgroup>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col" aria-label="Domain">
                <span class="header-filter"
                  ><select v-model="domain" aria-label="Domain">
                    <option value="">Domain</option>
                    <option v-for="d in domains" :key="d" :value="d">{{ d }}</option></select
                  ><Icon name="down" :size="12"
                /></span>
              </th>
              <th scope="col" aria-label="Built for">
                <span class="header-filter"
                  ><select v-model="stage" aria-label="Stage">
                    <option value="">Built for</option>
                    <option v-for="s in stages" :key="s" :value="s">{{ label(s) }}</option></select
                  ><Icon name="down" :size="12"
                /></span>
              </th>
              <th scope="col">Agents</th>
              <th scope="col">Version</th>
              <th scope="col">Availability</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in teams.data.value" :key="t.id">
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
      <div v-if="teams.isSuccess.value && !teams.data.value?.length" class="directory-empty">
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
.header-filter {
  position: relative;
  display: inline-flex;
  align-items: center;
  max-width: 100%;
}
.header-filter select {
  appearance: none;
  width: 100%;
  min-width: 0;
  border: 0;
  border-radius: 0;
  padding: 0 18px 0 0;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}
.header-filter svg {
  position: absolute;
  right: 0;
  pointer-events: none;
}
.team-table-scroll {
  overflow-x: auto;
}
.team-table {
  min-width: 780px;
  table-layout: fixed;
}
.team-table .name-column {
  width: 44%;
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
.team-table th:first-child {
  padding-left: 52px;
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
