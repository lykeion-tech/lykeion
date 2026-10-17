<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import { ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import {
  approvePack,
  uploadPack,
  usePackCatalog,
  usePackVersions,
} from '../api/queries';
import type { Field } from '../api/types';

const props = defineProps<{ name?: string }>();
const client = useQueryClient();
const catalog = usePackCatalog();
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
const busy = ref(false);
const error = ref('');
const file = ref<HTMLInputElement>();
watch([workspaceId, () => props.name], () => {
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
    <template v-if="name">
      <RouterLink to="/teams" class="text-button">← Teams</RouterLink>
      <div v-if="opened" class="research-detail">
        <header>
          <h1>{{ opened.name }}</h1>
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
              >{{ t.name }}<small>{{ summary(t) }}</small></span
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
.team-section h2 {
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
