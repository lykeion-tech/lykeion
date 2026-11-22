<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { githubIconPath } from '../../../../../../packages/cli-icons';
import { useGitHub, useGitHubMutations } from '../../../api/library';
import { useMembers, useWorkspace, useWorkspaces } from '../../../api/queries';
import { selectWorkspace, workspaceId } from '../../../api/session';

const github = useGitHub();
const { connectGitHub, disconnectGitHub, setGitHubAutoLink, retryGitHubImport } =
  useGitHubMutations();
const workspaces = useWorkspaces();
const members = useMembers();
const workspace = useWorkspace();
const owner = computed(
  () => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role === 'owner',
);
const prefix = computed(() => workspace.data.value?.display_prefix);
const settings = github.data;
const connections = computed(() => settings.value?.connections ?? []);
const description = computed(() => {
  const example = prefix.value ? ` (like ${prefix.value}-12)` : '';
  const closing = prefix.value ? `Closes ${prefix.value}-12` : 'Closes followed by the study ID';
  return `Links a pull request to a study when its branch or title contains the study's ID${example}, or its description says ${closing}.`;
});
const error = ref('');
// Older servers reported success without identifying the initiating workspace.
const completed = ref(false);
const callbackWorkspace = ref('');
const connectedWorkspace = computed(() =>
  workspaces.data.value?.find((w) => w.id === callbackWorkspace.value),
);
const busy = ref(false);
const continuation = ref('');
const existingForm = ref(false);
const installationUrl = ref('');
const route = useRoute();
const router = useRouter();
watch(
  () => route.query,
  ({ github: result, github_error: problem, github_workspace: target, ...rest }) => {
    if (result === undefined && problem === undefined && target === undefined) return;
    completed.value = result === 'connected' && problem === undefined;
    callbackWorkspace.value = completed.value && typeof target === 'string' ? target : '';
    if (typeof problem === 'string') error.value = problem;
    else error.value = '';
    void router.replace({ path: route.path, query: rest, hash: route.hash });
  },
  { immediate: true },
);

function memberName(id: string) {
  const member = members.data.value?.find((m) => m.user_id === id);
  return member?.name || member?.email || 'Workspace member';
}
let visit = 0;
watch(
  workspaceId,
  () => {
    visit++;
    existingForm.value = false;
    installationUrl.value = '';
    continuation.value = '';
    error.value = '';
    completed.value = false;
    callbackWorkspace.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
onScopeDispose(() => {
  visit++;
});
async function act(operation: () => Promise<void>) {
  if (!owner.value || busy.value) return;
  const started = visit;
  busy.value = true;
  error.value = '';
  continuation.value = '';
  try {
    await operation();
  } catch (e) {
    if (started === visit) error.value = (e as Error).message;
  } finally {
    if (started === visit) busy.value = false;
  }
}
async function connect(existing?: string) {
  const started = visit;
  const workspace = workspaceId.value;
  const { url } = await connectGitHub(existing);
  if (started !== visit || workspace !== workspaceId.value || !owner.value) return;
  const target = new URL(url);
  const validPath =
    existing === undefined
      ? /^\/apps\/[\w-]+\/installations\/new$/.test(target.pathname)
      : target.pathname === '/login/oauth/authorize';
  if (target.origin !== 'https://github.com' || target.username || target.password || !validPath)
    throw new Error('Invalid GitHub installation link.');
  if (window.lykeionDesktop) continuation.value = url;
  else window.location.assign(url);
}
function disconnect(id: number, login: string) {
  if (confirm(`Disconnect ${login} from this workspace? Linked pull requests will be kept.`))
    return act(() => disconnectGitHub(id));
}
</script>
<template>
  <section aria-label="GitHub settings">
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <p v-if="completed" class="type-label" role="status">
      <template v-if="connectedWorkspace">
        GitHub connected to {{ connectedWorkspace.name }}.
        <button
          v-if="connectedWorkspace.id !== workspaceId"
          class="text-button"
          @click="selectWorkspace(connectedWorkspace.id)"
        >
          Open {{ connectedWorkspace.name }}
        </button>
      </template>
      <template v-else
        >GitHub setup completed. Check the workspace where you started setup.</template
      >
    </p>
    <p v-if="github.isError.value" class="inline-error" role="alert">
      Could not load GitHub settings: {{ github.error.value?.message }}
      <button class="text-button" @click="github.refetch()">Retry</button>
    </p>
    <div class="setting-box">
      <div class="app-row" data-app="github">
        <svg viewBox="0 0 24 24" width="24" height="24" fill="currentColor" aria-hidden="true">
          <path :d="githubIconPath" />
        </svg>
        <span class="name"
          >GitHub
          <small v-if="settings?.configured === false"
            >GitHub isn't configured on this server.</small
          >
          <small v-else-if="settings && !connections.length">Not connected</small>
          <small v-else-if="github.isPending.value" role="status">Loading…</small>
        </span>
        <button
          v-if="owner && settings?.configured"
          class="button"
          :disabled="busy"
          @click="act(connect)"
        >
          {{ connections.length ? 'Add account or organization' : 'Connect GitHub' }}
        </button>
      </div>
      <div v-if="owner && settings?.configured" class="app-row">
        <button
          class="text-button"
          :aria-expanded="existingForm"
          :disabled="busy"
          @click="existingForm = !existingForm"
        >
          Connect existing installation
        </button>
        <form
          v-if="existingForm"
          class="installation-form"
          @submit.prevent="act(() => connect(installationUrl.trim()))"
        >
          <label for="github-installation-url">GitHub installation settings URL</label>
          <input
            id="github-installation-url"
            type="url"
            required
            :value="installationUrl"
            @input="installationUrl = ($event.target as HTMLInputElement).value"
          />
          <button class="button" :disabled="busy || !installationUrl.trim()">
            Authorize installation
          </button>
        </form>
      </div>
      <div v-if="owner && continuation" class="app-row">
        <span class="name"
          ><small>Sign in to Lykeion in your browser with the same owner account.</small></span
        >
        <a class="button" :href="continuation" target="_blank" rel="noopener noreferrer"
          >Continue to GitHub</a
        >
      </div>
      <div v-for="c in connections" :key="c.installation_id" class="app-row">
        <span class="name"
          >Connected · {{ c.account_login }} · by {{ memberName(c.connected_by) }}
          <small v-if="c.status === 'suspended'">Suspended on GitHub</small>
          <small v-if="['pending', 'running'].includes(c.import_state)"
            >Importing open pull requests…</small
          >
          <small v-else-if="c.import_state === 'failed'">Open pull request import failed.</small>
        </span>
        <button
          v-if="owner && c.status === 'active' && c.import_state === 'failed'"
          class="button"
          :disabled="busy"
          @click="act(() => retryGitHubImport(c.installation_id))"
        >
          Retry import
        </button>
        <button
          v-if="owner"
          class="button"
          :aria-label="`Disconnect ${c.account_login}`"
          :disabled="busy"
          @click="disconnect(c.installation_id, c.account_login)"
        >
          Disconnect
        </button>
      </div>
    </div>
    <div v-if="settings" class="setting-box">
      <div class="app-row">
        <span class="name"
          >Auto-link pull requests<small id="github-auto-link-description">{{
            description
          }}</small></span
        >
        <button
          class="auto-link"
          role="switch"
          aria-label="Auto-link pull requests"
          aria-describedby="github-auto-link-description"
          :aria-checked="settings.auto_link === true"
          :disabled="!owner || busy || typeof settings.auto_link !== 'boolean'"
          @click="act(() => setGitHubAutoLink(!settings!.auto_link))"
        >
          <span />
        </button>
      </div>
    </div>
  </section>
</template>
<style scoped src="./integration-settings.css"></style>
<style scoped>
.setting-box + .setting-box {
  margin-top: 16px;
}
.app-row + .app-row {
  border-top: 1px solid var(--border);
}
.name {
  overflow-wrap: anywhere;
}
.app-row {
  flex-wrap: wrap;
}
.app-row .name {
  flex: 1 1 150px;
}
.installation-form {
  display: grid;
  gap: 6px;
  flex: 1 1 220px;
  min-width: 0;
}
.installation-form input {
  min-width: 0;
}
.installation-form .button {
  justify-self: start;
}
.auto-link {
  width: 32px;
  height: 20px;
  padding: 2px;
  border-radius: 10px;
  border: 1px solid var(--muted);
  background: var(--input);
  color: var(--muted);
  cursor: pointer;
}
.auto-link span {
  display: block;
  height: 14px;
  width: 14px;
  border-radius: 50%;
  background: currentColor;
}
.auto-link[aria-checked='true'] {
  background: var(--text);
  color: var(--surface);
  border-color: var(--text);
}
.auto-link[aria-checked='true'] span {
  margin-left: auto;
}
.auto-link:disabled {
  opacity: 0.65;
  cursor: default;
}
</style>
