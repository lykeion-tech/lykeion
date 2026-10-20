<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { addTeam, useTeam, useTeams } from '../api/queries';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import Icon from '../components/Icon.vue';
import TeamMemberProfile from '../components/TeamMemberProfile.vue';
import { useTaskComposer } from '../state/task-composer';
const props = defineProps<{ id: string }>();
const client = useQueryClient();
const detail = useTeam(() => props.id);
const store = useTeams('', '');
const manifest = computed(() => detail.data.value?.manifest);
const added = computed(() => !!store.data.value?.find((t) => t.id === props.id)?.added);
const version = computed(() => detail.data.value?.versions.find((v) => v.id === props.id));
const people = computed(() =>
  manifest.value
    ? [
        { member: manifest.value.leader, role: 'Leader' },
        ...manifest.value.members.map((member) => ({ member, role: 'Member' })),
        { member: manifest.value.auditor, role: 'Auditor' },
      ]
    : [],
);
const tabs = ['Overview', 'Agents', 'Scientific settings', 'Versions'] as const;
const tab = ref<(typeof tabs)[number]>('Overview');
const busy = ref(false),
  error = ref(''),
  created = ref<number | null>(null);
let visit = 0;
watch([workspaceId, () => props.id], () => {
  visit++;
  tab.value = 'Overview';
  busy.value = false;
  error.value = '';
  created.value = null;
});
const { openComposer } = useTaskComposer();
const stage = (value: string) =>
  value ? value[0]!.toUpperCase() + value.slice(1) : 'A set of stages';
const day = (at: string) =>
  new Date(at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
const studies = (n: number) => `${n} ${n === 1 ? 'study' : 'studies'}`;
async function add() {
  if (busy.value) return;
  const ws = workspaceId.value,
    id = props.id,
    ownVisit = visit;
  busy.value = true;
  error.value = '';
  try {
    const agents = await addTeam(id);
    if (visit === ownVisit) created.value = agents.length;
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.me }),
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
      client.invalidateQueries({ queryKey: keys.agents(ws) }),
    ]);
  } catch (e) {
    if (visit === ownVisit) error.value = (e as Error).message;
  } finally {
    if (visit === ownVisit) busy.value = false;
  }
}
function newStudy() {
  if (manifest.value && added.value)
    openComposer(undefined, { type: 'team', teams: { [manifest.value.stage]: props.id } });
}
function tabKey(event: KeyboardEvent, index: number) {
  let target: number;
  if (event.key === 'ArrowRight') target = (index + 1) % tabs.length;
  else if (event.key === 'ArrowLeft') target = (index + tabs.length - 1) % tabs.length;
  else if (event.key === 'Home') target = 0;
  else if (event.key === 'End') target = tabs.length - 1;
  else return;
  event.preventDefault();
  tab.value = tabs[target]!;
  const buttons = (
    event.currentTarget as HTMLElement
  ).parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]');
  buttons?.[target]?.focus();
}
</script>
<template>
  <section class="agent-page team-detail-page">
    <template v-if="manifest">
      <header class="agent-hero">
        <nav class="agent-crumbs" aria-label="Breadcrumb">
          <RouterLink to="/teams">Teams</RouterLink><span>/</span
          ><span aria-current="page">{{ manifest.name }}</span>
        </nav>
        <div class="agent-identity">
          <span class="agent-avatar"><Icon name="network" :size="26" /></span>
          <div>
            <div class="agent-title">
              <h1>{{ manifest.name }}</h1>
              <span
                v-if="store.isSuccess.value"
                class="agent-status"
                :class="{ archived: !added }"
                >{{ added ? 'Added' : 'Not added' }}</span
              >
            </div>
            <p v-if="manifest.description" class="agent-description">{{ manifest.description }}</p>
            <p class="agent-meta">
              <span><Icon name="circle" :size="13" />{{ manifest.domain || 'No domain' }}</span
              ><span><Icon name="network" :size="13" />{{ stage(manifest.stage) }}</span
              ><span><Icon name="bot" :size="13" />{{ people.length }} agents</span
              ><span>Version {{ manifest.version }}</span>
            </p>
          </div>
          <div class="agent-actions">
            <button v-if="added" class="button primary" @click="newStudy">
              <Icon name="plus" :size="14" />New study</button
            ><button
              v-else
              class="button primary"
              :disabled="busy || !store.isSuccess.value"
              @click="add"
            >
              Add Team
            </button>
          </div>
        </div>
      </header>
      <div class="agent-tabs-bar" role="tablist" aria-label="Team sections">
        <button
          v-for="(t, i) in tabs"
          :id="'team-tab-' + i"
          :key="t"
          role="tab"
          :aria-selected="tab === t"
          :tabindex="tab === t ? 0 : -1"
          :aria-controls="'team-panel-' + i"
          @click="tab = t"
          @keydown="tabKey($event, i)"
        >
          {{ t }}
        </button>
      </div>
      <p v-if="error" class="inline-error agent-error" role="alert">
        {{ error }}
        <button class="text-button" :disabled="busy" @click="add">Retry adding Team</button>
      </p>
      <p v-if="store.error.value" class="inline-error agent-error" role="alert">
        {{ store.error.value.message }}
        <button class="text-button" @click="store.refetch()">Retry Team availability</button>
      </p>
      <p v-if="created !== null" class="team-created" role="status">
        Created {{ created }} {{ created === 1 ? 'agent' : 'agents' }} in your
        <RouterLink to="/agents">Agents</RouterLink> page
      </p>
      <div
        v-if="tab === 'Overview'"
        id="team-panel-0"
        class="agent-overview"
        role="tabpanel"
        aria-labelledby="team-tab-0"
      >
        <div class="team-roles">
          <h2>Team roles</h2>
          <p class="team-lede">A leader coordinates the work. An independent auditor reviews it.</p>
          <div class="team-role-cards">
            <TeamMemberProfile :member="manifest.leader" role="Leader" card /><TeamMemberProfile
              :member="manifest.auditor"
              role="Auditor"
              card
            />
          </div>
          <section class="team-section">
            <h2>Members</h2>
            <div class="team-member-list">
              <TeamMemberProfile v-for="m in manifest.members" :key="m.name" :member="m" />
            </div>
            <p v-if="!manifest.members.length" class="team-lede">No additional members.</p>
          </section>
        </div>
        <aside class="agent-panel" aria-label="Team summary">
          <h3>Team</h3>
          <dl>
            <dt>Domain</dt>
            <dd>{{ manifest.domain || 'No domain' }}</dd>
            <dt>Stage</dt>
            <dd>{{ stage(manifest.stage) }}</dd>
            <dt>Agents</dt>
            <dd>{{ people.length }}</dd>
            <dt>Steps per column</dt>
            <dd>{{ manifest.steps || 12 }}</dd>
            <dt>Availability</dt>
            <dd>{{ store.isSuccess.value ? (added ? 'Added' : 'Not added') : 'Loading…' }}</dd>
          </dl>
          <h3>Version</h3>
          <dl>
            <dt>Current</dt>
            <dd>v{{ manifest.version }}</dd>
            <template v-if="version"
              ><dt>Source</dt>
              <dd>
                {{ version.source === 'shipped' ? 'Shipped with Lykeion' : 'Uploaded by you' }}
              </dd></template
            >
          </dl>
          <p class="team-note">Studies keep the Team version they started with.</p>
        </aside>
      </div>
      <section
        v-else-if="tab === 'Agents'"
        id="team-panel-1"
        class="agent-work"
        role="tabpanel"
        aria-labelledby="team-tab-1"
      >
        <h2>Agents in this Team</h2>
        <p class="team-lede">The roles and capabilities this Team brings to a study.</p>
        <div class="team-member-list">
          <TeamMemberProfile
            v-for="person in people"
            :key="person.role + person.member.name"
            :member="person.member"
            :role="person.role"
          />
        </div>
      </section>
      <section
        v-else-if="tab === 'Scientific settings'"
        id="team-panel-2"
        class="agent-work"
        role="tabpanel"
        aria-labelledby="team-tab-2"
      >
        <h2>Scientific settings</h2>
        <p class="team-lede">The Team’s scope and the criteria used to review its work.</p>
        <dl class="team-settings">
          <dt>Domain</dt>
          <dd>{{ manifest.domain || 'No domain' }}</dd>
          <dt>Stage</dt>
          <dd>{{ stage(manifest.stage) }}</dd>
          <dt>Steps per column</dt>
          <dd>{{ manifest.steps || 12 }} steps per column</dd>
        </dl>
        <section class="team-section">
          <h2>Review criteria</h2>
          <ol class="team-criteria">
            <li>Does the work cite where its facts came from?</li>
            <li>Does it answer what the researcher asked?</li>
            <li>Does it say what it couldn't do?</li>
            <li v-for="c in manifest.criteria" :key="c">{{ c }}</li>
          </ol>
        </section>
      </section>
      <section
        v-else
        id="team-panel-3"
        class="agent-work"
        role="tabpanel"
        aria-labelledby="team-tab-3"
      >
        <h2>Versions</h2>
        <p class="team-lede">The version history of this Team.</p>
        <div class="team-version-scroll">
          <table class="team-versions" :aria-label="manifest.name + ' versions'">
            <thead>
              <tr>
                <th>Version</th>
                <th>Fingerprint</th>
                <th>Uploaded</th>
                <th>Studies</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="v in [...(detail.data.value?.versions || [])].reverse()" :key="v.id">
                <td>v{{ v.version }}</td>
                <td>
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
      </section>
    </template>
    <div v-else class="agent-work">
      <RouterLink to="/teams" class="text-button">← Teams</RouterLink>
      <p v-if="detail.isError.value" class="inline-error" role="alert">
        {{ detail.error.value?.message }}
        <button class="text-button" @click="detail.refetch()">Retry Team</button>
      </p>
      <p v-else class="muted" role="status">Loading Team…</p>
    </div>
  </section>
</template>
<style src="./agent-detail.css" />
<style scoped>
.team-detail-page h2 {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 12px;
}
.team-lede {
  color: var(--muted);
  font-size: 13px;
  margin: 0 0 24px;
}
.team-role-cards {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.team-section {
  margin-top: 28px;
}
.team-member-list {
  border-top: 1px solid var(--border);
}
.team-created {
  padding: 12px var(--gutter) 0;
  font-size: 13px;
}
.team-note {
  color: var(--muted);
  font-size: 12px;
}
.team-settings {
  display: grid;
  grid-template-columns: 180px minmax(0, 1fr);
  gap: 18px;
  font-size: 13px;
  max-width: 820px;
}
.team-settings dt {
  color: var(--muted);
}
.team-criteria {
  padding-left: 20px;
  color: var(--muted);
  font-size: 13px;
  line-height: 2;
}
.team-version-scroll {
  overflow-x: auto;
}
.team-versions {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.team-versions th {
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
  text-align: left;
  padding: 0 16px 12px 0;
}
.team-versions td {
  padding: 16px 16px 16px 0;
  border-top: 1px solid var(--border);
  white-space: nowrap;
}
.team-versions code {
  font-size: 12px;
}
@media (max-width: 900px) {
  .team-role-cards {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 600px) {
  .agent-tabs-bar {
    gap: 18px;
    overflow-x: auto;
  }
  .agent-tabs-bar button {
    white-space: nowrap;
  }
  .team-settings {
    grid-template-columns: 120px minmax(0, 1fr);
  }
}
</style>
