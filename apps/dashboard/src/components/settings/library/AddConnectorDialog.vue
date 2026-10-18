<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import Modal from '../../Modal.vue';
import Icon from '../../Icon.vue';
import ConnectorConfigFields, {
  emptyConnector,
  connectorInput,
  connectorProblem,
} from './ConnectorConfigFields.vue';
import { useLibraryMutations } from '../../../api/library';
import { useWorkspaces } from '../../../api/queries';
import { workspaceId } from '../../../api/session';
import { useResourceDraft, guardResourceDrafts } from '../../../state/resource-draft';
const emit = defineEmits<{ close: []; created: [id: string] }>();
const workspaces = useWorkspaces();
const allowed = computed(() =>
  ['owner', 'admin'].includes(
    workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role ?? '',
  ),
);
const empty = { revision: 0, value: emptyConnector() };
const form = useResourceDraft(
  computed(() => workspaceId.value + ':add-connector'),
  computed(() => (allowed.value ? empty : undefined)),
);
const fields = computed({
  get: () => form.draft.value!,
  set: (value) => (form.draft.value = value),
});
const mutations = useLibraryMutations(),
  error = ref(''),
  busy = ref(false);
const problem = computed(() => (fields.value ? connectorProblem(fields.value) : ''));
let visit = 0;
onUnmounted(() => visit++);
async function submit() {
  if (!allowed.value || !form.canSave.value || problem.value || busy.value) return;
  const token = visit,
    ws = workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    const response = await mutations.createWorkspaceMCP(connectorInput(fields.value));
    if (token === visit && ws === workspaceId.value) emit('created', response.id);
  } catch (e) {
    if (token === visit && ws === workspaceId.value) {
      error.value = (e as Error).message;
      await workspaces.refetch();
    }
  } finally {
    if (token === visit && ws === workspaceId.value) busy.value = false;
  }
}
</script>
<template>
  <Modal
    v-slot="{ close }"
    class="add-connector-modal"
    title="Add connector"
    :before-close="guardResourceDrafts"
    @close="emit('close')"
    ><section class="add-connector">
      <header>
        <h2>Add connector</h2>
        <button class="icon-button" aria-label="Close add connector" @click="close">
          <Icon name="close" :size="18" />
        </button>
      </header>
      <div class="body">
        <ConnectorConfigFields v-if="fields" v-model="fields" :disabled="!allowed || busy" />
        <p v-if="!allowed" role="alert">
          Only workspace owners and admins can add custom connectors. Your draft is preserved.
        </p>
        <p class="scope">
          Shared with this workspace. Add a connector only if you trust its server and the tools it
          exposes.
        </p>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <p v-if="busy" role="status">Adding connector…</p>
      </div>
      <footer>
        <button class="button" @click="close">Cancel</button
        ><button
          class="button primary"
          :disabled="!allowed || !form.canSave.value || !!problem || busy"
          :title="problem"
          @click="submit"
        >
          Add connector
        </button>
      </footer>
    </section></Modal
  >
</template>
<style scoped>
.add-connector-modal,
.add-connector {
  width: 620px;
  max-width: 100%;
}
.add-connector {
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 21px 28px;
  border-bottom: 1px solid var(--border);
}
h2 {
  font-size: 16px;
  font-weight: 500;
  margin: 0;
}
.icon-button {
  border: 0;
  background: none;
  color: var(--muted);
  cursor: pointer;
}
.body {
  padding: 24px 28px;
  overflow: auto;
}
.scope {
  font-size: 12px;
  line-height: 1.5;
  color: var(--muted);
  margin: 22px 0 0;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  border-top: 1px solid var(--border);
  padding: 14px 28px;
}
.inline-error {
  overflow-wrap: anywhere;
}
button:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
