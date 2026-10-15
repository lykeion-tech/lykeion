<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
import { api, ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useMembers, useRuntimes } from '../api/queries';
import { groupDaemons, type Daemon } from '../lib/machines';
import { timeAgo } from '../lib/studies';
import { notify } from '../state/notice';

const client = useQueryClient();
const runtimes = useRuntimes();
const members = useMembers();
const daemons = computed(() => groupDaemons(runtimes.data.value ?? []));
const menuFor = ref('');
const dialog = ref<'' | 'add' | 'daemon' | 'rename'>('');
const renaming = ref<Daemon | null>(null);
const draft = ref('');
const error = ref('');
const busy = ref(false);
let generation = 0;
watch(
  workspaceId,
  () => {
    generation++;
    menuFor.value = '';
    dialog.value = '';
    renaming.value = null;
    draft.value = '';
    error.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
watch(
  [dialog, () => renaming.value?.id],
  () => {
    generation++;
    error.value = '';
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
const cli = (h: string) => ({ claude: '✳', codex: '◎' })[h] ?? h[0];
const cliName = (h: string) => ({ claude: 'Claude Code', codex: 'Codex' })[h] ?? h;
const owner = (id: string) => {
  const m = members.data.value?.find((x) => x.user_id === id);
  return m ? m.name || m.email : '';
};
const refresh = (workspace: string) =>
  client.invalidateQueries({ queryKey: keys.runtimes(workspace) });

function startRename(d: Daemon) {
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
    if (own.current()) await refresh(own.workspace);
  } catch (e) {
    if (!own.current()) return;
    notify(message(e, 'remove'));
    await refresh(own.workspace);
  } finally {
    if (own.current()) busy.value = false;
  }
}
</script>
<template>
  <section class="resources resource-machines">
    <div class="resource-header-actions">
      <button class="button" @click="dialog = 'daemon'"><Icon name="network" />Daemon</button>
      <button class="button primary" @click="dialog = 'add'">
        <Icon name="plus" />Add a computer
      </button>
    </div>
    <div class="inset-content">
      <p v-if="runtimes.error.value" class="inline-error" role="alert">
        {{ runtimes.error.value.message }}
      </p>
      <ul v-if="daemons.length" class="machine-rows">
        <li v-for="d in daemons" :key="d.id" class="machine-row" :class="{ offline: !d.online }">
          <span class="square-icon"><Icon name="monitor" /></span>
          <span class="name"
            ><b>{{ d.name }}</b
            ><small
              >daemon {{ d.id.slice(0, 8) }}…<template v-if="owner(d.ownerId)">
                · {{ owner(d.ownerId) }}</template
              ></small
            ></span
          >
          <span class="status">{{ d.online ? 'Online' : 'Offline' }}</span>
          <span class="clis"
            ><small>{{ d.runtimes.length }} runtime{{ d.runtimes.length === 1 ? '' : 's' }}</small
            ><i
              v-for="r in d.runtimes"
              :key="r.id"
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
          }}</small>
          <small class="seen">{{ timeAgo(d.lastSeen) }}</small>
          <span class="menu-anchor">
            <button
              class="icon-button"
              :disabled="busy"
              aria-label="Machine actions"
              @click="menuFor = menuFor === d.id ? '' : d.id"
            >
              <Icon name="right" />
            </button>
            <span v-if="menuFor === d.id" class="row-menu" role="menu">
              <button role="menuitem" @click="startRename(d)">Rename…</button>
              <button v-if="!d.online" role="menuitem" class="danger" @click="remove(d)">
                Remove
              </button>
            </span>
          </span>
        </li>
      </ul>
      <div v-else-if="runtimes.isSuccess.value" class="small-empty">
        <Icon name="monitor" :size="28" />
        <p>No computers connected</p>
        <small>Add a computer to run your agents.</small>
        <button class="button primary" @click="dialog = 'add'">
          <Icon name="plus" />Add a computer
        </button>
      </div>
    </div>
  </section>
  <Modal v-if="dialog === 'add'" title="Add a computer" @close="dialog = ''">
    <div class="resource-form">
      <h2>Add a computer</h2>
      <p>On the computer whose agent CLIs (Claude Code, Codex) should run your studies:</p>
      <p><code>lyk login</code> signs the CLI in to this server.</p>
      <p>
        <code>lyk daemon start</code> starts the daemon; it registers each CLI it finds as a runtime
        here.
      </p>
      <button class="button" @click="dialog = ''">Done</button>
    </div>
  </Modal>
  <Modal v-if="dialog === 'daemon'" title="Daemon" @close="dialog = ''">
    <div class="resource-form">
      <h2>Daemon</h2>
      <p>
        <code>lyk daemon start</code> starts it in the background;
        <code>lyk daemon status</code> and <code>lyk daemon logs</code> show what it is doing;
        <code>lyk daemon stop</code> stops it.
      </p>
      <button class="button" @click="dialog = ''">Done</button>
    </div>
  </Modal>
  <Modal v-if="dialog === 'rename'" title="Rename machine" @close="dialog = ''">
    <form class="resource-form" @submit.prevent="rename">
      <h2>Rename machine</h2>
      <label>Name<input v-model="draft" aria-label="Name" maxlength="64" autofocus /></label>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <button class="button primary" :disabled="busy">Save</button>
    </form>
  </Modal>
</template>
<style scoped src="./resources.css"></style>
<style scoped>
.machine-rows {
  list-style: none;
  padding: 20px 0 0;
  margin: 0 auto;
  max-width: 1000px;
}
.machine-row {
  display: flex;
  align-items: center;
  gap: 14px;
  background: var(--input);
  border-radius: 10px;
  padding: 12px 18px;
  margin-top: 12px;
  font-size: 13px;
}
.machine-row.offline {
  opacity: 0.75;
}
.name {
  flex: 1;
}
.name b {
  display: block;
  font-weight: 500;
}
.name small,
.seen,
.clis small {
  color: var(--muted);
  font-size: 12px;
}
.status {
  width: 120px;
}
.machine-row:not(.offline) .status {
  color: var(--stage-validation-dot);
}
.clis {
  width: 150px;
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}
.clis small {
  width: 100%;
}
.clis i {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  background: var(--surface);
  display: inline-grid;
  place-items: center;
  font-size: 10px;
  font-style: normal;
}
.seen {
  width: 80px;
}
.runs {
  width: 120px;
  color: var(--muted);
  font-size: 12px;
}
.row-menu {
  position: absolute;
  right: 0;
  top: 100%;
  z-index: 5;
  background: var(--fill-menu);
  border-radius: 8px;
  padding: 4px;
  width: 160px;
  box-shadow: 0 10px 30px #0003;
}
.machine-row:has(.row-menu) {
  position: relative;
  z-index: 6;
}
.row-menu button {
  display: block;
  width: 100%;
  text-align: left;
  padding: 6px 9px;
  border-radius: 5px;
}
.row-menu .danger {
  color: var(--error-text);
}
code {
  font-family: 'JetBrains Mono', ui-monospace, monospace;
  font-size: 12px;
  background: var(--input);
  padding: 1px 5px;
  border-radius: 4px;
}
</style>
