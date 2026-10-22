<script setup lang="ts">
import { version } from '../../../../package.json';
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
          <p>Update checks will be available when a desktop update provider is connected.</p>
        </div>
        <button class="button compact" disabled>Check now</button>
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
  font-size: 12px;
  margin: 6px 0 0;
  line-height: 16px;
}
.version {
  color: var(--muted);
  font-family: var(--font-mono, monospace);
  font-size: 12px;
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
