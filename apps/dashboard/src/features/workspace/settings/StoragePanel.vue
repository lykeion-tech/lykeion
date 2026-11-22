<script setup lang="ts">
import { useStorage } from '../../../api/queries';
import type { StorageMachine } from '../../../api/types';
const storage = useStorage();
const desktop = typeof window !== 'undefined' && Boolean(window.lykeionDesktop);
const labels = { workspaces: 'Workspaces', environments: 'Environments', state: 'Daemon state' };
function bytes(value: number) {
  if (value === 0) return '0 B';
  const index = Math.min(4, Math.floor(Math.log(value) / Math.log(1024)));
  return `${(value / 1024 ** index).toLocaleString(undefined, { maximumFractionDigits: 1 })} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][index]}`;
}
const date = (value: string) => new Date(value).toLocaleString();
const total = (machine: StorageMachine) =>
  machine.snapshot?.categories.reduce((sum, item) => sum + item.bytes, 0) ?? 0;
const complete = (machine: StorageMachine) =>
  machine.snapshot?.categories.every((item) => item.complete);
const measured = (machine: StorageMachine) =>
  machine.snapshot?.categories.some((item) => item.complete || item.bytes > 0);
</script>
<template>
  <div class="storage-panel" data-testid="storage-panel">
    <p v-if="storage.isPending.value" role="status">Loading storage…</p>
    <div v-else-if="storage.isError.value" role="alert" class="error">
      Could not load storage. {{ storage.error.value?.message }}
      <button class="button compact" @click="storage.refetch()">Retry</button>
    </div>
    <p v-else-if="!storage.data.value?.machines.length" class="description">
      No machines registered to you in this workspace.
    </p>
    <template v-else>
      <article
        v-for="machine in storage.data.value?.machines"
        :key="machine.daemon_id"
        class="machine"
        data-testid="storage-machine"
      >
        <header>
          <h3>{{ machine.name }}</h3>
          <span class="status" :class="{ online: machine.online }">{{
            machine.online ? 'Online' : 'Offline'
          }}</span>
        </header>
        <p v-if="!machine.snapshot" class="unavailable">
          {{
            desktop
              ? 'This machine has not reported storage yet.'
              : 'The web app can’t check this machine’s storage directly. Open the Lykeion desktop app on this machine to check local storage.'
          }}
        </p>
        <template v-else>
          <p class="description">
            {{ machine.online ? 'Measured' : 'Last known measurement' }}
            {{ date(machine.snapshot.measured_at) }}
          </p>
          <div
            v-if="complete(machine)"
            class="usage-bar"
            role="img"
            :aria-label="`Measured Lykeion files: ${bytes(total(machine))}`"
          >
            <span
              v-for="category in machine.snapshot.categories"
              :key="category.key"
              :class="category.key"
              :style="{ width: `${total(machine) ? (category.bytes / total(machine)) * 100 : 0}%` }"
              :title="`${labels[category.key]}: ${bytes(category.bytes)}`"
            ></span>
          </div>
          <dl class="categories">
            <div
              v-for="category in machine.snapshot.categories"
              :key="category.key"
              class="category"
            >
              <dt>
                <i :class="category.key"></i>{{ labels[category.key]
                }}<code>{{ category.path }}</code>
              </dt>
              <dd>
                {{
                  category.complete
                    ? bytes(category.bytes)
                    : category.bytes > 0
                      ? `${bytes(category.bytes)} recorded · partial`
                      : 'Unavailable'
                }}<small v-if="!category.complete">{{
                  category.error || 'Measurement incomplete'
                }}</small>
              </dd>
            </div>
            <div class="total">
              <dt>{{ complete(machine) ? 'Total measured' : 'Partial subtotal' }}</dt>
              <dd>{{ measured(machine) ? bytes(total(machine)) : 'Unavailable' }}</dd>
            </div>
          </dl>
          <div class="filesystems">
            <h4>Available space by filesystem</h4>
            <p v-if="!machine.snapshot.filesystems.length" class="description">
              Available space is unavailable.
            </p>
            <div
              v-for="filesystem in machine.snapshot.filesystems"
              :key="filesystem.id"
              class="filesystem"
            >
              <span
                ><code v-for="path in filesystem.paths" :key="path">{{ path }}</code></span
              ><strong>{{ bytes(filesystem.available_bytes) }} available</strong>
            </div>
          </div>
        </template>
      </article>
    </template>
  </div>
</template>
<style scoped>
.storage-panel {
  font-size: 13px;
}
.description {
  color: var(--muted);
  line-height: 20px;
  margin: 0 0 12px;
}
.machine {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 20px;
  margin-top: 20px;
}
.machine header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}
h3 {
  font-size: 14px;
  font-weight: 600;
  margin: 0;
}
.status {
  font-size: 13px;
  color: var(--muted);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 2px 8px;
}
.status.online {
  color: var(--primary-text);
}
.usage-bar {
  height: 12px;
  display: flex;
  overflow: hidden;
  border-radius: 6px;
  background: var(--border);
  margin-top: 20px;
}
.workspaces {
  background: var(--primary);
}
.environments {
  background: var(--muted);
}
.state {
  background: var(--selected);
}
.categories {
  margin: 16px 0 0;
}
.category,
.total {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  padding: 14px 0;
  border-top: 1px solid var(--border);
}
dt {
  min-width: 0;
}
dd {
  margin: 0;
  text-align: right;
  flex-shrink: 0;
}
i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 2px;
  margin-right: 8px;
}
code {
  display: block;
  color: var(--muted);
  font-size: 11px;
  overflow-wrap: anywhere;
  margin-top: 5px;
}
small {
  display: block;
  color: var(--muted);
  margin-top: 5px;
  max-width: 250px;
}
.total {
  font-weight: 600;
}
h4 {
  font-weight: 500;
  margin: 8px 0 12px;
}
.filesystem {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  margin-top: 10px;
}
.filesystem strong {
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
}
.filesystem code {
  margin: 0 0 4px;
}
.unavailable {
  color: var(--muted);
  padding: 12px 0;
}
.error {
  color: var(--error-text);
  display: flex;
  align-items: center;
  gap: 12px;
}
@media (width < 48rem) {
  .machine {
    padding: 16px;
  }
  .category,
  .filesystem {
    flex-wrap: wrap;
    gap: 8px;
  }
  dd {
    text-align: left;
  }
}
</style>
