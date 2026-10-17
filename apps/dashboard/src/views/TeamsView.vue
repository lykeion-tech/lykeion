<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import { api, ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import {
  approvePack,
  uploadPack,
  useMe,
  usePackCatalog,
  usePackVersions,
  useTeam,
} from '../api/queries';
import type { Field, PackVersion } from '../api/types';

const props = defineProps<{ name?: string }>();
const client = useQueryClient();
const me = useMe();
const catalog = usePackCatalog();
const mine = computed(() => me.data.value?.pack_name ?? '');
const myTeam = computed(() => catalog.data.value?.find((t) => t.name === mine.value));
const myVersions = usePackVersions(mine);
const opened = computed(() => catalog.data.value?.find((t) => t.name === props.name));
const versions = usePackVersions(() => props.name ?? '');
const sections = computed(() => [
  {
    title: "Lykeion's Teams",
    teams: catalog.data.value?.filter((t) => t.source === 'shipped') ?? [],
  },
  {
    title: 'Your Teams',
    teams: catalog.data.value?.filter((t) => t.source === 'uploaded') ?? [],
  },
]);
// The version new studies use: the newest one this person approved.
const inUse = computed(() => versions.data.value?.filter((v) => v.approvals_by_me).at(-1)?.id);
// Your Team's current version, while you haven't approved it.
const waiting = computed(() => {
  const team = myTeam.value;
  const rows = myVersions.data.value;
  if (!team || !rows) return null;
  const row = rows.find((v) => v.version === String(team.version));
  return row?.approvals_by_me ? null : { team, row };
});
const busy = ref(false);
const error = ref('');
const file = ref<HTMLInputElement>();
watch([workspaceId, mine, () => props.name], () => {
  error.value = '';
});

const agents = (t: Field) =>
  t.agents
    .filter((a) => a.roles.length)
    .map((a) => `${a.column[0]!.toUpperCase()}${a.column.slice(1)}: ${a.roles.join(', ')}`)
    .join(' · ');
const summary = (t: Field) =>
  [
    t.description || (t.source === 'uploaded' ? 'Uploaded by you' : ''),
    `Starts from: ${t.start_kinds.join(', ')}`,
    `v${t.version}`,
  ]
    .filter(Boolean)
    .join(' · ');
const day = (at: string) =>
  new Date(at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
const studies = (n: number) => `${n} ${n === 1 ? 'study' : 'studies'}`;

async function act(work: () => Promise<unknown>) {
  if (busy.value) return;
  const workspace = workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    await work();
  } catch (e) {
    if (workspaceId.value === workspace)
      error.value =
        e instanceof ApiError && e.status === 413
          ? 'A Team archive can be at most 5 MiB.'
          : (e as Error).message;
  } finally {
    busy.value = false;
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.me }),
      client.invalidateQueries({ queryKey: keys.packs(workspace) }),
    ]);
  }
}

// A shipped Team's current version is approved by choosing it again; an
// upload's version is approved by id.
const approve = (team: Field, row?: PackVersion) =>
  act(() => (team.source === 'shipped' || !row ? useTeam(team.name) : approvePack(row.id)));

function makeMine(team: Field) {
  if (
    !confirm(
      `Make ${team.name} your Team? This approves v${team.version}, whose check code runs in the gate for your new studies. Studies already running stay on their version.`,
    )
  )
    return;
  void act(async () => {
    if (team.source === 'uploaded') {
      const rows = await api<PackVersion[]>(
        'GET',
        `/api/packs?name=${encodeURIComponent(team.name)}`,
      );
      const newest = rows.at(-1);
      if (newest && !newest.approvals_by_me) await approvePack(newest.id);
    }
    await useTeam(team.name);
  });
}

function upload(event: Event) {
  const input = event.target as HTMLInputElement;
  const chosen = input.files?.[0];
  input.value = '';
  if (chosen) void act(() => uploadPack(chosen));
}
</script>
<template>
  <section class="directory-page">
    <input
      ref="file"
      type="file"
      accept=".zip,application/zip"
      hidden
      aria-hidden="true"
      @change="upload"
    />
    <Teleport v-if="!name" to="#page-header-actions">
      <button class="button primary" :disabled="busy" @click="file?.click()">
        <Icon name="upload" :size="14" />Upload a Team
      </button>
    </Teleport>
    <p v-if="catalog.error.value" class="inline-error" role="alert">
      {{ catalog.error.value.message }}
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <div v-if="waiting" class="team-notice" role="status">
      <div>
        <b
          >{{ waiting.team.name }} Team v{{ waiting.team.version }} is available and needs your
          approval.</b
        >
        <span
          >Approving runs its check code in the gate for your new studies, in a sandbox with no
          network or files. Running studies stay on their version.</span
        >
      </div>
      <button class="button primary" :disabled="busy" @click="approve(waiting.team, waiting.row)">
        Approve v{{ waiting.team.version }}
      </button>
    </div>
    <template v-if="name">
      <RouterLink to="/teams" class="text-button">← Teams</RouterLink>
      <div v-if="opened" class="research-detail">
        <header>
          <h1>{{ opened.name }}</h1>
          <span v-if="opened.name === mine" class="chip">Your Team</span>
          <button v-else class="button primary" :disabled="busy" @click="makeMine(opened)">
            Make my Team
          </button>
        </header>
        <p class="description">
          {{ opened.description || (opened.source === 'uploaded' ? 'Uploaded by you.' : '') }}
        </p>
        <p class="description">
          {{ agents(opened) }}
        </p>
        <h2>Versions</h2>
        <table
          v-if="versions.data.value"
          class="team-versions"
          :aria-label="opened.name + ' versions'"
        >
          <tbody>
            <tr v-for="v in [...versions.data.value].reverse()" :key="v.id">
              <td>
                <b>v{{ v.version }}</b>
                <code :title="v.sha256">{{ v.sha256.slice(0, 4) }}…{{ v.sha256.slice(-4) }}</code>
                <span v-if="v.id === inUse && opened.name === mine" class="chip">new studies</span>
              </td>
              <td>
                {{ v.source === 'shipped' ? 'Shipped with Lykeion' : 'Uploaded by you' }} ·
                {{ day(v.created_at) }}
              </td>
              <td>
                <template v-if="v.approvals_by_me"
                  >✓ Approved by you<template v-if="v.approved_at">
                    · {{ day(v.approved_at) }}</template
                  ></template
                >
                <template v-else
                  >Not approved
                  <button class="button" :disabled="busy" @click="act(() => approvePack(v.id))">
                    Approve v{{ v.version }}
                  </button></template
                >
              </td>
              <td>{{ studies(v.pinned_studies) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else-if="catalog.isSuccess.value" class="muted">No Team called {{ name }}.</p>
    </template>
    <template v-else>
      <section
        v-for="(s, i) in sections"
        v-show="s.teams.length"
        :key="s.title"
        class="team-section"
        :aria-labelledby="'teams-section-' + i"
      >
        <h2 :id="'teams-section-' + i">{{ s.title }}</h2>
        <div class="directory-list">
          <RouterLink
            v-for="t in s.teams"
            :key="t.name"
            :to="'/teams/' + encodeURIComponent(t.name)"
            ><Icon name="network" /><span
              >{{ t.name }}<span v-if="t.name === mine" class="chip team-tag">Your Team</span
              ><small>{{ summary(t) }}</small></span
            ><Icon name="right"
          /></RouterLink>
        </div>
      </section>
      <div v-if="catalog.isSuccess.value && !catalog.data.value?.length" class="directory-empty">
        <Icon name="network" :size="30" />
        <h2>No Teams yet</h2>
        <p>A Team is the agents that work each stage of your studies.</p>
        <button class="button" :disabled="busy" @click="file?.click()">Upload a Team</button>
      </div>
    </template>
  </section>
</template>
<style src="./directory.css" />
<style scoped>
.team-notice {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  padding: 14px 18px;
  border-radius: 10px;
  background: var(--stage-experimentation-bg);
  font-size: 13px;
}
.team-notice div {
  flex: 1;
}
.team-notice b {
  display: block;
  font-weight: 500;
}
.team-notice span {
  color: var(--muted);
  font-size: 12px;
}
.team-section h2 {
  margin: 20px 0 0;
  font-size: 13px;
  font-weight: 500;
}
.team-tag {
  margin-left: 8px;
}
.research-detail > header .chip {
  font-size: 12px;
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
.team-versions .chip {
  margin-left: 6px;
}
</style>
