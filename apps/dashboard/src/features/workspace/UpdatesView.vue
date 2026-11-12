<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { version } from '../../../../../package.json';

const bridge = window.lykeionDesktop?.updates;
const available = ref(false);
const checking = ref(false);
const message = ref('');
let mounted = true;

onMounted(async () => {
  if (!bridge) return;
  try {
    const enabled = await bridge.available();
    if (mounted) available.value = enabled;
  } catch {
    if (mounted) available.value = false;
  }
});

onBeforeUnmount(() => {
  mounted = false;
  if (checking.value) void bridge?.cancel().catch(() => {});
});

async function checkForUpdate() {
  if (!bridge || !available.value || checking.value) return;
  checking.value = true;
  message.value = 'Checking for and downloading an update…';
  try {
    const result = await bridge.check();
    if (!mounted) return;
    message.value =
      result.state === 'current'
        ? 'Lykeion is up to date.'
        : `Version ${result.version} was downloaded. Installation is not available yet.`;
  } catch {
    if (mounted) message.value = 'The update check could not be completed. Try again later.';
  } finally {
    if (mounted) checking.value = false;
  }
}

async function cancelCheck() {
  if (!bridge || !checking.value) return;
  try {
    await bridge.cancel();
  } catch {
    /* The interrupted check reports its own outcome. */
  }
}
</script>

<template>
  <section class="updates-page" aria-label="Updates">
    <div class="updates-card">
      <div class="update-row">
        <span>Current version</span>
        <span class="version">v{{ version }}</span>
      </div>
      <div class="update-row">
        <div>
          <span>Automatic background updates</span>
          <p>Unavailable in this build. A desktop update provider has not been configured.</p>
        </div>
        <button
          class="update-switch"
          role="switch"
          aria-label="Automatic background updates"
          aria-checked="false"
          disabled
        >
          <span />
        </button>
      </div>
      <div class="update-row">
        <div>
          <span>Check for updates</span>
          <p v-if="!available">
            Update checks will be available when a desktop update provider is connected.
          </p>
          <p v-else aria-live="polite">
            {{ message || 'Check for and download an update. Installation is not yet available.' }}
          </p>
        </div>
        <button v-if="checking" class="button compact" @click="cancelCheck">Cancel</button>
        <button v-else class="button compact" :disabled="!available" @click="checkForUpdate">
          Check now
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.updates-page {
  width: min(100%, 1448px);
  margin: 18px auto;
  padding: 0 20px;
  overflow-y: auto;
}
.updates-card {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--fill-card);
  overflow: hidden;
}
.update-row {
  min-height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 16px;
}
.update-row + .update-row {
  border-top: 1px solid var(--border);
}
.update-row p {
  color: var(--muted);
  font-size: 13px;
  margin: 6px 0 0;
  line-height: 18px;
}
.version {
  color: var(--muted);
  font-family: var(--font-mono, monospace);
  font-size: 13px;
}
.update-row > button {
  flex-shrink: 0;
}
.update-switch {
  width: 32px;
  height: 18px;
  border-radius: 20px;
  background: var(--selected);
  padding: 1px;
  display: flex;
  align-items: center;
}
.update-switch span {
  height: 16px;
  width: 16px;
  border-radius: 50%;
  background: var(--text);
}
</style>
