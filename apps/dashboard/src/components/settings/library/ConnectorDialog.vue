<script setup lang="ts">
import { computed } from 'vue';
import Modal from '../../Modal.vue';
import Icon from '../../Icon.vue';
import { useConnectorLibrary, useWorkspaceMCPServers } from '../../../api/library';
import { useMe, useMembers } from '../../../api/queries';

const props = defineProps<{ kind: 'catalogue' | 'custom'; connectorId: string }>();
const emit = defineEmits<{ close: [] }>();
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
              <dt>Availability</dt>
              <dd>{{ entry.enabled ? 'Available to this workspace' : entry.reason }}</dd>
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
          <p class="hint scope">
            {{
              kind === 'catalogue'
                ? 'Available connectors can be selected for Teams and routed to agents.'
                : 'Agents can use this connection through the recorded tool door.'
            }}
          </p>
        </template>
      </div>
      <footer><button class="button" @click="close">Close</button></footer>
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
.tabs {
  display: flex;
  gap: 24px;
  padding: 0 24px;
  border-bottom: 1px solid var(--border);
}
.tabs button {
  border: 0;
  background: none;
  padding: 12px 0;
  color: var(--muted);
  cursor: pointer;
}
.tabs [aria-selected='true'] {
  border-bottom: 2px solid var(--text);
  color: var(--text);
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
a,
.link {
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
.setting {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  border-bottom: 1px solid var(--border);
  padding-bottom: 18px;
  margin-bottom: 20px;
}
.setting input[role='switch'] {
  appearance: none;
  flex-shrink: 0;
  width: 31px;
  height: 18px;
  border: 0;
  border-radius: 12px;
  background: var(--muted);
  padding: 2px;
  cursor: pointer;
}
.setting input[role='switch']:checked {
  background: var(--primary);
}
.setting input[role='switch']::after {
  content: '';
  display: block;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--surface);
  transition: transform 0.12s;
}
.setting input[role='switch']:checked::after {
  transform: translateX(13px);
}
.setting b {
  font-weight: 500;
}
.field {
  display: block;
  font-size: 12px;
  margin: 20px 0 10px;
}
.field input {
  display: block;
  width: 100%;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 8px 10px;
  margin-top: 7px;
  color: var(--text);
}
.check {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  font-size: 12px;
  line-height: 1.5;
  margin: 20px 0 10px;
}
.check input {
  margin-top: 3px;
}
.link {
  border: 0;
  background: none;
  padding: 0;
  font-size: 12px;
  cursor: pointer;
}
.terms {
  display: block;
  font-size: 12px;
  margin-top: 10px;
}
.edit,
.delete {
  margin-top: 14px;
}
.delete {
  display: block;
}
.replacement {
  margin: 0 0 22px;
}
.draft-warning,
.delete-confirm {
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 12px;
  margin-top: 18px;
  font-size: 12px;
}
.delete-confirm .button {
  margin-right: 8px;
}
footer {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  padding: 14px 24px;
  border-top: 1px solid var(--border);
}
.inline-error {
  overflow-wrap: anywhere;
}
button:focus-visible,
input:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
