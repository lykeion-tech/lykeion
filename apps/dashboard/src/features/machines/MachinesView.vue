<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import Icon from '../../ui/Icon.vue';
import CliIcon from '../coresearch/CliIcon.vue';
import Modal from '../../ui/Modal.vue';
import MachineSharing from './MachineSharing.vue';
import DaemonSettings from './DaemonSettings.vue';
import LocalSetupCheck from './LocalSetupCheck.vue';
import { useMe } from '../../api/queries';
import { backendLabel } from '../../lib/backends';
import AddComputerDialog from './AddComputerDialog.vue';
import { api, ApiError } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useMembers, useRuntimes } from '../../api/queries';
import {
  desktopMachineID,
  desktopProfileMachine,
  desktopProfileReady,
  groupDaemons,
  localPlaceholderID,
  type Daemon,
} from './machines';
import type { DaemonSnapshot } from '../../../../desktop/src/daemon-types';
import { timeAgo } from '../../lib/studies';

const props = defineProps<{ machineId?: string; embedded?: boolean }>();
const router = useRouter();
const client = useQueryClient();
const runtimes = useRuntimes();
const me = useMe();
const members = useMembers();
const nativeSnapshot = ref<DaemonSnapshot | null>(null);
const bridge = typeof window !== 'undefined' ? window.lykeionDesktop?.daemon : undefined;
let active = true;
let revision = 0;
const acceptSnapshot = (value: DaemonSnapshot) => {
  if (!active) return;
  revision++;
  nativeSnapshot.value = value;
};
const unsubscribe = bridge?.onStatusChange(acceptSnapshot);
if (bridge) {
  const initialRevision = revision;
  void bridge
    .getSnapshot()
    .then((value) => {
      if (revision === initialRevision) acceptSnapshot(value);
    })
    .catch(() => {});
}
onUnmounted(() => {
  active = false;
  unsubscribe?.();
});
const registeredDaemons = computed(() => groupDaemons(runtimes.data.value ?? []));
const registeredLocal = computed(() =>
  desktopProfileReady(nativeSnapshot.value, me.data.value?.id)
    ? registeredDaemons.value.find(
        (d) => d.id === nativeSnapshot.value?.daemonID && d.ownerId === me.data.value?.id,
      )
    : undefined,
);
const localProfile = computed(() =>
  desktopProfileMachine(registeredDaemons.value, nativeSnapshot.value, me.data.value?.id),
);
const daemons = computed(() =>
  localProfile.value ? [localProfile.value, ...registeredDaemons.value] : registeredDaemons.value,
);
const localRoute = computed(
  () =>
    router.currentRoute.value.query.desktopProfile === '1' &&
    !!nativeSnapshot.value &&
    desktopProfileReady(nativeSnapshot.value, me.data.value?.id) &&
    (props.machineId === localPlaceholderID ||
      props.machineId === desktopMachineID(nativeSnapshot.value)),
);
const machine = computed(() =>
  localRoute.value
    ? (registeredLocal.value ?? localProfile.value)
    : registeredDaemons.value.find((d) => d.id === props.machineId),
);
const signInProblems = computed(() => {
  const local = registeredLocal.value;
  if (!local || machine.value !== local || nativeSnapshot.value?.status !== 'running') return [];
  return local.runtimes
    .filter(
      (r) =>
        r.owner_id === me.data.value?.id &&
        r.workspace_id === workspaceId.value &&
        r.status === 'online' &&
        r.last_seen_at === local.lastSeen &&
        r.unhealthy_reason === 'sign_in',
    )
    .map((r) => r.harness);
});
watch(
  [registeredLocal, localRoute],
  ([local, selected]) => {
    if (local && selected)
      void router.replace({
        path: '/settings',
        query: {
          ...router.currentRoute.value.query,
          tab: 'machines',
          machine: local.id,
          desktopProfile: undefined,
        },
      });
  },
  { immediate: true },
);
const isOwner = computed(() => !!me.data.value?.id && machine.value?.ownerId === me.data.value.id);
const sandboxFailure = computed(() => {
  const current = machine.value;
  if (!current) return '';
  // Omitted CLI rows keep old bases; use only the latest machine report.
  const status = current.runtimes.find(
    (r) => r.last_seen_at === current.lastSeen && r.bases?.sandbox?.startsWith('failed'),
  )?.bases.sandbox;
  if (!status) return '';
  const reason = status.slice('failed'.length).replace(/^:\s*/, '').trim();
  return reason ? `Can't run agents: ${reason}` : "Can't run agents";
});
// The daemon's reason follows this prefix: either what to install on this
// machine, or that this version cannot set up new environments.
const unavailablePrefix = 'failed: bounded scientific environment setup is unavailable on this machine';
const unavailableReason = (state: string | undefined) => {
  if (!state?.startsWith(unavailablePrefix)) return undefined;
  const rest = state.slice(unavailablePrefix.length);
  return rest.startsWith(': ') ? rest.slice(2) : '';
};
const unavailableBases = computed(() => {
  const current = machine.value;
  if (!current || !current.online || !isOwner.value || !workspaceId.value) return [];
  const reports = current.runtimes.filter(
    (r) =>
      r.owner_id === me.data.value?.id &&
      r.workspace_id === workspaceId.value &&
      r.status === 'online' &&
      r.last_seen_at === current.lastSeen,
  );
  if (!reports.length) return [];
  return (
    [
      ['python', 'Python'],
      ['r', 'R'],
      ['julia', 'Julia'],
    ] as const
  )
    .filter(([key]) => reports.every((r) => unavailableReason(r.bases?.[key]) !== undefined))
    .map(([key, label]) => ({ label, fix: unavailableReason(reports[0].bases?.[key]) }));
});
const unavailableFix = computed(() => unavailableBases.value.find((b) => b.fix)?.fix ?? '');
const daemonSettings = ref<InstanceType<typeof DaemonSettings>>();
const sharingDetails = ref(false);
const sharingForm = ref<InstanceType<typeof MachineSharing>>();
const dialog = ref<'' | 'add' | 'daemon' | 'rename'>('');
defineExpose({
  openAdd: () => {
    dialog.value = 'add';
  },
});
const renaming = ref<Daemon | null>(null);
const draft = ref('');
const error = ref('');
const removalError = ref<{ id: string; message: string } | null>(null);
const busy = ref(false);
let generation = 0;
watch(
  [workspaceId, () => props.machineId, () => router.currentRoute.value.query.desktopProfile],
  () => {
    generation++;
    sharingDetails.value = false;
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
const cliName = backendLabel;
const runtimeProblem = (reason: string) => {
  const messages: Record<string, string> = {
    cli_missing: 'Agent CLI is missing',
    cli_too_old: 'Agent CLI is too old',
    cli_broken: 'Agent CLI stopped working',
    provider_quota: 'Provider usage limit reached',
    sign_in: 'Agent CLI needs sign-in',
    sandbox: 'Sandbox could not start',
    door: 'Connection to Lykeion failed',
    fence_breach: 'Evidence isolation failed',
  };
  return messages[reason] ?? 'Runtime check failed';
};
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
    if (own.current() && props.machineId === own.id)
      await router.push({ path: '/settings', query: { tab: 'machines' } });
  } catch (e) {
    if (!own.current()) return;
    removalError.value = { id: own.id, message: message(e, 'remove') };
    await refresh(own.workspace);
  } finally {
    if (own.current()) busy.value = false;
  }
}
</script>
<template>
  <section
    class="resources resource-machines"
    :class="{ 'machine-detail': machineId, 'settings-machines': embedded }"
  >
    <div v-if="!machineId && !embedded" class="resource-header-actions">
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
            <div class="machine-heading-row">
              <span class="machine-icon"><Icon name="monitor" :size="24" /></span>
              <div class="machine-heading-copy">
                <div class="machine-title">
                  <h1>{{ machine.name }}</h1>
                  <span class="machine-health" :class="{ online: machine.online }"
                    ><Icon name="network" :size="12" />{{
                      machine.localProfile ? '—' : machine.online ? 'Online' : 'Offline'
                    }}</span
                  >
                </div>
                <div class="machine-metadata">
                  <span v-if="!machine.localProfile"
                    >{{ machine.runtimes.length }} runtime{{
                      machine.runtimes.length === 1 ? '' : 's'
                    }}</span
                  ><span v-if="owner(machine.ownerId)">{{ owner(machine.ownerId) }}</span>
                  <span v-if="machine.online && machine.activeRuns">
                    {{ machine.activeRuns }} active run{{ machine.activeRuns === 1 ? '' : 's' }}
                  </span>
                </div>
              </div>
              <div class="machine-actions">
                <button v-if="!machine.localProfile" class="button" @click="startRename(machine)">
                  <Icon name="edit" :size="14" />Rename
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
                  :disabled="!daemonSettings?.canControl"
                  :title="
                    daemonSettings?.canControl
                      ? undefined
                      : 'Restart on the computer using Daemon instructions'
                  "
                  @click="
                    machine.localProfile && daemonSettings?.stopped
                      ? daemonSettings?.start()
                      : daemonSettings?.restart()
                  "
                >
                  <Icon
                    :name="machine.localProfile && daemonSettings?.stopped ? 'play' : 'refresh'"
                    :size="14"
                  />{{ machine.localProfile && daemonSettings?.stopped ? 'Start' : 'Restart' }}
                </button>
                <button
                  class="button stop-machine"
                  :disabled="!daemonSettings?.canControl || daemonSettings?.stopped"
                  :title="
                    daemonSettings?.canControl
                      ? undefined
                      : 'Stop on the computer using Daemon instructions'
                  "
                  @click="daemonSettings?.stop()"
                >
                  <Icon name="archive" :size="14" />Stop
                </button>
                <button
                  v-if="!machine.localProfile && !machine.online"
                  class="button"
                  :disabled="busy"
                  @click="remove(machine)"
                >
                  Remove
                </button>
              </div>
            </div>
            <p v-if="sandboxFailure" class="inline-error" role="alert">{{ sandboxFailure }}</p>
            <p v-if="unavailableBases.length" class="inline-error" role="status">
              Shared scientific setup for
              {{ unavailableBases.map((b) => b.label).join(', ') }} is unavailable on this machine.
              <template v-if="unavailableFix">To set up new ones, {{ unavailableFix }}.</template>
              <template v-else
                >Existing prepared environments may remain usable; a supported update is required to
                set up new ones.</template
              >
            </p>
            <p v-if="removalError?.id === machine.id" class="inline-error" role="alert">
              {{ removalError.message }}
            </p>
          </div>
        </header>
        <div class="machine-rail machine-runtime-section">
          <div class="runtime-heading">
            <div>
              <h2>Runtimes</h2>
              <p>Claude Code and Codex are supported. Agent CLIs appear automatically when the daemon detects them.</p>
            </div>
          </div>
          <div class="runtime-table-scroll">
            <table class="machine-runtime-table" aria-label="Runtimes">
              <thead>
                <tr>
                  <th>Runtime</th>
                  <th>Health</th>
                  <th>Active runs</th>
                  <th>CLI</th>
                  <th>Colleagues</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in machine.runtimes" :key="r.id">
                  <td>
                    <span class="runtime-name"
                      ><i class="harness-icon" :class="r.harness" aria-hidden="true"
                        ><CliIcon
                          v-if="r.harness === 'claude' || r.harness === 'codex'"
                          :cli="r.harness"
                        /><template v-else>{{ r.harness[0] }}</template></i
                      >{{ cliName(r.harness) }}</span
                    >
                  </td>
                  <td>
                    <span
                      class="machine-health"
                      :class="{ online: r.status === 'online' && !r.unhealthy_reason }"
                      ><Icon name="network" :size="12" />{{
                        r.unhealthy_reason
                          ? `${r.status === 'offline' ? 'Offline · ' : ''}Needs attention: ${runtimeProblem(r.unhealthy_reason)}`
                          : r.status === 'online'
                            ? 'Online'
                            : r.status === 'offline'
                              ? 'Offline'
                              : 'Not reported'
                      }}</span
                    >
                  </td>
                  <td>{{ r.active_runs ?? 'Not reported' }}</td>
                  <td class="runtime-version" :title="r.cli_version || 'Version not reported'">
                    {{ r.cli_version || 'Not reported' }}
                  </td>
                  <td>
                    <button
                      class="text-button"
                      :aria-label="`Sharing details for ${cliName(r.harness)}`"
                      :disabled="!isOwner"
                      :title="isOwner ? undefined : 'Only the machine owner can change sharing'"
                      @click="sharingDetails = true"
                    >
                      Details
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="!machine.online && !machine.localProfile" class="remote-limit">
            Check the daemon on this computer using Daemon instructions.
          </p>
          <p v-else-if="machine.runtimes.some((r) => r.unhealthy_reason)" class="remote-limit">
            Check daemon diagnostics below and use Daemon instructions on the computer to investigate.
            <button class="text-button" @click="daemonSettings?.$el.scrollIntoView()">View diagnostics</button>
          </p>
          <div class="remote-limit">
            <p>
              {{
                daemonSettings?.managed
                  ? 'Custom runtimes are unavailable. Use Daemon instructions for terminal commands.'
                  : 'Remote controls and custom runtimes are unavailable. Use Daemon instructions on the computer.'
              }}
            </p>
            <button class="button" @click="dialog = 'daemon'">
              <Icon name="network" :size="14" />Daemon instructions
            </button>
          </div>
          <DaemonSettings
            ref="daemonSettings"
            :machine-id="machine.id"
            :is-owner="isOwner"
            :account-id="me.data.value?.id"
            :local-profile="machine.localProfile"
            :sign-in-problems="signInProblems"
          />
        </div>
      </template>
      <div v-else class="small-empty">
        <Icon name="monitor" :size="28" />
        <p>{{ runtimes.isPending.value ? 'Loading computer…' : 'Computer not found' }}</p>
        <RouterLink class="button" :to="{ path: '/settings', query: { tab: 'machines' } }"
          >Back to Machines</RouterLink
        >
      </div>
    </template>
    <div v-else class="inset-content">
      <section
        v-if="
          me.data.value?.id &&
          nativeSnapshot?.availability === 'available' &&
          !registeredLocal &&
          bridge
        "
        class="tool-setup"
        aria-labelledby="local-setup-heading"
      >
        <h3 id="local-setup-heading">Set up this computer</h3>
        <LocalSetupCheck :account-id="me.data.value.id" />
      </section>
      <ul v-if="daemons.length" class="machine-rows">
        <li
          v-for="d in daemons"
          :key="`${d.localProfile ? 'desktop' : 'registered'}:${d.id}`"
          class="machine-row"
          :class="{ offline: !d.online }"
        >
          <RouterLink
            class="machine-link"
            :to="{
              path: '/settings',
              query: {
                tab: 'machines',
                machine: d.id,
                ...(d.localProfile ? { desktopProfile: '1' } : {}),
              },
            }"
          >
            <span class="square-icon"><Icon name="monitor" /></span>
            <div class="name">
              <b>{{ d.name }}</b
              ><small v-if="owner(d.ownerId)">{{ owner(d.ownerId) }}</small>
              <p v-if="removalError?.id === d.id" class="inline-error" role="alert">
                {{ removalError.message }}
              </p>
            </div>
            <span class="status">{{ d.localProfile ? '—' : d.online ? 'Online' : 'Offline' }}</span>
            <span class="clis"
              ><small v-if="d.localProfile">—</small
              ><small v-else
                >{{ d.runtimes.length }} runtime{{ d.runtimes.length === 1 ? '' : 's' }}</small
              ><i
                v-for="r in d.runtimes"
                :key="r.id"
                :class="r.harness"
                :title="cliName(r.harness) + (r.cli_version ? ' ' + r.cli_version : '')"
                ><CliIcon
                  v-if="r.harness === 'claude' || r.harness === 'codex'"
                  :cli="r.harness"
                /><template v-else>{{ r.harness[0] }}</template></i
              ></span
            >
            <small class="runs">{{
              !d.online
                ? '—'
                : d.activeRuns
                  ? `${d.activeRuns} run${d.activeRuns === 1 ? '' : 's'}`
                  : 'All idle'
            }}</small
            ><small class="seen">{{ d.localProfile ? '—' : timeAgo(d.lastSeen) }}</small>
          </RouterLink>
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
      <div class="machine-rename-actions">
        <button class="button primary" :disabled="busy">Save</button>
      </div>
    </form></Modal
  >
  <Modal
    v-if="sharingDetails && machine && isOwner"
    v-slot="{ close }"
    title="Details"
    :before-close="() => sharingForm?.canClose() ?? true"
    @close="sharingDetails = false"
  >
    <div class="machine-sharing-dialog">
      <header class="runtime-heading">
        <h2>Details</h2>
        <button class="icon-button" aria-label="Close" @click="close">
          <Icon name="close" />
        </button>
      </header>
      <MachineSharing
        ref="sharingForm"
        :machine-id="machine.id"
        :runtimes="machine.runtimes"
        hide-heading
      />
    </div>
  </Modal>
</template>
<style scoped src="../../ui/resources.css"></style>
<style scoped src="./machines.css"></style>
