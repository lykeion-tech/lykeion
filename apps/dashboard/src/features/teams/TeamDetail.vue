<script setup lang="ts">
import { matchesQuery } from '../../lib/search';
import {
  boardColumnOf,
  boardColumns,
  studyDisplayTitle,
  studyNumber,
  timeAgo,
} from '../../lib/studies';
import { teamDisplayParts, teamDomain } from '../../lib/team-name';
import { backendLabel } from '../../lib/backends';
import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { api } from '../../api/client';
import type { TeamMember } from '../../api/types';
import Modal from '../../ui/Modal.vue';
import TeamSkillsPicker from './TeamSkillsPicker.vue';
import { useQueryClient } from '@tanstack/vue-query';
import {
  addTeam,
  duplicateTeam,
  useTeam,
  useTeams,
  useCards,
  useWorkspace,
  useMe,
} from '../../api/queries';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import Icon from '../../ui/Icon.vue';
import EmojiIcon from '../../ui/EmojiIcon.vue';
import TeamMemberProfile from './TeamMemberProfile.vue';
import ModelSwitcher from '../coresearch/cowrite/ModelSwitcher.vue';
import '../coresearch/l2.css';
import { useStudyComposer } from '../../state/study-composer';
const props = defineProps<{ id: string }>();
const client = useQueryClient();
const router = useRouter();
const skillMember = ref<TeamMember | null>(null);
const skillsBusy = ref(false);
const skillsError = ref('');
// Saving replaces the Team in place; the server refuses while a study uses it.
async function saveSkills(skills: string[]) {
  if (!skillMember.value || skillsBusy.value) return;
  const ownVisit = visit,
    ws = workspaceId.value,
    id = props.id;
  skillsBusy.value = true;
  skillsError.value = '';
  try {
    await api(
      'POST',
      `/api/teams/${id}/skills`,
      { member: skillMember.value.name, skills },
      undefined,
      { workspace: ws },
    );
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
      client.invalidateQueries({ queryKey: keys.agents(ws) }),
    ]);
    if (ownVisit === visit) skillMember.value = null;
  } catch (e) {
    if (ownVisit === visit) skillsError.value = (e as Error).message;
  } finally {
    if (ownVisit === visit) skillsBusy.value = false;
  }
}
const detail = useTeam(() => props.id);
const me = useMe();
const settingsName = ref('');
const settingsDescription = ref('');
const settingsVisibility = ref<'private' | 'workspace'>('private');
const settingsBusy = ref(false);
const settingsError = ref('');
watch(
  () => detail.data.value,
  (value) => {
    if (!value) return;
    settingsName.value = value.manifest.name;
    settingsDescription.value = value.manifest.description || '';
    settingsVisibility.value = value.visibility || 'private';
  },
  { immediate: true },
);
const canEditTeam = computed(
  () =>
    !!detail.data.value && detail.data.value.uploaded_by === me.data.value?.id && !builtIn.value,
);
async function saveSettings(input: Record<string, string>) {
  if (settingsBusy.value || !canEditTeam.value) return;
  const ownVisit = visit,
    ws = workspaceId.value,
    id = props.id;
  settingsBusy.value = true;
  settingsError.value = '';
  try {
    await api('PATCH', `/api/teams/${id}`, input, undefined, { workspace: ws });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.team(ws, id) }),
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
    ]);
  } catch (e) {
    if (ownVisit === visit) settingsError.value = (e as Error).message;
  } finally {
    if (ownVisit === visit) settingsBusy.value = false;
  }
}

const store = useTeams('', '');
const manifest = computed(() => detail.data.value?.manifest);
const row = computed(() => store.data.value?.find((t) => t.id === props.id));
const added = computed(() => !!row.value?.added);
// A built-in Team is in every workspace, works without Add and can't be changed.
const builtIn = computed(() => row.value?.source === 'shipped');
const displayDomain = computed(() =>
  manifest.value ? teamDomain(row.value?.source, manifest.value.stage, manifest.value.domain) : '',
);
const availability = computed(() =>
  builtIn.value ? 'Built in' : added.value ? 'Added' : 'Not added',
);
const copyName = ref<string | null>(null);
const copyBusy = ref(false);
const copyError = ref('');
async function duplicate() {
  const name = copyName.value?.trim();
  if (!name || copyBusy.value) return;
  const ownVisit = visit,
    ws = workspaceId.value;
  copyBusy.value = true;
  copyError.value = '';
  try {
    const copy = await duplicateTeam(props.id, name);
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
      client.invalidateQueries({ queryKey: keys.agents(ws) }),
    ]);
    if (ownVisit === visit) await router.push(`/teams/${copy.id}`);
  } catch (e) {
    if (ownVisit === visit) copyError.value = (e as Error).message;
  } finally {
    if (ownVisit === visit) copyBusy.value = false;
  }
}
const people = computed(() =>
  manifest.value
    ? [
        { member: manifest.value.leader, role: 'Leader' },
        ...manifest.value.members.map((member) => ({ member, role: 'Member' })),
        { member: manifest.value.auditor, role: 'Auditor' },
      ]
    : [],
);
const selectedPerson = ref('leader');
const selected = computed(() => {
  if (!manifest.value) return null;
  if (selectedPerson.value === 'auditor')
    return { member: manifest.value.auditor, role: 'Auditor' };
  if (selectedPerson.value.startsWith('member:')) {
    const member = manifest.value.members[Number(selectedPerson.value.slice(7))];
    if (member) return { member, role: 'Member' };
  }
  return { member: manifest.value.leader, role: 'Leader' };
});
const harnesses = computed(() => {
  if (builtIn.value) {
    const name = selected.value?.member.name;
    const harness = name && detail.data.value?.my_agents?.[name]?.harness;
    return harness ? backendLabel(harness) : '';
  }
  const clis = people.value.flatMap(({ member }) =>
    member.agent.models
      ? Object.keys(member.agent.models)
      : member.agent.harness
        ? [member.agent.harness]
        : [],
  );
  return [...new Set(clis)].map(backendLabel).join(' / ');
});
const cards = computed(() =>
  manifest.value
    ? [
        { member: manifest.value.leader, role: 'Leader', key: 'leader' },
        { member: manifest.value.auditor, role: 'Auditor', key: 'auditor' },
        ...manifest.value.members.map((member, index) => ({
          member,
          role: 'Member',
          key: `member:${index}`,
        })),
      ]
    : [],
);
const cardsQuery = useCards();
const workspace = useWorkspace();
const search = ref('');
const collapsed = ref<string[]>([]);
const groups = computed(() => {
  const assigned = (cardsQuery.data.value ?? [])
    .filter(
      (card) =>
        Object.values(card.teams ?? {}).some((team) => team.pack_id === props.id) ||
        (card.assignee_type === 'team' && card.assignee_id === props.id),
    )
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  return boardColumns.map((column) => ({
    ...column,
    cards: assigned.filter(
      (card) =>
        boardColumnOf(card) === column.id &&
        matchesQuery(
          `${studyNumber(workspace.data.value, card)} ${card.title} ${card.question}`,
          search.value,
        ),
    ),
  }));
});
function toggleGroup(id: string) {
  collapsed.value = collapsed.value.includes(id)
    ? collapsed.value.filter((item) => item !== id)
    : [...collapsed.value, id];
}
const tabs = ['Overview', 'Work', 'Settings'] as const;
const tab = ref<(typeof tabs)[number]>('Overview');
const settingsSection = ref<'general' | 'access'>('general');
const busy = ref(false),
  error = ref(''),
  created = ref<number | null>(null);
const iconBusy = ref(false);
let visit = 0;
const memberCLIBusy = ref(false);
const memberCLIError = ref('');
async function changeMemberCLI(harness: string) {
  const member = selected.value?.member;
  const agent = member && detail.data.value?.my_agents?.[member.name];
  if (!member || !agent || memberCLIBusy.value) return;
  if (harness === agent.harness) return;
  const model = member.agent.models?.[harness] ?? '';
  const ws = workspaceId.value;
  const teamID = props.id;
  const ownVisit = visit;
  memberCLIBusy.value = true;
  memberCLIError.value = '';
  try {
    await api('PATCH', `/api/agents/${agent.id}`, { harness, model }, undefined, { workspace: ws });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.team(ws, teamID) }),
      client.invalidateQueries({ queryKey: keys.agents(ws) }),
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
    ]);
  } catch (e) {
    if (ownVisit === visit) {
      memberCLIError.value = (e as Error).message;
    }
  } finally {
    if (ownVisit === visit) memberCLIBusy.value = false;
  }
}
watch([workspaceId, () => props.id], () => {
  visit++;
  skillMember.value = null;
  skillsBusy.value = false;
  skillsError.value = '';
  tab.value = 'Overview';
  settingsSection.value = 'general';
  selectedPerson.value = 'leader';
  search.value = '';
  collapsed.value = [];
  busy.value = false;
  error.value = '';
  created.value = null;
  copyName.value = null;
  copyBusy.value = false;
  copyError.value = '';
  iconBusy.value = false;
  memberCLIBusy.value = false;
  memberCLIError.value = '';
  settingsBusy.value = false;
  settingsError.value = '';
});
async function saveIcon(value: string) {
  if (iconBusy.value) return;
  const ownVisit = visit,
    ws = workspaceId.value,
    id = props.id;
  iconBusy.value = true;
  error.value = '';
  try {
    await api('PATCH', `/api/teams/${id}`, { avatar_url: value }, undefined, { workspace: ws });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.team(ws, id) }),
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
    ]);
  } catch (e) {
    if (visit === ownVisit) error.value = (e as Error).message;
  } finally {
    if (visit === ownVisit) iconBusy.value = false;
  }
}
async function deleteTeam() {
  if (
    busy.value ||
    !manifest.value ||
    !confirm(
      `Delete ${teamDisplayParts(manifest.value.name).label}? Its Agent copies will also be deleted, including copies owned by other workspace members. Past studies and run history will be kept.`,
    )
  )
    return;
  const ownVisit = visit,
    ws = workspaceId.value,
    id = props.id;
  busy.value = true;
  error.value = '';
  try {
    await api('DELETE', `/api/teams/${id}`, undefined, undefined, { workspace: ws });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
      client.invalidateQueries({ queryKey: keys.agents(ws) }),
      client.invalidateQueries({ queryKey: ['ws', ws, 'agent'] }),
    ]);
    client.removeQueries({ queryKey: keys.team(ws, id) });
    if (visit === ownVisit) await router.push('/teams');
  } catch (e) {
    if (visit === ownVisit) error.value = (e as Error).message;
  } finally {
    if (visit === ownVisit) busy.value = false;
  }
}
const { openComposer } = useStudyComposer();
const stage = (value: string) =>
  value ? value[0]!.toUpperCase() + value.slice(1) : 'A set of stages';
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
  if (manifest.value && (added.value || builtIn.value))
    openComposer({ type: 'team', teams: { [manifest.value.stage]: props.id } });
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
      <div class="agent-page-header">
        <nav class="detail-breadcrumb team-breadcrumb" aria-label="Team navigation">
          <RouterLink to="/teams"><Icon name="back" :size="14" />Teams</RouterLink>
          <span aria-hidden="true">/</span>
          <span aria-current="page">{{ teamDisplayParts(manifest.name).label }}</span>
        </nav>
        <header class="agent-hero">
          <div class="agent-identity">
            <EmojiIcon
              :key="workspaceId + ':' + id"
              :value="detail.data.value?.avatar_url || ''"
              :editable="canEditTeam"
              :busy="iconBusy"
              fallback="network"
              @select="saveIcon"
            />
            <div class="team-heading">
              <div class="agent-title">
                <h1>{{ teamDisplayParts(manifest.name).label }}</h1>
                <span
                  v-if="store.isSuccess.value"
                  class="agent-status"
                  :class="{ archived: !added && !builtIn }"
                  >{{ availability }}</span
                >
              </div>
              <p v-if="manifest.description" class="agent-description">
                {{ manifest.description }}
              </p>
              <p class="agent-meta">
                <span><Icon name="network" :size="13" />{{ stage(manifest.stage) }}</span
                ><span v-if="harnesses"><Icon name="monitor" :size="13" />{{ harnesses }}</span
                ><span v-if="displayDomain"
                  ><Icon name="circle" :size="13" />{{ displayDomain }}</span
                ><span><Icon name="bot" :size="13" />{{ people.length }} agents</span>
              </p>
            </div>
            <div class="agent-actions">
              <button v-if="canEditTeam" class="button" :disabled="busy" @click="deleteTeam">
                <Icon name="trash" :size="14" />Delete Team
              </button>
              <button
                v-if="builtIn"
                class="button"
                @click="
                  copyError = '';
                  copyName = manifest.name + ' (duplicate)';
                "
              >
                Duplicate</button
              ><button v-if="added || builtIn" class="button primary" @click="newStudy">
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
        class="team-studio"
        role="tabpanel"
        aria-labelledby="team-tab-0"
      >
        <div class="team-canvas-column">
          <div class="team-canvas">
            <div class="canvas-heading">
              <strong>Team</strong><span>{{ people.length }} agents</span>
            </div>
            <div class="canvas-layout">
              <button
                v-for="person in cards"
                :key="person.key"
                class="canvas-agent"
                :aria-pressed="selectedPerson === person.key"
                @click="selectedPerson = person.key"
              >
                <span class="card-identity"
                  ><span class="avatar">{{ person.member.name[0] }}</span
                  ><span
                    ><strong>{{ person.member.name }}</strong
                    ><small>{{ person.role }}</small></span
                  ></span
                >
                <span class="card-description">{{ person.member.role }}</span>
              </button>
            </div>
          </div>
          <p v-if="builtIn" class="team-note">
            Built-in Teams can't be changed. Duplicate this Team to make one of your own.
          </p>
        </div>
        <aside v-if="selected" class="team-inspector" aria-label="Agent details">
          <h2>Agent details</h2>
          <TeamMemberProfile
            :member="selected.member"
            :role="selected.role"
            :editable-skills="canEditTeam"
            @edit-skills="
              skillsError = '';
              skillMember = selected.member;
            "
          />
          <div class="inspector-harness">
            <span>CLI</span>
            <div
              v-if="builtIn && detail.data.value?.my_agents?.[selected.member.name]"
              class="coresearch team-cli-picker"
            >
              <ModelSwitcher
                :cli="detail.data.value.my_agents[selected.member.name]!.harness"
                :model-value="detail.data.value.my_agents[selected.member.name]!.model"
                :models="[]"
                :clis="
                  Object.keys(
                    selected.member.agent.models ?? { [selected.member.agent.harness ?? '']: '' },
                  )
                "
                :disabled="memberCLIBusy"
                cli-only
                @update:cli="changeMemberCLI"
              />
            </div>
            <p v-else-if="builtIn">Connect a machine to set up your Team agents.</p>
            <p v-else>
              {{
                Object.keys(
                  selected.member.agent.models ?? { [selected.member.agent.harness ?? '']: '' },
                )
                  .map(backendLabel)
                  .join(' or ')
              }}
            </p>
            <p v-if="memberCLIError" role="alert">{{ memberCLIError }}</p>
          </div>
        </aside>
      </div>
      <section
        v-else-if="tab === 'Work'"
        id="team-panel-1"
        class="agent-work"
        role="tabpanel"
        aria-labelledby="team-tab-1"
      >
        <div class="agent-work-toolbar">
          <label class="team-work-search"
            ><Icon name="search" :size="14" /><input
              v-model="search"
              placeholder="Search studies..."
              aria-label="Search studies"
          /></label>
        </div>
        <p v-if="cardsQuery.isPending.value" class="muted" role="status">Loading studies…</p>
        <p v-else-if="cardsQuery.isError.value" class="inline-error" role="alert">
          {{ cardsQuery.error.value?.message }}
          <button class="text-button" @click="cardsQuery.refetch()">Retry studies</button>
        </p>
        <template v-else
          ><section v-for="group in groups" :key="group.id" class="agent-group">
            <button
              class="agent-group-head"
              :class="'group-' + group.id"
              :aria-expanded="!collapsed.includes(group.id)"
              @click="toggleGroup(group.id)"
            >
              <Icon name="down" :size="12" /><span class="stage-ring" />{{ group.name
              }}<small>{{ group.cards.length }}</small>
            </button>
            <template v-if="!collapsed.includes(group.id)"
              ><RouterLink
                v-for="card in group.cards"
                :key="card.id"
                :to="'/studies/' + card.id"
                class="agent-study"
                ><small>{{ studyNumber(workspace.data.value, card) }}</small
                ><span>{{ studyDisplayTitle(card.title || card.question) }}</span
                ><em>{{ timeAgo(card.updated_at) }}</em></RouterLink
              >
              <p v-if="!group.cards.length" class="agent-group-empty">No studies</p></template
            >
          </section></template
        >
      </section>
      <section
        v-else-if="tab === 'Settings'"
        id="team-panel-2"
        class="agent-split"
        role="tabpanel"
        aria-labelledby="team-tab-2"
      >
        <nav class="agent-subnav" aria-label="Team settings">
          <button
            :aria-current="settingsSection === 'general' ? 'page' : undefined"
            @click="settingsSection = 'general'"
          >
            General
          </button>
          <button
            :aria-current="settingsSection === 'access' ? 'page' : undefined"
            @click="settingsSection = 'access'"
          >
            Access
          </button>
        </nav>
        <div v-if="settingsSection === 'general'" class="agent-pane">
          <h2>General</h2>
          <h3>Profile</h3>
          <form
            @submit.prevent="
              saveSettings({ name: settingsName.trim(), description: settingsDescription })
            "
          >
            <div class="agent-card">
              <label class="agent-field"
                >Name<input
                  v-model="settingsName"
                  aria-label="Team name"
                  maxlength="64"
                  required
                  :readonly="!canEditTeam"
              /></label>
              <label class="agent-field"
                >Description<span>
                  <textarea
                    v-model="settingsDescription"
                    aria-label="Team description"
                    maxlength="1000"
                    :readonly="!canEditTeam"
                  /><small>{{ settingsDescription.length }} / 1000</small></span
                ></label
              >
            </div>
            <p v-if="settingsError" class="inline-error" role="alert">{{ settingsError }}</p>
            <footer v-if="canEditTeam">
              <button class="button primary" :disabled="settingsBusy || !settingsName.trim()">
                Save changes
              </button>
            </footer>
          </form>
          <h2>Details</h2>
          <p class="team-lede">The Team’s scope and the criteria used to review its work.</p>
          <dl class="team-settings">
            <dt>Domain</dt>
            <dd>{{ displayDomain || 'No domain' }}</dd>
            <dt>Stage</dt>
            <dd>{{ stage(manifest.stage) }}</dd>
            <template v-if="row"
              ><dt>Source</dt>
              <dd>
                {{
                  builtIn
                    ? 'Built into Lykeion'
                    : canEditTeam
                      ? 'Uploaded by you'
                      : 'Shared by a workspace member'
                }}
              </dd></template
            >
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
        </div>
        <form
          v-else
          class="agent-pane"
          @submit.prevent="saveSettings({ visibility: settingsVisibility })"
        >
          <h2>Access</h2>
          <h3>Who can use this Team</h3>
          <div class="agent-card agent-radios" role="radiogroup" aria-label="Access">
            <label
              ><input
                v-model="settingsVisibility"
                type="radio"
                value="private"
                :disabled="!canEditTeam"
              /><span
                ><b>Only the owner</b
                ><small
                  >Only the owner can add and use this Team. Existing Agent copies remain with their
                  owners.</small
                ></span
              ></label
            >
            <label
              ><input
                v-model="settingsVisibility"
                type="radio"
                value="workspace"
                :disabled="!canEditTeam"
              /><span
                ><b>Entire workspace</b
                ><small>Every workspace member can add and use this Team.</small></span
              ></label
            >
          </div>
          <p v-if="settingsError" class="inline-error" role="alert">{{ settingsError }}</p>
          <footer v-if="canEditTeam">
            <button
              class="button primary"
              :disabled="settingsBusy || settingsVisibility === detail.data.value?.visibility"
            >
              Save changes
            </button>
          </footer>
        </form>
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
    <TeamSkillsPicker
      v-if="skillMember"
      :member="skillMember"
      :busy="skillsBusy"
      :error="skillsError"
      @close="skillMember = null"
      @save="saveSkills"
    />
    <Modal
      v-if="copyName !== null"
      title="Duplicate this Team"
      @close="!copyBusy && (copyName = null)"
    >
      <form class="team-copy" @submit.prevent="duplicate">
        <h2>Duplicate this Team</h2>
        <p class="team-lede">The duplicate is a Team of your own that you can change.</p>
        <label
          >Name<input v-model="copyName" aria-label="Name of the duplicate" :disabled="copyBusy"
        /></label>
        <p v-if="copyError" role="alert" class="inline-error">{{ copyError }}</p>
        <footer>
          <button type="button" class="button" :disabled="copyBusy" @click="copyName = null">
            Cancel</button
          ><button class="button primary" :disabled="copyBusy || !copyName.trim()">
            {{ copyBusy ? 'Duplicating…' : 'Duplicate' }}
          </button>
        </footer>
      </form>
    </Modal>
  </section>
</template>
<style src="../agents/agent-detail.css" />
<style scoped>
.team-detail-page {
  --gutter: 16px;
  scrollbar-gutter: auto;
}
.team-work-search {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: 224px;
  padding: 7px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--muted);
}
.team-work-search input {
  min-width: 0;
  width: 100%;
  border: 0;
  outline: 0;
  background: transparent;
  color: var(--text);
  font-size: 12px;
}
.team-work-search:focus-within {
  outline: 2px solid var(--primary);
  outline-offset: 1px;
}

.team-heading {
  flex: 1;
  min-width: 0;
}
.team-detail-page .agent-identity > .agent-actions {
  flex: none;
}
.team-studio {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  gap: 24px;
  padding: 24px var(--gutter);
  align-items: start;
}
.team-canvas {
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  overflow: hidden;
}
.canvas-heading {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 18px 20px;
  font-size: 13px;
}
.canvas-heading span {
  color: var(--muted);
  font-size: 12px;
}
.canvas-layout {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  grid-auto-rows: 1fr;
}
.canvas-agent {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 14px;
  min-width: 0;
  min-height: 130px;
  padding: 20px;
  text-align: left;
  background: var(--surface);
  border: 0;
  border-top: 1px solid var(--border);
}
.canvas-agent:nth-child(even) {
  border-left: 1px solid var(--border);
}
.canvas-agent:nth-child(odd):last-child {
  border-right: 1px solid var(--border);
}
.canvas-agent:nth-child(even):nth-last-child(2) {
  border-bottom: 1px solid var(--border);
}
.canvas-agent:hover {
  background: var(--hover);
}
.canvas-agent[aria-pressed='true'] {
  background: color-mix(in srgb, var(--primary) 8%, var(--surface));
  box-shadow: inset 3px 0 var(--primary);
}
.canvas-agent:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: -3px;
}
.card-identity {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.canvas-agent .avatar {
  width: 32px;
  height: 32px;
  flex: none;
  font-size: 12px;
}
.canvas-agent strong {
  display: block;
  font-size: 13px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.canvas-agent small {
  display: inline-block;
  margin-top: 3px;
  padding: 2px 5px;
  border-radius: 4px;
  background: var(--input);
  color: var(--muted);
  font-size: 11px;
}
.card-description {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.team-canvas-column .team-note {
  margin: 16px 0 0;
}
.team-inspector {
  min-width: 0;
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--input);
}
.team-inspector :deep(.avatar) {
  background: var(--surface);
}
.team-detail-page .team-inspector h2 {
  color: var(--text);
  font-size: 14px;
  font-weight: 600;
}
.inspector-harness {
  padding-top: 16px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.inspector-harness span {
  color: var(--muted);
}
.inspector-harness p {
  margin-top: 6px;
}
.team-cli-picker {
  width: 100%;
  height: auto;
  margin-top: 6px;
  background: transparent;
}
.team-cli-picker :deep(.cli-menu--with-cli) {
  width: max-content;
  max-width: calc(100vw - 32px);
}
@container (max-width: 1000px) {
  .team-studio {
    grid-template-columns: 1fr;
  }
}
@container (max-width: 600px) {
  .canvas-layout {
    grid-template-columns: 1fr;
  }
  .canvas-agent:nth-child(even) {
    border-left: 0;
  }
  .canvas-agent:nth-child(odd):last-child {
    border-right: 0;
  }
  .canvas-agent:nth-child(even):nth-last-child(2) {
    border-bottom: 0;
  }
}

.team-detail-page .agent-pane > h2:first-child {
  font-size: 16px;
  font-weight: 500;
  margin-bottom: 30px;
}
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
.team-copy {
  padding: 24px;
}
.team-copy label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: var(--muted);
  font-size: 12px;
}
.team-copy input {
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text);
  font-size: 13px;
}
.team-copy footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
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
