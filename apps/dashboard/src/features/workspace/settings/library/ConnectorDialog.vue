<script setup lang="ts">
import { computed } from 'vue';
import Modal from '../../../../ui/Modal.vue';
import Icon from '../../../../ui/Icon.vue';
import { useConnectorLibrary, useWorkspaceMCPServers } from '../../../../api/library';
import { useMe, useMembers } from '../../../../api/queries';

const props = defineProps<{
  kind: 'catalogue' | 'custom';
  connectorId: string;
  canEdit?: boolean;
}>();
const emit = defineEmits<{ close: []; edit: [] }>();
const catalogue = useConnectorLibrary(),
  custom = useWorkspaceMCPServers();
const members = useMembers(),
  me = useMe();
const entry = computed(() => catalogue.data.value?.find((c) => c.name === props.connectorId));
const server = computed(() => custom.data.value?.find((c) => c.id === props.connectorId));
const row = computed(() => (props.kind === 'catalogue' ? entry.value : server.value));
const query = computed(() => (props.kind === 'catalogue' ? catalogue : custom));
const author = computed(() => {
  const id = server.value?.created_by;
  if (id === me.data.value?.id) return 'You';
  const person = members.data.value?.find((m) => m.user_id === id);
  return person?.name || person?.email || id;
});
</script>
<template>
  <Modal
    v-slot="{ close }"
    class="connector-modal"
    :title="`${row?.name ?? connectorId} connector`"
    @close="emit('close')"
  >
    <section class="connector-detail">
      <header>
        <div class="title">
          <h2>{{ row?.name ?? connectorId }}</h2>
          <button class="icon-button" aria-label="Close connector" @click="close">
            <Icon name="close" :size="18" />
          </button>
        </div>
        <p v-if="entry && kind === 'catalogue'" class="lede">{{ entry.purpose }}</p>
        <div class="chips">
          <span>{{ kind === 'catalogue' ? entry?.domain : 'Custom' }}</span>
          <span>{{
            kind === 'catalogue'
              ? entry?.tier
              : server?.transport === 'url'
                ? 'Remote URL'
                : 'Command'
          }}</span>
        </div>
      </header>
      <div class="body">
        <p v-if="query.isPending.value" role="status">Loading connector…</p>
        <p v-if="query.error.value" role="alert">{{ query.error.value.message }}</p>
        <p v-if="!query.isPending.value && !row" role="alert">
          This connector is no longer available.
        </p>
        <template v-if="row">
          <h3>Overview</h3>
          <dl class="facts">
            <template v-if="kind === 'catalogue' && entry">
              <dt>Origin</dt>
              <dd>Lykeion scientific catalogue</dd>
              <template v-if="entry.terms"
                ><dt>Source terms</dt>
                <dd>
                  <a :href="entry.terms" target="_blank" rel="noreferrer">View source terms ↗</a>
                </dd></template
              >
            </template>
            <template v-else>
              <dt>Origin</dt>
              <dd>Added to this workspace</dd>
              <dt>Added by</dt>
              <dd>{{ author }}</dd>
              <dt>Connection</dt>
              <dd>{{ server?.transport === 'url' ? 'Remote URL' : 'Command' }}</dd>
            </template>
            <dt>Scope</dt>
            <dd>Workspace</dd>
          </dl>
          <p v-if="kind === 'custom'" class="hint scope">
            Agents can use this connection through the recorded tool door.
          </p>
        </template>
      </div>
      <footer v-if="row && kind === 'catalogue'">
        <p class="hint footer-hint">
          Available connectors can be selected for Teams and routed to agents.
        </p>
      </footer>
      <footer v-if="server && kind === 'custom' && canEdit">
        <button class="button" type="button" @click="emit('edit')">Edit connector</button>
      </footer>
    </section>
  </Modal>
</template>
<style scoped>
.connector-modal,
.connector-detail {
  width: 620px;
  max-width: 100%;
}
.connector-detail {
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
  background: var(--surface);
}
header {
  padding: 22px 24px 18px;
}
.title {
  display: flex;
  align-items: center;
  gap: 12px;
}
.title h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
  flex: 1;
}
.icon-button {
  border: 0;
  background: none;
  color: var(--muted);
  cursor: pointer;
}
.lede {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
  margin: 10px 0 14px;
}
.chips {
  display: flex;
  gap: 6px;
  margin-top: 12px;
}
.chips span {
  font-size: 11px;
  background: var(--hover);
  color: var(--muted);
  border-radius: 5px;
  padding: 4px 7px;
}
.body {
  padding: 16px 24px 24px;
  overflow: auto;
  min-height: 250px;
}
.body h3 {
  font-size: 16px;
  margin: 0 0 16px;
}
.facts {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 14px;
  font-size: 12px;
  line-height: 1.5;
  margin: 0;
}
.facts dt {
  color: var(--muted);
}
.facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
a {
  color: var(--primary-text);
}
a {
  text-decoration: none;
}
.hint {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
  margin: 6px 0 0;
}
.scope {
  margin-top: 24px;
}
.terms {
  display: block;
  font-size: 12px;
  margin-top: 10px;
}
footer {
  display: flex;
  align-items: center;
  padding: 14px 24px;
  border-top: 1px solid var(--border);
}
.footer-hint {
  margin: 0;
}
button:focus-visible,
input:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
