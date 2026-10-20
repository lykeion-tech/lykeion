<script setup lang="ts">
import { computed } from 'vue';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
import { backendDefaultModel, backendLabel } from '../lib/backends';
import type { Runtime } from '../api/types';
const props = defineProps<{ harness: string; runtimes?: Runtime[] }>();
defineEmits<{ close: [] }>();
const name = computed(() => backendLabel(props.harness));
const reported = computed(() => (props.runtimes ?? []).filter((r) => r.harness === props.harness));
</script>
<template>
  <Modal
    v-slot="{ close }"
    class="backend-modal"
    :title="`${name} backend details`"
    @close="$emit('close')"
  >
    <div class="backend-dialog">
      <header>
        <h2>{{ name }}</h2>
        <button class="icon-button" aria-label="Close" @click="close"><Icon name="close" /></button>
      </header>
      <div class="backend-body">
        <span class="adapter-chip">Adapter capabilities</span>
        <dl class="backend-facts">
          <dt>CLI</dt>
          <dd>
            {{ { claude: 'claude', codex: 'codex' }[harness] || 'Not reported' }}
          </dd>
          <dt>Default model</dt>
          <dd>{{ backendDefaultModel(harness) || 'Harness default' }}</dd>
          <dt>Model selection</dt>
          <dd>An explicit model overrides the adapter default.</dd>
        </dl>
        <h3>Daemon reports</h3>
        <template v-if="reported.length">
          <dl v-for="runtime in reported" :key="runtime.id" class="backend-facts runtime-report">
            <dt>Machine</dt>
            <dd>{{ runtime.custom_name || runtime.device_name }}</dd>
            <dt>Health</dt>
            <dd>
              {{
                runtime.status === 'online'
                  ? 'Online'
                  : runtime.status === 'offline'
                    ? 'Offline'
                    : 'Not reported'
              }}
            </dd>
            <dt>CLI version</dt>
            <dd>{{ runtime.cli_version || 'Not reported' }}</dd>
            <dt>Active runs</dt>
            <dd>{{ runtime.active_runs ?? 'Not reported' }}</dd>
          </dl>
        </template>
        <dl v-else class="backend-facts">
          <dt>Health</dt>
          <dd>Not reported</dd>
          <dt>CLI version</dt>
          <dd>Not reported</dd>
          <dt>Active runs</dt>
          <dd>Not reported</dd>
        </dl>
      </div>
      <footer><button class="button" @click="close">Close</button></footer>
    </div>
  </Modal>
</template>
<style scoped>
.backend-modal {
  width: 620px;
}
.backend-dialog {
  width: 100%;
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - 64px);
  background: var(--canvas);
  border-radius: 14px;
}
header,
footer {
  padding: 18px 24px;
  display: flex;
  align-items: center;
  border-bottom: 1px solid var(--border);
  gap: 12px;
}
header h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
  flex: 1;
}
.backend-body {
  overflow: auto;
  padding: 24px;
}
.adapter-chip {
  display: inline-block;
  background: var(--input);
  color: var(--muted);
  font-size: 11px;
  border-radius: 5px;
  padding: 4px 7px;
  margin-bottom: 18px;
}
.backend-facts {
  display: grid;
  grid-template-columns: 132px minmax(0, 1fr);
  gap: 14px;
  font-size: 12px;
  line-height: 1.5;
  margin: 0;
}
dt {
  color: var(--muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
h3 {
  font-size: 13px;
  font-weight: 500;
  margin: 24px 0 16px;
}
.runtime-report + .runtime-report {
  margin-top: 18px;
  padding-top: 18px;
  border-top: 1px solid var(--border);
}
footer {
  border-bottom: 0;
  border-top: 1px solid var(--border);
  justify-content: flex-end;
  padding: 14px 24px;
}
@media (max-width: 600px) {
  .backend-facts {
    grid-template-columns: 110px minmax(0, 1fr);
  }
  .backend-body {
    padding: 20px;
  }
}
</style>
