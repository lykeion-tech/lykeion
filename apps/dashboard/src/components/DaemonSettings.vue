<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { desktopMachineID, desktopProfileReady, localPlaceholderID } from '../lib/machines';
import type { DaemonPreferences, DaemonSnapshot } from '../../../desktop/src/daemon-types';

const props = defineProps<{
  machineId: string;
  isOwner: boolean;
  accountId?: string;
  localProfile?: boolean;
}>();
const bridge = typeof window !== 'undefined' ? window.lykeionDesktop?.daemon : undefined;
const snapshot = ref<DaemonSnapshot | null>(null);
const preferences = ref<DaemonPreferences | null>(null);
const error = ref('');
const busy = ref(false);
let generation = 0;
let unsubscribe: (() => void) | undefined;
const matches = computed(
  () =>
    !!bridge &&
    props.isOwner &&
    desktopProfileReady(snapshot.value, props.accountId) &&
    !!snapshot.value &&
    (props.localProfile
      ? props.machineId === localPlaceholderID ||
        props.machineId === desktopMachineID(snapshot.value)
      : snapshot.value.daemonID === props.machineId),
);
const available = computed(() => matches.value && snapshot.value?.availability === 'available');
const managed = computed(() => available.value && !!snapshot.value?.cliAvailable);
const canControl = computed(
  () =>
    available.value &&
    !!snapshot.value?.cliAvailable &&
    snapshot.value.status !== 'unknown' &&
    !busy.value,
);
const stopped = computed(() => matches.value && snapshot.value?.status === 'stopped');
const explanation = computed(() => {
  if (!bridge) return '';
  if (snapshot.value?.availability === 'unavailable')
    return 'Daemon controls require the macOS or Linux desktop development preview.';
  if (!matches.value)
    return 'This machine is remote or managed outside this desktop app. Use Daemon instructions on that computer.';
  if (!snapshot.value?.cliAvailable)
    return 'Development lyk is missing. Run npm run desktop:dev to build it.';
  return '';
});

watch(
  [() => props.machineId, () => props.isOwner, () => props.accountId, () => props.localProfile],
  (_, __, onCleanup) => {
    const visit = ++generation;
    let revision = 0;
    let loadingPreferences = false;
    snapshot.value = null;
    preferences.value = null;
    error.value = '';
    busy.value = false;
    const current = () => visit === generation;
    const accept = (value: DaemonSnapshot) => {
      if (!current()) return;
      revision++;
      snapshot.value = value;
      if (!matches.value) {
        preferences.value = null;
        return;
      }
      if (!preferences.value && !loadingPreferences && available.value) {
        loadingPreferences = true;
        void bridge!
          .getPreferences()
          .then((prefs) => {
            if (current() && matches.value) preferences.value = prefs;
          })
          .catch(() => {
            if (current() && matches.value) error.value = 'Daemon preferences could not be loaded.';
          })
          .finally(() => {
            loadingPreferences = false;
          });
      }
    };
    if (bridge) {
      unsubscribe = bridge.onStatusChange(accept);
      const initialRevision = revision;
      void bridge
        .getSnapshot()
        .then((value) => {
          if (revision === initialRevision) accept(value);
        })
        .catch(() => {
          if (current()) error.value = 'Desktop daemon status could not be loaded.';
        });
    }
    onCleanup(() => {
      unsubscribe?.();
      unsubscribe = undefined;
    });
  },
  { immediate: true, flush: 'sync' },
);
onUnmounted(() => {
  generation++;
});

async function toggle(key: keyof DaemonPreferences) {
  if (
    !bridge ||
    !available.value ||
    !snapshot.value?.cliAvailable ||
    !preferences.value ||
    busy.value
  )
    return;
  const visit = generation;
  const id = props.machineId;
  busy.value = true;
  error.value = '';
  try {
    const saved = await bridge.setPreferences({
      ...preferences.value,
      [key]: !preferences.value[key],
    });
    if (visit === generation && matches.value && props.machineId === id) preferences.value = saved;
  } catch {
    if (visit === generation && matches.value)
      error.value = 'Daemon preferences could not be saved. Your previous settings are unchanged.';
  } finally {
    if (visit === generation) busy.value = false;
  }
}
async function lifecycle(action: 'start' | 'restart' | 'stop') {
  if (!bridge || !canControl.value || (action === 'stop' && stopped.value)) return;
  const visit = generation;
  busy.value = true;
  error.value = '';
  try {
    const value = await bridge[action]();
    if (visit === generation) snapshot.value = value;
  } catch {
    if (visit === generation)
      error.value = `The desktop daemon could not ${action === 'stop' ? 'be stopped' : action === 'start' ? 'be started' : 'be restarted'}. Try again.`;
  } finally {
    if (visit === generation) busy.value = false;
  }
}
function duration(seconds: number | null | undefined) {
  if (seconds == null) return '—';
  const total = Math.floor(seconds);
  return [
    total >= 3600 ? `${Math.floor(total / 3600)}h` : '',
    total >= 60 ? `${Math.floor(total / 60) % 60}m` : '',
    `${total % 60}s`,
  ]
    .filter(Boolean)
    .join(' ');
}
const diagnostics = computed(() => {
  const value = matches.value ? snapshot.value : null;
  return [
    [
      'State',
      value && value.status !== 'unknown'
        ? value.status[0]!.toUpperCase() + value.status.slice(1)
        : '—',
    ],
    ['Uptime', duration(value?.uptime)],
    ['PID', value?.pid ?? '—'],
    ['Daemon ID', value?.daemonID || '—'],
    ['Profile', value?.profile || '—'],
    ['Server URL', value?.serverURL || '—'],
    ['Device name', value?.deviceName || '—'],
    ['Workspaces', value?.workspaceCount ?? '—'],
  ];
});
defineExpose({
  managed,
  canControl,
  stopped,
  start: () => lifecycle('start'),
  restart: () => lifecycle('restart'),
  stop: () => lifecycle('stop'),
});
</script>
<template>
  <section class="daemon-settings" aria-labelledby="daemon-settings-heading">
    <h2 id="daemon-settings-heading">Daemon</h2>
    <p class="daemon-intro">Manage the daemon that runs your agents on this computer.</p>
    <p v-if="explanation" class="daemon-explanation">{{ explanation }}</p>
    <p v-if="error || (matches && snapshot?.error)" class="inline-error" role="alert">
      {{ error || snapshot?.error }}
    </p>
    <div class="daemon-preferences">
      <div
        v-for="setting in [
          {
            key: 'autoStart',
            label: 'Auto-start on launch',
            description: 'Start the daemon after you sign in to the desktop app.',
          },
          {
            key: 'autoStop',
            label: 'Auto-stop on quit',
            description: 'Stop the daemon when you quit the desktop app.',
          },
        ] as const"
        :key="setting.key"
        class="daemon-preference-row"
      >
        <div>
          <span>{{ setting.label }}</span
          ><small>{{ setting.description }}</small>
        </div>
        <button
          class="daemon-switch"
          type="button"
          role="switch"
          :aria-label="setting.label"
          :aria-checked="!!preferences?.[setting.key]"
          :disabled="!available || !snapshot?.cliAvailable || !preferences || busy"
          @click="toggle(setting.key)"
        >
          <span />
        </button>
      </div>
      <div class="daemon-preference-row">
        <span>CLI Status</span
        ><span class="daemon-cli">{{
          matches ? (snapshot?.cliAvailable ? 'Available' : 'Missing') : '—'
        }}</span>
      </div>
    </div>
    <h3>Diagnostics</h3>
    <dl class="daemon-diagnostics">
      <div v-for="[label, value] in diagnostics" :key="label" class="daemon-diagnostic-row">
        <dt>{{ label }}</dt>
        <dd>{{ value }}</dd>
      </div>
    </dl>
  </section>
</template>
<style scoped>
.daemon-settings {
  margin-top: 24px;
}
h2 {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 6px;
}
.daemon-intro,
.daemon-explanation {
  font-size: 12px;
  color: var(--muted);
  line-height: 18px;
  margin: 0 0 16px;
}
.daemon-explanation {
  margin-top: 16px;
}
.daemon-preferences,
.daemon-diagnostics {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--input);
  overflow: hidden;
}
.daemon-preference-row {
  min-height: 64px;
  padding: 14px 16px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 20px;
  font-size: 14px;
}
.daemon-preference-row + .daemon-preference-row,
.daemon-diagnostic-row + .daemon-diagnostic-row {
  border-top: 1px solid var(--border);
}
.daemon-preference-row small {
  display: block;
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
  margin-top: 4px;
}
.daemon-cli {
  color: var(--muted);
  font-size: 12px;
}
h3 {
  font-size: 14px;
  font-weight: 600;
  margin: 24px 0 16px;
}
.daemon-diagnostics {
  margin: 0;
}
.daemon-diagnostic-row {
  padding: 12px 16px;
  display: flex;
  justify-content: space-between;
  gap: 24px;
  font-size: 12px;
  line-height: 18px;
}
dt {
  color: var(--muted);
  flex-shrink: 0;
}
dd {
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
  font-family: var(--font-mono, ui-monospace, monospace);
}
.daemon-switch {
  width: 32px;
  height: 18px;
  border: 0;
  border-radius: 999px;
  padding: 2px;
  flex-shrink: 0;
  background: var(--muted);
  cursor: pointer;
  display: flex;
  align-items: center;
}
.daemon-switch[aria-checked='true'] {
  background: var(--primary);
}
.daemon-switch span {
  display: block;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #fff;
  transform: translateX(0);
  transition: transform 0.12s;
}
.daemon-switch[aria-checked='true'] span {
  transform: translateX(14px);
}
.daemon-switch:disabled {
  opacity: 0.45;
  cursor: default;
}
.daemon-switch:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .daemon-switch span {
    transition: none;
  }
}
</style>
