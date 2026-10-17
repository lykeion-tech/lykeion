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

const client = useQueryClient();
const me = useMe();
const catalog = usePackCatalog();
const mine = computed(() => me.data.value?.pack_name ?? '');
const myTeam = computed(() => catalog.data.value?.find((t) => t.name === mine.value));
const versions = usePackVersions(mine);
const sections = computed(() => [
  {
    title: "Lykeion's Teams",
    note: '',
    teams: catalog.data.value?.filter((t) => t.source === 'shipped') ?? [],
  },
  {
    title: 'Your Teams',
    note: 'only you can use, approve or update these',
    teams: catalog.data.value?.filter((t) => t.source === 'uploaded') ?? [],
  },
]);
const pinned = computed(() =>
  (versions.data.value ?? []).reduce((n, v) => n + v.pinned_studies, 0),
);
// The version new studies use: the newest one this person approved.
const inUse = computed(() => versions.data.value?.filter((v) => v.approvals_by_me).at(-1)?.id);
// Your Team's current version, while you haven't approved it.
const waiting = computed(() => {
  const team = myTeam.value;
  const rows = versions.data.value;
  if (!team || !rows) return null;
  const row = rows.find((v) => v.version === String(team.version));
  return row?.approvals_by_me ? null : { team, row };
});
const open = ref(false);
const busy = ref(false);
const error = ref('');
const file = ref<HTMLInputElement>();
watch([workspaceId, mine], () => {
  open.value = false;
  error.value = '';
});

const agents = (t: Field) =>
  t.agents
    .filter((a) => a.roles.length)
    .map((a) => `${a.column[0]!.toUpperCase()}${a.column.slice(1)}: ${a.roles.join(', ')}`)
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
  <section class="resources">
    <Teleport to="#page-header-count">{{ catalog.data.value?.length ?? '' }}</Teleport>
    <Teleport to="#page-header-actions">
      <button class="button primary" :disabled="busy" @click="file?.click()">
        <Icon name="upload" :size="14" />Upload a Team
      </button>
      <input
        ref="file"
        type="file"
        accept=".zip,application/zip"
        hidden
        aria-hidden="true"
        @change="upload"
      />
    </Teleport>
    <div class="inset-content">
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
      <section
        v-for="(s, i) in sections"
        :key="s.title"
        class="team-section"
        :aria-labelledby="'teams-section-' + i"
      >
        <h2 :id="'teams-section-' + i">
          {{ s.title }}<small v-if="s.note"> · {{ s.note }}</small>
        </h2>
        <ul v-if="s.teams.length" class="machine-rows">
          <li
            v-for="t in s.teams"
            :key="t.name"
            class="machine-row"
            :class="{ 'your-team': t.name === mine }"
          >
            <div class="team-row">
              <span class="square-icon"><Icon name="flag" /></span>
              <div class="name">
                <b>{{ t.name }}</b
                ><small>{{ t.source === 'uploaded' ? 'Uploaded by you' : t.description }}</small>
                <span v-if="t.name === mine" class="chip">Your Team</span>
              </div>
              <span class="team-agents" :title="agents(t)">{{ agents(t) }}</span>
              <span class="team-starts">Starts from: {{ t.start_kinds.join(', ') }}</span>
              <span class="team-version">v{{ t.version }}</span>
              <button
                v-if="t.name === mine"
                class="button team-action"
                :aria-expanded="open"
                @click="open = !open"
              >
                {{ studies(pinned) }} ›
              </button>
              <button v-else class="button team-action" :disabled="busy" @click="makeMine(t)">
                Make my Team
              </button>
            </div>
            <table
              v-if="t.name === mine && open && versions.data.value"
              class="team-versions"
              :aria-label="t.name + ' versions'"
            >
              <tbody>
                <tr v-for="v in [...versions.data.value].reverse()" :key="v.id">
                  <td>
                    <b>v{{ v.version }}</b>
                    <code :title="v.sha256"
                      >{{ v.sha256.slice(0, 4) }}…{{ v.sha256.slice(-4) }}</code
                    >
                    <span v-if="v.id === inUse" class="chip">new studies</span>
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
          </li>
        </ul>
        <p v-else-if="catalog.isSuccess.value" class="team-empty">
          {{
            s.title === 'Your Teams'
              ? 'Upload a Team (.zip) with your own roles, skills and checks.'
              : 'Lykeion ships no Teams yet.'
          }}
        </p>
      </section>
    </div>
  </section>
</template>
<style scoped src="./resources.css"></style>
<style scoped src="./machines.css"></style>
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
.team-section h2 small {
  font-weight: 400;
  color: var(--muted);
  font-size: 13px;
}
.team-section .machine-rows {
  padding-top: 0;
}
.machine-row {
  flex-direction: column;
  align-items: stretch;
  padding: 0;
}
.machine-row.your-team {
  background: var(--stage-reporting-bg);
}
.team-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
}
.name .chip {
  margin-top: 4px;
}
.team-agents,
.team-starts,
.team-version,
.team-empty {
  color: var(--muted);
  font-size: 12.5px;
}
.team-agents {
  width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.team-starts {
  width: 150px;
}
.team-version {
  width: 70px;
  font-family: ui-monospace, monospace;
}
.team-action {
  width: 120px;
  justify-content: center;
}
.team-versions {
  margin: 0 18px 14px 76px;
  border-collapse: collapse;
  font-size: 12.5px;
}
.team-versions td {
  padding: 6px 16px 6px 0;
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
.team-empty {
  margin: 8px 0 0;
}
@media (max-width: 1100px) {
  .team-row {
    flex-wrap: wrap;
  }
  .team-agents {
    width: auto;
    flex-basis: 100%;
    order: 5;
  }
}
</style>
