<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue';
import { version } from '../../../../../package.json';
import ReleaseHighlights from './ReleaseHighlights.vue';
import { releaseHighlights } from './release-highlights';

const bridge = window.lykeionDesktop?.updates;
const checking = ref(false);
const message = ref('');
const highlightsOpen = ref(false);
let mounted = true;
onBeforeUnmount(() => {
  mounted = false;
});

async function checkForUpdate() {
  if (!bridge || checking.value) return;
  checking.value = true;
  message.value = 'Checking for updates…';
  let text = 'The update check could not be completed. Check your connection and try again.';
  try {
    const result = await bridge.check();
    if (result.state === 'current') text = 'Lykeion is up to date.';
    else if (result.state === 'downloaded')
      text = `Version ${result.version} is ready. It installs when you quit Lykeion.`;
    else if (result.state === 'unavailable')
      text =
        'This copy of Lykeion does not update itself. Install a release from GitHub to get updates.';
  } catch {
    /* The default message covers a failed check. */
  }
  if (!mounted) return;
  message.value = text;
  checking.value = false;
}
</script>

<template>
  <section class="updates-page" aria-label="Updates">
    <div class="updates-card">
      <div class="update-row">
        <span>Current version</span>
        <span class="version">v{{ version }}</span>
      </div>
      <div v-if="releaseHighlights?.length" class="update-row">
        <span>Release highlights</span>
        <button class="button compact" type="button" @click="highlightsOpen = true">
          What’s new
        </button>
      </div>
      <div class="update-row">
        <div>
          <span>Automatic updates</span>
          <p v-if="bridge">
            Lykeion downloads new versions in the background and installs them when you quit.
            Running work is not interrupted.
          </p>
          <p v-else>The web app always runs the latest version.</p>
        </div>
      </div>
      <div v-if="bridge" class="update-row">
        <div>
          <span>Check for updates</span>
          <p aria-live="polite">{{ message || 'Lykeion also checks every hour.' }}</p>
        </div>
        <button class="button compact" :disabled="checking" @click="checkForUpdate">
          Check now
        </button>
      </div>
    </div>
    <ReleaseHighlights v-if="highlightsOpen" @close="highlightsOpen = false" />
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
</style>
