<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import Modal from '../../../ui/Modal.vue';
import Icon from '../../../ui/Icon.vue';
import SelectPicker from '../../../ui/SelectPicker.vue';
import { useAgents } from '../../../api/queries';
import { useSlackMutations } from '../../../api/library';
import { workspaceId } from '../../../api/session';
import { notify } from '../../../state/notice';
import { slackManifest } from '../slack-manifest';

const props = defineProps<{ mode: 'connect' | 'manage'; agentId?: string | null }>();
const emit = defineEmits<{ close: [] }>();
const agents = useAgents();
const mutations = useSlackMutations();
// The server re-validates: workspace-visible, not archived, not Aristotle, has a stage.
const usable = computed(() =>
  (agents.data.value ?? []).filter(
    (a) =>
      a.visibility === 'workspace' && !a.archived_at && a.built_in_key !== 'aristotle' && a.stage,
  ),
);
const botToken = ref(''),
  appToken = ref(''),
  agent = ref(props.agentId ?? ''),
  busy = ref(false),
  error = ref('');
const manifest = slackManifest();
const steps = [
  'At api.slack.com/apps choose Create New App, then From a manifest, and paste this manifest.',
  'Install the app to your Slack workspace and copy its Bot User OAuth Token.',
  'Under Basic Information, create an App-Level Token with the connections:write scope.',
  'Paste both tokens below. They are sealed on the server and cannot be read back.',
];
let visit = 0;
const ws = workspaceId.value;
onUnmounted(() => visit++);
const current = (token: number) => token === visit && ws === workspaceId.value;
async function run(work: () => Promise<unknown>, done: string) {
  if (busy.value) return;
  const token = visit;
  busy.value = true;
  error.value = '';
  try {
    await work();
    if (!current(token)) return;
    // Tokens never outlive the request that carried them.
    botToken.value = '';
    appToken.value = '';
    notify(done);
    emit('close');
  } catch (e) {
    if (current(token)) error.value = (e as Error).message;
  } finally {
    if (current(token)) busy.value = false;
  }
}
const connect = () =>
  run(
    () =>
      mutations.connectSlack({
        bot_token: botToken.value.trim(),
        app_token: appToken.value.trim(),
        drafting_agent_id: agent.value,
      }),
    'Slack connected.',
  );
const save = () => run(() => mutations.updateSlack(agent.value), 'Drafting agent saved.');
function disconnect() {
  if (!confirm('Disconnect Slack? People in Slack can no longer reach Aristotle.')) return;
  void run(() => mutations.disconnectSlack(), 'Slack disconnected.');
}
async function copy() {
  try {
    await navigator.clipboard.writeText(manifest);
    notify('Copied.');
  } catch {
    error.value = 'Could not access your clipboard.';
  }
}
const ready = computed(
  () => !!agent.value && (props.mode === 'manage' || (botToken.value.trim() && appToken.value.trim())),
);
</script>
<template>
  <Modal
    v-slot="{ close }"
    :title="mode === 'connect' ? 'Connect Slack' : 'Manage Slack'"
    @close="emit('close')"
  >
    <section class="slack-dialog">
      <header>
        <h2>{{ mode === 'connect' ? 'Connect Slack' : 'Manage Slack' }}</h2>
        <button class="icon-button" aria-label="Close" @click="close">
          <Icon name="close" :size="18" />
        </button>
      </header>
      <div class="body">
        <ol v-if="mode === 'connect'" class="steps">
          <li v-for="(step, i) in steps" :key="i">
            {{ step }}
            <div v-if="i === 0" class="manifest">
              <textarea readonly rows="8" aria-label="Slack app manifest" :value="manifest" />
              <button class="button" @click="copy"><Icon name="copy" :size="14" />Copy</button>
            </div>
          </li>
        </ol>
        <template v-if="mode === 'connect'">
          <label class="field"
            >Bot token (xoxb-)<input
              type="password"
              aria-label="Bot token (xoxb-)"
              autocomplete="new-password"
              placeholder="xoxb-…"
              :value="botToken"
              :disabled="busy"
              @input="botToken = ($event.target as HTMLInputElement).value"
          /></label>
          <label class="field"
            >App-level token (xapp-)<input
              type="password"
              aria-label="App-level token (xapp-)"
              autocomplete="new-password"
              placeholder="xapp-…"
              :value="appToken"
              :disabled="busy"
              @input="appToken = ($event.target as HTMLInputElement).value"
          /></label>
        </template>
        <label class="field"
          >Drafting agent<SelectPicker
            ><select
              aria-label="Drafting agent"
              :value="agent"
              :disabled="busy"
              @change="agent = ($event.target as HTMLSelectElement).value"
            >
              <option value="" disabled>Choose an agent</option>
              <option v-for="a in usable" :key="a.id" :value="a.id">{{ a.name }}</option>
            </select></SelectPicker
          ></label
        >
        <p class="hint">The agent that drafts studies from /study in Slack.</p>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      </div>
      <footer>
        <button
          v-if="mode === 'manage'"
          class="button danger"
          :disabled="busy"
          @click="disconnect"
        >
          Disconnect
        </button>
        <span class="spacer" />
        <button class="button" @click="close">Cancel</button>
        <button
          v-if="mode === 'connect'"
          class="button primary"
          :disabled="busy || !ready"
          @click="connect"
        >
          Connect
        </button>
        <button v-else class="button primary" :disabled="busy || !ready" @click="save">Save</button>
      </footer>
    </section>
  </Modal>
</template>
<style scoped>
.slack-dialog {
  width: 620px;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
}
header,
footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 20px;
}
header {
  justify-content: space-between;
}
header h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
}
footer {
  border-top: 1px solid var(--border);
}
.spacer {
  flex: 1;
}
.body {
  padding: 0 20px 16px;
  overflow-y: auto;
  display: grid;
  gap: 12px;
  font-size: 14px;
}
.steps {
  margin: 0;
  padding-left: 20px;
  display: grid;
  gap: 8px;
  line-height: 20px;
}
.manifest {
  display: grid;
  gap: 6px;
  justify-items: start;
  margin-top: 6px;
}
.manifest textarea {
  width: 100%;
  font: 12px/16px ui-monospace, monospace;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 8px;
  box-sizing: border-box;
}
.field {
  display: grid;
  gap: 6px;
}
.field input {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 7px;
  font: inherit;
  padding: 6px 10px;
  height: 32px;
  min-width: 0;
}
.hint {
  color: var(--muted);
  font-size: 13px;
  margin: 0;
}
</style>
