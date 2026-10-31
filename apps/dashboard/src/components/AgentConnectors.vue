<script setup lang="ts">
import { computed, ref } from 'vue';
import Icon from '../ui/Icon.vue';
import Modal from '../ui/Modal.vue';
import AgentConnectorDialog from './AgentConnectorDialog.vue';
import type { AgentConnector } from '../lib/agent-connectors';
const props = defineProps<{ servers: AgentConnector[]; editable: boolean; archived: boolean }>();
const emit = defineEmits<{ save: [servers: AgentConnector[]]; remove: [name: string] }>();
const allowed = computed(() => props.editable && !props.archived);
const inspecting = ref<{ name?: string }>();
const removing = ref<string>();
function save(servers: AgentConnector[]) {
  if (allowed.value) emit('save', servers);
}
function remove() {
  if (!allowed.value || !props.servers.some((server) => server.name === removing.value)) return;
  emit('remove', removing.value!);
  removing.value = undefined;
}
</script>
<template>
  <section class="agent-connectors">
    <h2>Connectors</h2>
    <p class="lede">
      This agent’s own servers are available on its solo studies and agent-assigned Autoresearch
      runs. Team runs use the Team’s declared connectors.
    </p>
    <div class="section-title">
      <h3>
        This agent’s servers <small>{{ servers.length }} of 8</small>
      </h3>
      <button
        v-if="allowed"
        class="button"
        :disabled="servers.length >= 8"
        @click="inspecting = {}"
      >
        <Icon name="plus" :size="14" />Add connector
      </button>
    </div>
    <div v-for="server in servers" :key="server.name" class="server-row">
      <Icon name="plug" :size="18" />
      <div class="text">
        <b>{{ server.name }}</b>
        <p>{{ server.url }}</p>
      </div>
      <button
        class="icon-button"
        :aria-label="`${allowed ? 'Edit' : 'View'} ${server.name}`"
        @click="inspecting = { name: server.name }"
      >
        <Icon :name="allowed ? 'edit' : 'forward'" :size="15" /></button
      ><button
        v-if="allowed"
        class="icon-button"
        :aria-label="`Remove ${server.name}`"
        @click="removing = server.name"
      >
        <Icon name="close" :size="15" />
      </button>
    </div>
    <p v-if="!servers.length" class="empty">No connectors have been added to this agent.</p>
    <div class="workspace-connectors">
      <h3>Workspace connectors</h3>
      <p class="lede">
        This agent also draws from the workspace library on solo studies and agent-assigned
        Autoresearch runs.
      </p>
      <RouterLink to="/settings/connectors">Manage workspace connectors →</RouterLink>
    </div>
    <p v-if="!allowed" class="lede access-note">
      {{
        archived
          ? 'This agent is archived. Its connector configuration is read-only.'
          : 'Only the agent’s owner and workspace admins can change its connectors.'
      }}
    </p>
    <AgentConnectorDialog
      v-if="inspecting"
      :key="inspecting.name ?? 'new'"
      :servers="servers"
      :editable="editable"
      :archived="archived"
      :name="inspecting.name"
      @save="save"
      @close="inspecting = undefined"
    />
    <Modal
      v-if="removing"
      v-slot="{ close }"
      class="agent-remove-connector"
      title="Remove connector"
      @close="removing = undefined"
      ><section>
        <header>
          <h2>Remove connector?</h2>
          <button class="icon-button" aria-label="Close" @click="close">
            <Icon name="close" :size="18" />
          </button>
        </header>
        <div class="remove-body">
          <p>
            Remove <b>{{ removing }}</b> from this agent?
          </p>
          <p class="lede">
            This detaches the agent’s saved connection. It does not delete the remote server or
            change workspace connectors.
          </p>
        </div>
        <footer>
          <button class="button" @click="close">Cancel</button
          ><button
            class="button danger"
            :disabled="!allowed || !servers.some((server) => server.name === removing)"
            @click="remove"
          >
            Remove connector
          </button>
        </footer>
      </section></Modal
    >
  </section>
</template>
<style scoped>
.agent-connectors {
  padding: 28px 30px;
  max-width: 820px;
  min-width: 0;
}
h2 {
  font-size: 17px;
  font-weight: 500;
  margin: 0 0 16px;
}
h3 {
  font-size: 13px;
  font-weight: 500;
  margin: 0;
}
.lede {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.6;
  margin: 0 0 16px;
}
.section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin: 24px 0 12px;
}
small {
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
  margin-left: 5px;
}
.server-row {
  display: flex;
  align-items: center;
  gap: 12px;
  border-bottom: 1px solid var(--border);
  padding: 16px 0;
}
.server-row:hover {
  background: var(--hover);
}
.text {
  flex: 1;
  min-width: 0;
}
.text b {
  font-size: 13px;
  font-weight: 500;
}
.text p {
  font-size: 12px;
  color: var(--muted);
  margin: 5px 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.icon-button {
  border: 0;
  background: none;
  color: var(--muted);
  padding: 4px;
  cursor: pointer;
}
.empty {
  border: 1px dashed var(--border);
  border-radius: 8px;
  padding: 24px;
  text-align: center;
  color: var(--muted);
  font-size: 12px;
}
.workspace-connectors {
  border-top: 1px solid var(--border);
  margin-top: 28px;
  padding-top: 20px;
}
.workspace-connectors .lede {
  margin: 8px 0;
}
a {
  color: var(--primary-text);
  text-decoration: none;
  font-size: 12px;
}
.access-note {
  margin-top: 20px;
}
.agent-remove-connector {
  width: 450px;
}
.agent-remove-connector header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px;
  border-bottom: 1px solid var(--border);
}
.agent-remove-connector h2 {
  margin: 0;
  font-size: 16px;
}
.remove-body {
  padding: 20px;
  font-size: 12px;
  line-height: 1.5;
}
.remove-body p {
  margin: 0 0 12px;
}
footer {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  padding: 14px 20px;
  border-top: 1px solid var(--border);
}
button:focus-visible,
a:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
