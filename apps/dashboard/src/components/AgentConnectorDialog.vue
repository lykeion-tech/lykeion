<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
import { type AgentConnector, validateAgentConnectors } from '../lib/agent-connectors';
import { useResourceDraft } from '../state/resource-draft';

const props = defineProps<{
  servers: AgentConnector[];
  editable: boolean;
  archived: boolean;
  name?: string;
}>();
const emit = defineEmits<{ close: []; save: [servers: AgentConnector[]] }>();
const allowed = computed(() => props.editable && !props.archived);
const original = computed(() => props.servers.find((server) => server.name === props.name));
const initial = computed(() => original.value ?? { name: '', url: '' });
// This revision fences incoming component snapshots, not an API acknowledgement.
const revision = ref(0);
watch(
  () => props.servers,
  () => revision.value++,
  { deep: true, flush: 'sync' },
);
const form = useResourceDraft(
  computed(() => `agent-connector:${props.name ?? 'new'}`),
  computed(() =>
    allowed.value && (!props.name || original.value)
      ? { revision: revision.value, value: initial.value }
      : undefined,
  ),
);
const fields = computed(() => form.draft.value ?? initial.value);
const nextServers = computed(() => {
  const replacement = { name: fields.value.name, url: fields.value.url };
  return props.name
    ? props.servers.map((server) =>
        server.name === props.name ? replacement : { name: server.name, url: server.url },
      )
    : [...props.servers.map(({ name, url }) => ({ name, url })), replacement];
});
const problem = computed(() => validateAgentConnectors(nextServers.value));
const title = computed(() => (props.name ? 'Edit connector' : 'Add connector'));
function save() {
  if (!allowed.value || !form.canSave.value || problem.value) return;
  // The owner must persist and acknowledge this list through its real API.
  emit('save', nextServers.value);
}
</script>
<template>
  <Modal
    v-slot="{ close }"
    class="agent-connector-modal"
    :title="props.name && !allowed && !form.dirty.value ? 'Connector details' : title"
    :before-close="form.reset"
    @close="emit('close')"
  >
    <form @submit.prevent="save">
      <header>
        <h2>{{ allowed ? title : 'Connector details' }}</h2>
        <button type="button" class="icon-button" aria-label="Close connector" @click="close">
          <Icon name="close" :size="18" />
        </button>
      </header>
      <div class="body">
        <label
          >Connector name<input
            v-model="fields.name"
            :readonly="!allowed"
            autocomplete="off"
            spellcheck="false"
          /><span class="hint"
            >Use a unique name. Names starting with lyk and the name sources are reserved.</span
          ></label
        >
        <label
          >Connector URL<input
            v-model="fields.url"
            :readonly="!allowed"
            autocomplete="off"
            spellcheck="false"
          /><span class="hint"
            >HTTP and HTTPS endpoints, with no credentials, query or fragment.</span
          ></label
        >
        <p class="hint">Saved to this agent. Servers have a name and URL only.</p>
        <p v-if="!allowed" class="hint" role="status">
          {{
            archived
              ? 'This agent is archived. Its connectors are read-only.'
              : 'Only the agent’s owner and workspace admins can change its connectors.'
          }}<template v-if="form.dirty.value"> Your draft is preserved.</template>
        </p>
        <p v-if="props.name && !original" class="hint" role="status">
          This connector is no longer available. Your draft is preserved.
        </p>
        <p v-if="problem && form.dirty.value" class="inline-error" role="alert">{{ problem }}</p>
        <div v-if="form.stale.value" class="draft-warning" role="status">
          <p>Saved connectors changed. Your draft is preserved.</p>
          <button type="button" class="button" @click="form.reset">Reload saved version</button>
        </div>
      </div>
      <footer>
        <button type="button" class="button" @click="close">Cancel</button
        ><button v-if="allowed" class="button primary" :disabled="!form.canSave.value || !!problem">
          {{ props.name ? 'Save changes' : 'Add connector' }}
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.agent-connector-modal {
  width: 450px;
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px;
  border-bottom: 1px solid var(--border);
}
h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 500;
}
.body {
  padding: 20px;
}
label {
  display: block;
  font-size: 12px;
  margin-bottom: 18px;
}
input {
  display: block;
  width: 100%;
  padding: 8px 10px;
  margin: 7px 0 6px;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--input);
  color: var(--text);
}
input:read-only {
  color: var(--muted);
}
.hint {
  display: block;
  font-size: 12px;
  line-height: 1.5;
  color: var(--muted);
}
.icon-button {
  border: 0;
  background: none;
  color: var(--muted);
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 14px 20px;
  border-top: 1px solid var(--border);
}
.draft-warning {
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 7px;
  margin-top: 18px;
}
.draft-warning p {
  margin: 0 0 10px;
}
.inline-error {
  margin-top: 12px;
  overflow-wrap: anywhere;
}
input:focus-visible,
button:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
