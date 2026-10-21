<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
import BackendDetailsDialog from '../components/BackendDetailsDialog.vue';
import { backendLabel } from '../lib/backends';
import AddComputerDialog from '../components/AddComputerDialog.vue';
import { api, ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useMembers, useRuntimes } from '../api/queries';
import { groupDaemons, type Daemon } from '../lib/machines';
import { timeAgo } from '../lib/studies';

const props = defineProps<{ machineId?: string }>();
const router = useRouter();
const client = useQueryClient();
const runtimes = useRuntimes();
const members = useMembers();
const daemons = computed(() => groupDaemons(runtimes.data.value ?? []));
const machine = computed(() => daemons.value.find((d) => d.id === props.machineId));
const sandboxFailure = computed(() => {
  const status = machine.value?.runtimes.find((r) => r.bases?.sandbox?.startsWith('failed'))?.bases
    .sandbox;
  if (!status) return '';
  const reason = status.slice('failed'.length).replace(/^:\s*/, '').trim();
  return reason ? `Can't run agents: ${reason}` : "Can't run agents";
});
const menuFor = ref('');
const backendDetails = ref('');
const dialog = ref<'' | 'add' | 'daemon' | 'rename'>('');
const renaming = ref<Daemon | null>(null);
const draft = ref('');
const error = ref('');
const removalError = ref<{ id: string; message: string } | null>(null);
const busy = ref(false);
let generation = 0;
watch(
  [workspaceId, () => props.machineId],
  () => {
    generation++;
    menuFor.value = '';
    backendDetails.value = '';
    dialog.value = '';
    renaming.value = null;
    draft.value = '';
    error.value = '';
    removalError.value = null;
    busy.value = false;
  },
  { flush: 'sync' },
);
watch(
  [dialog, () => renaming.value?.id],
  () => {
    generation++;
    error.value = '';
    removalError.value = null;
    busy.value = false;
  },
  { flush: 'sync' },
);
watch(
  menuFor,
  () => {
    removalError.value = null;
  },
  { flush: 'sync' },
);
onUnmounted(() => generation++);

function scope(d: Daemon) {
  const workspace = workspaceId.value;
  const visit = generation;
  const id = d.id;
  const ids = d.runtimes.map((r) => r.id);
  return {
    workspace,
    id,
    ids,
    current: () => generation === visit && workspaceId.value === workspace,
  };
}
const message = (e: unknown, action: string) =>
  e instanceof ApiError && e.status === 404
    ? `Only the machine’s owner can ${action} it.`
    : (e as Error).message;
const cli = (h: string) => ({ claude: '✳', codex: '◎' })[h] ?? h[0];
const cliName = backendLabel;
const owner = (id: string) => {
  const m = members.data.value?.find((x) => x.user_id === id);
  return m ? m.name || m.email : '';
};
const refresh = (workspace: string) =>
  client.invalidateQueries({ queryKey: keys.runtimes(workspace) });

function startRename(d: Daemon) {
  document.activeElement
    ?.closest('.menu-anchor')
    ?.querySelector<HTMLButtonElement>('[aria-haspopup]')
    ?.focus();
  menuFor.value = '';
  renaming.value = d;
  draft.value = d.name;
  error.value = '';
  dialog.value = 'rename';
}
async function rename() {
  const d = renaming.value;
  if (!d || busy.value) return;
  const own = scope(d);
  const name = draft.value.trim();
  busy.value = true;
  error.value = '';
  try {
    for (const id of own.ids) {
      if (!own.current()) return;
      await api('PATCH', `/api/runtimes/${id}`, { custom_name: name });
    }
    if (!own.current()) return;
    await refresh(own.workspace);
    if (own.current()) dialog.value = '';
  } catch (e) {
    if (!own.current()) return;
    error.value = message(e, 'rename');
    await refresh(own.workspace);
  } finally {
    if (own.current()) busy.value = false;
  }
}
async function remove(d: Daemon) {
  if (busy.value || d.online) return;
  menuFor.value = '';
  const own = scope(d);
  if (!confirm(`Remove ${d.name}? Its daemon registers again the next time it starts.`)) return;
  busy.value = true;
  try {
    for (const id of own.ids) {
      if (!own.current()) return;
      await api('DELETE', `/api/runtimes/${id}`);
    }
    if (!own.current()) return;
    await refresh(own.workspace);
    if (own.current() && props.machineId === own.id) await router.push('/machines');
  } catch (e) {
    if (!own.current()) return;
    removalError.value = { id: own.id, message: message(e, 'remove') };
    await refresh(own.workspace);
  } finally {
    if (own.current()) busy.value = false;
  }
}
async function toggleMenu(d: Daemon, event: MouseEvent) {
  const anchor = (event.currentTarget as HTMLElement).parentElement;
  menuFor.value = menuFor.value === d.id ? '' : d.id;
  if (menuFor.value) {
    await nextTick();
    anchor?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();
  }
}
function menuKey(event: KeyboardEvent) {
  const anchor = event.currentTarget as HTMLElement;
  if (event.key === 'Escape') {
    event.preventDefault();
    menuFor.value = '';
    anchor.querySelector<HTMLButtonElement>('[aria-haspopup]')?.focus();
  } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault();
    const items = [...anchor.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    const index =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : (current + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
    items[index]?.focus();
  }
}
</script>
<template>
  <section class="resources resource-machines" :class="{ 'machine-detail': machineId }">
    <div v-if="!machineId" class="resource-header-actions">
      <button class="button primary" @click="dialog = 'add'">
        <Icon name="plus" />Add a machine
      </button>
    </div>
    <p v-if="runtimes.error.value" class="inline-error machine-error" role="alert">
      {{ runtimes.error.value.message }}
    </p>
    <template v-if="machineId">
      <template v-if="machine">
        <header class="machine-detail-header">
          <div class="machine-rail">
            <nav class="machine-breadcrumb" aria-label="Machine breadcrumb">
              <RouterLink to="/machines">Machines</RouterLink><span aria-hidden="true">/</span
              ><span>{{ machine.name }}</span>
            </nav>
            <div class="machine-heading-row">
              <span class="machine-icon"><Icon name="monitor" :size="24" /></span>
              <div class="machine-heading-copy">
                <div class="machine-title">
                  <h1>{{ machine.name }}</h1>
                  <span class="machine-health" :class="{ online: machine.online }"
                    ><Icon name="network" :size="12" />{{
                      machine.online ? 'Online' : 'Offline'
                    }}</span
                  >
                </div>
                <p class="daemon-id" :title="machine.id">daemon {{ machine.id }}</p>
                <div class="machine-metadata">
                  <span
                    >{{ machine.runtimes.length }} runtime{{
                      machine.runtimes.length === 1 ? '' : 's'
                    }}</span
                  ><span>{{
                    machine.online
                      ? machine.activeRuns
                        ? `${machine.activeRuns} active run${machine.activeRuns === 1 ? '' : 's'}`
                        : 'All idle'
                      : '—'
                  }}</span
                  ><span v-if="owner(machine.ownerId)">{{ owner(machine.ownerId) }}</span
                  ><time :datetime="machine.lastSeen" :title="machine.lastSeen">{{
                    timeAgo(machine.lastSeen)
                  }}</time>
                </div>
              </div>
              <div class="machine-actions">
                <button class="button" @click="startRename(machine)">
                  <Icon name="edit" :size="14" />Rename machine
                </button>
                <button
                  class="button"
                  disabled
                  title="Read logs on the computer using Daemon instructions"
                >
                  <Icon name="file" :size="14" />View logs
                </button>
                <button
                  class="button"
                  disabled
                  title="Restart on the computer using Daemon instructions"
                >
                  <Icon name="refresh" :size="14" />Restart
                </button>
                <button
                  class="button stop-machine"
                  disabled
                  title="Stop on the computer using Daemon instructions"
                >
                  <Icon name="archive" :size="14" />Stop
                </button>
                <button
                  v-if="!machine.online"
                  class="button"
                  :disabled="busy"
                  @click="remove(machine)"
                >
                  Remove
                </button>
              </div>
            </div>
            <p v-if="sandboxFailure" class="inline-error" role="alert">{{ sandboxFailure }}</p>
            <p v-if="removalError?.id === machine.id" class="inline-error" role="alert">
              {{ removalError.message }}
            </p>
          </div>
        </header>
        <div class="machine-rail machine-runtime-section">
          <div class="runtime-heading">
            <div>
              <h2>Runtimes</h2>
              <p>Agent CLIs appear automatically when the daemon detects them.</p>
            </div>
            <button
              class="button primary"
              disabled
              title="Custom runtime configuration is not available in this version"
            >
              <Icon name="plus" :size="14" />Add custom runtime
            </button>
          </div>
          <div class="runtime-table-scroll">
            <table class="machine-runtime-table" aria-label="Runtimes">
              <thead>
                <tr>
                  <th>Runtime</th>
                  <th>Health</th>
                  <th>Active runs</th>
                  <th>Cost · 7d</th>
                  <th>CLI</th>
                  <th><span class="sr-only">Details</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in machine.runtimes" :key="r.id">
                  <td>
                    <span class="runtime-name"
                      ><i class="harness-icon" :class="r.harness" aria-hidden="true">{{
                        cli(r.harness)
                      }}</i
                      >{{ cliName(r.harness) }}<small>Built-in</small></span
                    >
                  </td>
                  <td>
                    <span class="machine-health" :class="{ online: r.status === 'online' }"
                      ><Icon name="network" :size="12" />{{
                        r.status === 'online'
                          ? 'Online'
                          : r.status === 'offline'
                            ? 'Offline'
                            : 'Not reported'
                      }}</span
                    >
                  </td>
                  <td>{{ r.active_runs ?? 'Not reported' }}</td>
                  <td class="unknown-metric" title="Seven-day runtime cost is not recorded">—</td>
                  <td class="runtime-version" :title="r.cli_version || 'Version not reported'">
                    {{ r.cli_version || 'Not reported' }}
                  </td>
                  <td>
                    <button
                      class="text-button"
                      aria-label="Backend details"
                      @click="backendDetails = r.harness"
                    >
                      Details
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="remote-limit">
            <p>
              Remote controls and custom runtimes are unavailable. Use Daemon instructions on the
              computer.
            </p>
            <button class="button" @click="dialog = 'daemon'">
              <Icon name="network" :size="14" />Daemon instructions
            </button>
          </div>
        </div>
      </template>
      <div v-else class="small-empty">
        <Icon name="monitor" :size="28" />
        <p>{{ runtimes.isPending.value ? 'Loading computer…' : 'Computer not found' }}</p>
        <RouterLink class="button" to="/machines">Back to Machines</RouterLink>
      </div>
    </template>
    <div v-else class="inset-content">
      <ul v-if="daemons.length" class="machine-rows">
        <li v-for="d in daemons" :key="d.id" class="machine-row" :class="{ offline: !d.online }">
          <RouterLink class="machine-link" :to="'/machines/' + encodeURIComponent(d.id)">
            <span class="square-icon"><Icon name="monitor" /></span>
            <div class="name">
              <b>{{ d.name }}</b
              ><small
                >daemon {{ d.id.slice(0, 8) }}…<template v-if="owner(d.ownerId)">
                  · {{ owner(d.ownerId) }}</template
                ></small
              >
              <p v-if="removalError?.id === d.id" class="inline-error" role="alert">
                {{ removalError.message }}
              </p>
            </div>
            <span class="status">{{ d.online ? 'Online' : 'Offline' }}</span>
            <span class="clis"
              ><small>{{ d.runtimes.length }} runtime{{ d.runtimes.length === 1 ? '' : 's' }}</small
              ><i
                v-for="r in d.runtimes"
                :key="r.id"
                :class="r.harness"
                :title="cliName(r.harness) + (r.cli_version ? ' ' + r.cli_version : '')"
                >{{ cli(r.harness) }}</i
              ></span
            >
            <small class="runs">{{
              !d.online
                ? '—'
                : d.activeRuns
                  ? `${d.activeRuns} run${d.activeRuns === 1 ? '' : 's'}`
                  : 'All idle'
            }}</small
            ><small class="seen">{{ timeAgo(d.lastSeen) }}</small>
          </RouterLink>
          <span class="menu-anchor" @keydown="menuKey"
            ><button
              class="icon-button"
              :disabled="busy"
              aria-label="Machine actions"
              aria-haspopup="menu"
              :aria-expanded="menuFor === d.id"
              @click="toggleMenu(d, $event)"
            >
              <Icon name="more" /></button
            ><span v-if="menuFor === d.id" class="row-menu" role="menu" aria-label="Machine actions"
              ><button role="menuitem" @click="startRename(d)">Rename…</button
              ><button v-if="!d.online" role="menuitem" class="danger" @click="remove(d)">
                Remove
              </button></span
            ></span
          >
        </li>
      </ul>
      <div v-else-if="runtimes.isSuccess.value" class="small-empty">
        <Icon name="monitor" :size="28" />
        <p>No computers connected</p>
        <small>Add a machine to run your agents.</small
        ><button class="button primary" @click="dialog = 'add'">
          <Icon name="plus" />Add a machine
        </button>
      </div>
    </div>
  </section>
  <AddComputerDialog v-if="dialog === 'add'" @close="dialog = ''" />
  <Modal v-if="dialog === 'daemon'" title="Daemon" @close="dialog = ''"
    ><div class="resource-form daemon-instructions">
      <h2>Daemon</h2>
      <p>
        On macOS or Linux, <code>lyk daemon start</code> starts it in the background;
        <code>lyk daemon status</code> and <code>lyk daemon logs</code> show what it is doing;
        <code>lyk daemon stop</code> stops it.
      </p>
      <p>
        On Windows, use <code>lyk.exe daemon run</code> and keep the terminal open. Read logs there;
        press Ctrl+C to stop.
      </p>
      <p>
        Use <code>./lyk</code> (macOS / Linux) or <code>.\lyk.exe</code> (Windows) when running a
        CLI built in the current directory.
      </p>
      <button class="button" @click="dialog = ''">Done</button>
    </div></Modal
  >
  <Modal v-if="dialog === 'rename'" title="Rename machine" @close="dialog = ''"
    ><form class="resource-form" @submit.prevent="rename">
      <h2>Rename machine</h2>
      <label>Name<input v-model="draft" aria-label="Name" maxlength="64" autofocus /></label>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <button class="button primary" :disabled="busy">Save</button>
    </form></Modal
  >
  <BackendDetailsDialog
    v-if="backendDetails"
    :key="workspaceId + backendDetails"
    :harness="backendDetails"
    :runtimes="machine?.runtimes"
    @close="backendDetails = ''"
  />
</template>
<style scoped src="./resources.css"></style>
<style scoped src="./machines.css"></style>
