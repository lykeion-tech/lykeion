<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import Modal from '../../Modal.vue';
import Icon from '../../Icon.vue';
import ConnectorConfigFields, {
  emptyConnector,
  connectorInput,
  connectorProblem,
} from './ConnectorConfigFields.vue';
import {
  useConnectorLibrary,
  useWorkspaceMCPServers,
  useLibraryMutations,
} from '../../../api/library';
import { useMe, useMembers, useWorkspaces } from '../../../api/queries';
import { workspaceId } from '../../../api/session';
import { ApiError } from '../../../api/client';
import { useResourceDraft } from '../../../state/resource-draft';
const props = defineProps<{ kind: 'catalogue' | 'custom'; connectorId: string }>();
const emit = defineEmits<{ close: [] }>();
const catalogue = useConnectorLibrary(),
  custom = useWorkspaceMCPServers(),
  workspaces = useWorkspaces(),
  members = useMembers(),
  me = useMe();
const entry = computed(() => catalogue.data.value?.find((c) => c.name === props.connectorId));
const server = computed(() => custom.data.value?.find((c) => c.id === props.connectorId));
const row = computed(() => (props.kind === 'catalogue' ? entry.value : server.value));
const role = computed(() => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role);
const allowed = computed(() =>
  props.kind === 'catalogue'
    ? role.value === 'owner'
    : role.value === 'owner' || role.value === 'admin',
);
const query = computed(() => (props.kind === 'catalogue' ? catalogue : custom));
const tab = ref<'overview' | 'configuration'>('overview'),
  editing = ref(false),
  deleting = ref(false),
  busy = ref(false),
  error = ref(''),
  refused = ref(false);
const identity = computed(() => `${workspaceId.value}:${props.kind}:${props.connectorId}`);
const value = () => ({
  enabled: entry.value?.policy_enabled ?? true,
  key: '',
  clearKey: false,
  licence: entry.value?.licence_confirmed ?? false,
  custom: emptyConnector(server.value?.name, server.value?.transport),
});
const saved = computed(() =>
  row.value && allowed.value && !query.value.isError.value && !refused.value
    ? { revision: row.value.revision, value: value() }
    : undefined,
);
const form = useResourceDraft(identity, saved);
const fields = computed({
  get: () => form.draft.value?.custom ?? emptyConnector(),
  set: (value) => {
    if (form.draft.value) form.draft.value.custom = value;
  },
});
const problem = computed(() =>
  props.kind === 'custom' && fields.value ? connectorProblem(fields.value) : '',
);
const author = computed(() => {
  const id = server.value?.created_by;
  if (id === me.data.value?.id) return 'You';
  const person = members.data.value?.find((m) => m.user_id === id);
  return person?.name || person?.email || id;
});
let visit = 0,
  refusalSnapshot = row.value;
onUnmounted(() => visit++);
watch([row, allowed], ([snapshot, permission]) => {
  if (
    refused.value &&
    snapshot &&
    snapshot !== refusalSnapshot &&
    permission &&
    !query.value.isError.value
  )
    refused.value = false;
});
watch(allowed, async (permission, previous) => {
  if (!permission || previous || !refused.value) return;
  const own = identity.value,
    token = visit;
  const fresh = await query.value.refetch();
  if (own === identity.value && token === visit && allowed.value && !fresh.isError && row.value)
    refused.value = false;
});
const mutations = useLibraryMutations();
async function save() {
  if (!form.canSave.value || !allowed.value || busy.value || problem.value) return;
  const accept = form.adopt(),
    own = identity.value,
    token = visit,
    revision = form.baseRevision.value!,
    draft = form.draft.value!;
  busy.value = true;
  error.value = '';
  try {
    const response =
      props.kind === 'catalogue'
        ? await mutations.setConnector(props.connectorId, {
            revision,
            enabled: draft.enabled,
            ...(entry.value?.tier === 'restricted' ? { licence_confirmed: draft.licence } : {}),
            ...(draft.clearKey ? { key: '' } : draft.key ? { key: draft.key } : {}),
          })
        : await mutations.updateWorkspaceMCP(props.connectorId, {
            revision,
            ...connectorInput(draft.custom),
          });
    if (
      own === identity.value &&
      token === visit &&
      accept({ revision: response.revision, value: value() })
    ) {
      if (!form.dirty.value) editing.value = false;
    }
  } catch (e) {
    if (own !== identity.value || token !== visit) return;
    error.value = (e as Error).message;
    if (e instanceof ApiError && [403, 404].includes(e.status)) {
      refusalSnapshot = row.value;
      refused.value = true;
      await workspaces.refetch();
    }
    if (e instanceof ApiError && [409, 403, 404].includes(e.status)) {
      const fresh = await query.value.refetch();
      if (own === identity.value && token === visit && !fresh.isError && allowed.value && row.value)
        refused.value = false;
    }
  } finally {
    if (own === identity.value && token === visit) busy.value = false;
  }
}
async function remove(close: () => Promise<void>) {
  if (!allowed.value || busy.value || !server.value) return;
  if (!(await form.reset())) return;
  const own = identity.value,
    token = visit;
  busy.value = true;
  error.value = '';
  try {
    await mutations.deleteWorkspaceMCP(props.connectorId, server.value.revision);
    if (own === identity.value && token === visit) await close();
  } catch (e) {
    if (own === identity.value && token === visit) {
      error.value = (e as Error).message;
      await query.value.refetch();
      await workspaces.refetch();
    }
  } finally {
    if (own === identity.value && token === visit) busy.value = false;
  }
}
async function cancelEdit() {
  if (await form.reset()) editing.value = false;
}
</script>
<template>
  <Modal
    class="connector-modal"
    :title="`${row?.name ?? connectorId} connector`"
    :before-close="async () => !form.dirty.value || (await form.reset())"
    v-slot="{ close }"
    @close="emit('close')"
    ><section class="connector-detail">
      <header>
        <div class="title">
          <h2>{{ row?.name ?? connectorId }}</h2>
          <button class="icon-button" aria-label="Close connector" @click="close">
            <Icon name="close" :size="18" />
          </button>
        </div>
        <p v-if="entry && kind === 'catalogue'" class="lede">{{ entry.purpose }}</p>
        <div class="chips">
          <span>{{ kind === 'catalogue' ? entry?.domain : 'Custom' }}</span
          ><span>{{
            kind === 'catalogue'
              ? entry?.tier
              : server?.transport === 'url'
                ? 'Remote URL'
                : 'Command'
          }}</span>
        </div>
      </header>
      <nav class="tabs" role="tablist" aria-label="Connector details">
        <button
          v-for="id in ['overview', 'configuration'] as const"
          :key="id"
          role="tab"
          :aria-selected="tab === id"
          @click="tab = id"
        >
          {{ id === 'overview' ? 'Overview' : 'Configuration' }}
        </button>
      </nav>
      <div class="body">
        <p v-if="query.isPending.value" role="status">Loading connector…</p>
        <p v-if="query.error.value" role="alert">{{ query.error.value.message }}</p>
        <p v-if="!query.isPending.value && !row" role="alert">
          This connector is no longer available. Your draft is preserved.
        </p>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <template v-if="tab === 'overview' && row"
          ><dl class="facts">
            <template v-if="kind === 'catalogue' && entry"
              ><dt>Availability</dt>
              <dd>{{ entry.enabled ? 'Available to this workspace' : entry.reason }}</dd>
              <dt>Origin</dt>
              <dd>Lykeion scientific catalogue</dd>
              <dt>Source terms</dt>
              <dd>
                <a v-if="entry.terms" :href="entry.terms" target="_blank" rel="noreferrer"
                  >View source terms ↗</a
                >
              </dd></template
            ><template v-else
              ><dt>Origin</dt>
              <dd>Added to this workspace</dd>
              <dt>Added by</dt>
              <dd>{{ author }}</dd>
              <dt>Connection</dt>
              <dd>{{ server?.transport === 'url' ? 'Remote URL' : 'Command' }}</dd></template
            >
            <dt>Scope</dt>
            <dd>Workspace</dd>
          </dl>
          <p class="hint scope">
            {{
              kind === 'catalogue'
                ? 'Available connectors can be selected for Teams and routed to agents.'
                : 'Agents can use this connection through the recorded tool door.'
            }}
          </p></template
        ><template v-if="tab === 'configuration'"
          ><p v-if="!allowed || refused" class="hint" role="status">
            {{
              kind === 'catalogue'
                ? 'Only the workspace owner can change catalogue settings.'
                : 'Only workspace owners and admins can change custom connectors.'
            }}<template v-if="form.dirty.value"> Your draft is preserved.</template>
          </p>
          <template v-if="kind === 'catalogue' && entry"
            ><div class="setting">
              <div>
                <b>Workspace availability</b>
                <p class="hint">
                  {{ entry.enabled ? 'Available to this workspace' : entry.reason }}
                </p>
                <p class="hint">Availability reflects workspace policy and required setup.</p>
              </div>
              <input
                :checked="form.draft.value?.enabled ?? entry.policy_enabled"
                type="checkbox"
                role="switch"
                aria-label="Enable connector"
                :disabled="!allowed || busy || refused"
                @change="
                  form.draft.value &&
                  (form.draft.value.enabled = ($event.target as HTMLInputElement).checked)
                "
              />
            </div>
            <template v-if="entry.takes_key"
              ><label class="field"
                >API key<input
                  v-if="form.draft.value"
                  v-model="form.draft.value.key"
                  type="password"
                  aria-label="API key"
                  autocomplete="new-password"
                  placeholder="Enter an API key"
                  :disabled="!allowed || busy || refused"
                  @input="form.draft.value.clearKey = false"
                />
                <p class="hint">{{ entry.key_set ? 'Key saved' : 'No key saved' }}</p>
                <p v-if="!entry.key_set && entry.enabled" class="hint">Available without a key.</p>
                <p class="hint">Keys are sealed on the server and cannot be read back.</p></label
              ><button
                v-if="entry.key_set && allowed && form.draft.value"
                class="link"
                :disabled="busy || refused"
                @click="
                  form.draft.value.clearKey = true;
                  form.draft.value.key = '';
                "
              >
                Clear saved key
              </button>
              <p v-if="form.draft.value?.clearKey" class="hint">
                The saved key will be cleared when you save.
              </p></template
            ><label v-if="entry.tier === 'restricted'" class="check"
              ><input
                :checked="form.draft.value?.licence ?? entry.licence_confirmed"
                type="checkbox"
                :disabled="!allowed || busy || refused"
                @change="
                  form.draft.value &&
                  (form.draft.value.licence = ($event.target as HTMLInputElement).checked)
                "
              />I confirm this workspace is entitled to use this source under its licence.</label
            ><a
              v-if="entry.terms"
              class="terms"
              :href="entry.terms"
              target="_blank"
              rel="noreferrer"
              >View source terms ↗</a
            ></template
          ><template v-else-if="server"
            ><template v-if="editing && fields"
              ><p class="hint replacement">
                Saved configuration is write-only. Re-enter the complete configuration, including
                every header, argument and environment value you want to keep.
              </p>
              <ConnectorConfigFields
                v-model="fields"
                :disabled="!allowed || busy || refused" /></template
            ><template v-else
              ><p class="hint">
                Saved configuration is write-only. Replacing it requires re-entering the complete
                connection.
              </p>
              <button v-if="allowed" class="button edit" @click="editing = true">
                Edit configuration
              </button></template
            >
            <div v-if="deleting" class="delete-confirm">
              <p>Delete {{ server.name }} from this workspace? The remote server is not deleted.</p>
              <button class="button" @click="deleting = false">Keep connector</button
              ><button class="button danger" :disabled="busy || !allowed" @click="remove(close)">
                Delete connector
              </button>
            </div>
            <button v-else-if="allowed && !editing" class="link delete" @click="deleting = true">
              Delete
            </button></template
          >
          <div v-if="form.stale.value" class="draft-warning" role="status">
            <p>Saved configuration changed. Your draft is preserved.</p>
            <button class="button" @click="form.reset">Reload saved version</button>
          </div></template
        >
      </div>
      <footer>
        <button v-if="editing" class="button" @click="cancelEdit">Cancel</button
        ><button v-else class="button" @click="close">Close</button
        ><button
          v-if="tab === 'configuration' && allowed && (kind === 'catalogue' || editing)"
          class="button primary"
          :disabled="!form.canSave.value || busy || !!problem || refused"
          :title="problem"
          @click="save"
        >
          Save changes
        </button>
      </footer>
    </section></Modal
  >
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
  padding: 24px;
  overflow: auto;
  min-height: 250px;
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
  color: var(--primary);
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
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
