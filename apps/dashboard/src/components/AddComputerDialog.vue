<script setup lang="ts">
/* global __LYK_PUBLIC_API_ORIGIN__ */
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
import MachineCommand from './MachineCommand.vue';
import { useRuntimes } from '../api/queries';
import { groupDaemons } from '../lib/machines';

const emit = defineEmits<{ close: [] }>();
const runtimes = useRuntimes();
const platform = ref<'unix' | 'windows'>('unix');
const executable = computed(() => (platform.value === 'windows' ? '.\\lyk.exe' : './lyk'));
const build = computed(
  () => `go build -o ${platform.value === 'windows' ? 'lyk.exe' : 'lyk'} ./cmd/lyk`,
);
const server = import.meta.env.DEV ? __LYK_PUBLIC_API_ORIGIN__ : window.location.origin;
const start = computed(
  () =>
    `${executable.value} login --server ${server} --email you@lab.org\n${executable.value} daemon ${platform.value === 'windows' ? 'run' : 'start'}`,
);
const daemons = computed(() => groupDaemons(runtimes.data.value ?? []));
const detected = computed(() =>
  baseline.value ? daemons.value.find((d) => d.online && !baseline.value!.has(d.id)) : undefined,
);
const baseline = ref<Set<string> | null>(null);
let live = true;
let timer: ReturnType<typeof setInterval> | undefined;
async function poll() {
  const result = await runtimes.refetch({ cancelRefetch: false });
  if (live && !baseline.value && result.isSuccess && !result.isFetching)
    baseline.value = new Set(
      groupDaemons(result.data ?? [])
        .filter((d) => d.online)
        .map((d) => d.id),
    );
}
async function platformKey(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const tabs = event.currentTarget as HTMLElement;
  platform.value =
    event.key === 'Home'
      ? 'unix'
      : event.key === 'End'
        ? 'windows'
        : platform.value === 'unix'
          ? 'windows'
          : 'unix';
  await nextTick();
  tabs.querySelector<HTMLButtonElement>('[aria-selected="true"]')?.focus();
}
onMounted(() => {
  void poll();
  timer = setInterval(() => void poll(), 3000);
});
onUnmounted(() => {
  live = false;
  clearInterval(timer);
});
</script>
<template>
  <Modal title="Add a computer" class="add-computer-modal" @close="emit('close')">
    <div class="add-computer">
      <button
        class="icon-button close-add"
        aria-label="Close Add a computer"
        @click="emit('close')"
      >
        <Icon name="close" />
      </button>
      <h2>Add a computer</h2>
      <p class="intro">
        Run these commands on the computer you want to add. We’ll detect it when the daemon comes
        online.
      </p>
      <div class="setup-step">
        <h3>1. Build the Lykeion CLI</h3>
        <div class="platforms" role="tablist" aria-label="Computer platform" @keydown="platformKey">
          <button
            id="machine-unix-tab"
            role="tab"
            :aria-selected="platform === 'unix'"
            :tabindex="platform === 'unix' ? 0 : -1"
            aria-controls="machine-commands"
            @click="platform = 'unix'"
          >
            macOS / Linux
          </button>
          <button
            id="machine-windows-tab"
            role="tab"
            :aria-selected="platform === 'windows'"
            :tabindex="platform === 'windows' ? 0 : -1"
            aria-controls="machine-commands"
            @click="platform = 'windows'"
          >
            Windows
          </button>
        </div>
        <p class="hint">
          Requires Go 1.26 and a Lykeion source checkout. Run from its
          <code>server/</code> directory.
        </p>
        <div
          id="machine-commands"
          role="tabpanel"
          :aria-labelledby="platform === 'unix' ? 'machine-unix-tab' : 'machine-windows-tab'"
        >
          <MachineCommand :command="build" label="Copy build command" />
          <h3 class="second-step">2. Sign in and start the daemon</h3>
          <MachineCommand :command="start" label="Copy start commands" />
        </div>
        <p class="hint">
          Replace <code>you@lab.org</code> with your email and enter the emailed code.
          <template v-if="platform === 'windows'"
            >Keep this terminal open; background control requires macOS or Linux.</template
          ><template v-else>The daemon then runs in the background.</template>
        </p>
      </div>
      <div class="connection-status" role="status">
        <span class="health-dot" />{{
          detected
            ? `Connected: ${detected.name}`
            : baseline
              ? 'Waiting for your computer'
              : 'Checking connected computers…'
        }}
      </div>
      <p v-if="runtimes.error.value" class="inline-error" role="alert">
        Could not check connected computers. Retrying…
      </p>
      <RouterLink
        v-if="detected"
        class="button primary detected-link"
        :to="'/machines/' + encodeURIComponent(detected.id)"
        @click="emit('close')"
        >View computer<Icon name="right"
      /></RouterLink>
      <details class="setup-help">
        <summary>Can’t open a browser on that computer?</summary>
        <p>
          The commands above use an email code in the terminal, so no browser is needed. Use
          <code>{{ executable }} daemon status</code> and
          <code>{{ executable }} daemon logs</code> on macOS or Linux to check a background daemon.
          On Windows, read the foreground terminal output.
        </p>
        <p v-if="server.includes('127.0.0.1') || server.includes('localhost')">
          This server address refers to this computer. For a different computer, replace it with the
          server’s reachable address.
        </p>
      </details>
    </div>
    <footer class="setup-footer">
      <button class="button" @click="emit('close')">Cancel</button>
    </footer>
  </Modal>
</template>
<style scoped>
.add-computer-modal {
  width: 512px;
  border-radius: 13px;
}
.add-computer {
  padding: 26px 24px 16px;
  position: relative;
  font-size: 12px;
}
.close-add {
  position: absolute;
  right: 10px;
  top: 10px;
}
h2 {
  font-size: 16px;
  line-height: 22px;
  font-weight: 500;
  margin: 0 0 8px;
}
.intro {
  max-width: 365px;
  color: var(--muted);
  line-height: 16px;
  margin: 0 0 24px;
}
h3 {
  font-size: 12px;
  font-weight: 500;
  margin: 0 0 8px;
}
.platforms {
  display: flex;
  gap: 2px;
  margin: 0 0 10px;
}
.platforms button {
  padding: 4px 8px;
  border-radius: 5px;
  font-size: 14px;
  color: var(--muted);
}
.platforms button[aria-selected='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 1px 3px #0002;
}
.hint {
  font-size: 11px;
  line-height: 16px;
  color: var(--muted);
  margin: 8px 0 10px;
}
.second-step {
  margin-top: 24px;
}
.connection-status {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 11px 12px;
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 18px;
  min-height: 38px;
}
.health-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--stage-validation-dot);
  flex-shrink: 0;
}
.setup-help {
  margin-top: 16px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  color: var(--muted);
  font-size: 12px;
}
.setup-help summary {
  cursor: pointer;
  padding: 10px 12px;
}
.setup-help p {
  padding: 0 12px 10px;
  line-height: 18px;
}
code {
  font-family: ui-monospace, monospace;
  font-size: 11px;
  overflow-wrap: anywhere;
}
.setup-footer {
  border-top: 1px solid var(--border);
  padding: 12px 24px;
  display: flex;
  justify-content: flex-end;
}
.setup-footer .button {
  background: var(--surface);
  min-height: 28px;
  font-size: 14px;
  padding: 3px 10px;
}
.detected-link {
  margin-top: 12px;
  display: inline-flex;
}
</style>
