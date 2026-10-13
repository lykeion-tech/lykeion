<script setup lang="ts">
import { computed } from 'vue';
import { useRouter } from 'vue-router';
import { state, notify } from '../services/workspace';
import Icon from './Icon.vue';
const props = defineProps<{ id: string }>();
const router = useRouter();
const runtime = computed(() => state.data?.runtimes.find((x) => x.id === props.id));
const machine = computed(() => state.data?.machines.find((x) => x.id === runtime.value?.machineId));
const agents = computed(() => state.data?.agents.filter((x) => x.runtimeId === props.id) ?? []);
</script>
<template>
  <section class="runtime-detail">
    <header>
      <button @click="router.push('/runtimes')">Machines</button>
      <Icon name="right" :size="12" />
      <button v-if="machine" @click="router.push('/runtimes/' + machine.id)">
        {{ machine.name }}
      </button>
      <Icon v-if="machine" name="right" :size="12" />
      <code>{{ runtime?.name ?? 'Runtime' }}</code>
    </header>
    <div v-if="runtime" class="runtime-layout">
      <main>
        <section class="runtime-info">
          <div class="runtime-title">
            <span class="runtime-logo">
              <Icon name="command" :size="24" />
            </span>
            <h1>{{ runtime.name }}</h1>
            <span class="runtime-health">● {{ runtime.status }}</span>
          </div>
          <dl>
            <div>
              <dt>OWNER</dt>
              <dd>—</dd>
            </div>
            <div>
              <dt>DEVICE</dt>
              <dd>{{ machine?.name ?? runtime.platform }}</dd>
            </div>
            <div>
              <dt>RUNTIME</dt>
              <dd>{{ runtime.version ?? '—' }}</dd>
            </div>
          </dl>
          <details>
            <summary>Technical details</summary>
            <div class="runtime-technical">
              <div>
                <small>DAEMON CLI</small>
                <code>—</code>
              </div>
              <div>
                <small>RUNTIME ID</small>
                <code>{{ runtime.id }}</code>
              </div>
            </div>
          </details>
        </section>
        <div class="runtime-usage">
          <Icon name="chart" :size="20" />
          <span>Usage data unavailable</span>
        </div>
      </main>
      <aside>
        <section>
          <header>
            Serving
            <small>{{ agents.length }} {{ agents.length === 1 ? 'agent' : 'agents' }}</small>
          </header>
          <button
            v-for="agent in agents"
            :key="agent.id"
            class="serving-agent"
            @click="router.push('/agents/' + agent.id)"
          >
            <Icon :name="agent.avatar ?? 'bot'" />
            <span
              >{{ agent.name }}<small>{{ agent.status }}</small>
            </span>
            <Icon name="right" class="push-right" :size="12" />
          </button>
          <p v-if="!agents.length">No agents assigned</p>
        </section>
        <section>
          <header>Diagnostics</header>
          <div class="runtime-diagnostics">
            <small>VISIBILITY</small>
            <div class="visibility-toggle">
              <button
                @click="notify('Runtime visibility requires the control plane access adapter.')"
              >
                <Icon name="shield" :size="12" />Private
              </button>
              <button
                @click="notify('Runtime visibility requires the control plane access adapter.')"
              >
                <Icon name="network" :size="12" />Public
              </button>
            </div>
            <button
              class="delete-runtime"
              @click="
                notify(
                  'Deleting runtimes requires the connected control plane. Nothing was deleted.',
                )
              "
            >
              <Icon name="close" :size="14" />Delete runtime
            </button>
          </div>
        </section>
      </aside>
    </div>
    <div v-else class="runtime-missing">Runtime not found.</div>
  </section>
</template>
<style scoped>
.runtime-detail {
  position: absolute;
  inset: 0;
  background: var(--surface);
  z-index: 6;
  overflow: auto;
}
.runtime-detail > header {
  height: 58px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 20px;
  font-size: 14px;
  color: var(--muted);
}
.runtime-detail > header button {
  padding: 0;
}
.runtime-detail > header code {
  font-size: 12px;
  color: var(--text);
}
.runtime-layout {
  display: flex;
  gap: 16px;
  padding: 18px 24px 24px;
}
.runtime-layout > main {
  flex: 1;
  min-width: 0;
}
.runtime-layout > aside {
  width: 320px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.runtime-info {
  background: #edecf1;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.runtime-title {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  height: 68px;
}
.runtime-title h1 {
  font-size: 16px;
  font-weight: 600;
}
.runtime-logo {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  background: #e8e8ef;
  border-radius: 6px;
}
.runtime-health {
  font-size: 12px;
  background: #dce4e4;
  color: var(--muted);
  border-radius: 14px;
  padding: 2px 8px;
}
.runtime-info dl {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  margin: 0;
}
.runtime-info dl > div {
  padding: 14px 16px;
  border-top: 1px solid var(--border);
  border-right: 1px solid var(--border);
  min-height: 70px;
}
.runtime-info dt,
.runtime-technical small {
  font-size: 10px;
  letter-spacing: 0.6px;
  color: var(--muted);
}
.runtime-info dd {
  margin: 7px 0 0;
  font-size: 13px;
}
.runtime-info summary {
  padding: 8px 16px;
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
  border-top: 1px solid var(--border);
}
.runtime-technical {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 20px;
  padding: 14px 16px;
  background: #e8e8ef;
}
.runtime-technical > div {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.runtime-technical code {
  font-size: 12px;
  overflow-wrap: anywhere;
}
.runtime-usage {
  height: 110px;
  border: 1px dashed var(--border);
  border-radius: 8px;
  margin-top: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 12px;
  font-size: 12px;
  color: var(--muted);
}
.runtime-layout > aside > section {
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.runtime-layout aside header {
  display: flex;
  justify-content: space-between;
  border-bottom: 1px solid var(--border);
  padding: 12px 16px;
  font-size: 12px;
  font-weight: 500;
}
.runtime-layout aside header small {
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
}
.runtime-layout aside p {
  padding: 14px 16px;
  font-size: 12px;
  color: var(--muted);
}
.serving-agent {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 16px;
  width: 100%;
  text-align: left;
  font-size: 12px;
}
.serving-agent small {
  display: block;
  margin-top: 3px;
  color: var(--muted);
}
.runtime-diagnostics {
  padding: 16px;
}
.runtime-diagnostics > small {
  font-size: 10px;
  color: var(--muted);
}
.visibility-toggle {
  display: flex;
  align-self: flex-start;
  background: #e1e1e6;
  width: fit-content;
  border-radius: 6px;
  margin-top: 6px;
  padding: 2px;
}
.visibility-toggle button {
  height: 24px;
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  padding: 0 8px;
  border-radius: 4px;
}
.delete-runtime {
  display: flex;
  gap: 8px;
  align-items: center;
  border-top: 1px solid var(--border);
  margin-top: 14px;
  width: 100%;
  padding: 20px 10px 8px;
  font-size: 14px;
  color: #d62846;
}
.runtime-missing {
  padding: 40px;
  text-align: center;
  color: var(--muted);
}
</style>
