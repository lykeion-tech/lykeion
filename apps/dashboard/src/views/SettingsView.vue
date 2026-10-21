<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { themes } from '../theme';
import Icon from '../components/Icon.vue';
import StoragePanel from '../components/settings/StoragePanel.vue';
import AnalyticsPanel from '../components/settings/AnalyticsPanel.vue';
import SkillPanel from '../components/settings/library/SkillPanel.vue';
import ConnectorPanel from '../components/settings/library/ConnectorPanel.vue';
import TokenCreationForm from '../components/settings/TokenCreationForm.vue';
import TimeZonePicker from '../components/settings/TimeZonePicker.vue';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useMe, useTokens, useWorkspace, useWorkspaces } from '../api/queries';
import { careerStages, type NewToken, type Workspace } from '../api/types';
import { careerStageLabels } from '../lib/studies';
import { matchesQuery } from '../lib/search';
import { notify } from '../state/notice';
import { usePreferences } from '../state/preferences';

const router = useRouter();
const props = defineProps<{ section: string }>();
const client = useQueryClient();
const me = useMe();
const workspace = useWorkspace();
const workspaces = useWorkspaces();
const tokens = useTokens();
const prefs = usePreferences();
const section = computed(() => (props.section === 'workspace' ? 'general' : props.section));
const query = ref('');
const error = ref('');
const pending = ref(0);
const busy = computed(() => pending.value > 0);
const name = ref('');
const created = ref<NewToken | null>(null);
const wsName = ref('');
const description = ref('');
const context = ref('');
const prefixEditing = ref(false);
const prefix = ref('');
const logoInput = ref<HTMLInputElement>();
let generation = 0;
let baseline: Workspace | undefined;
const myRole = computed(() => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role);
const isAdmin = computed(() => myRole.value === 'owner' || myRole.value === 'admin');
const groups = computed(() =>
  [
    {
      title: 'Personal',
      suffix: me.data.value?.email.split('@')[0] ?? '',
      items: [
        { id: 'profile', label: 'Profile', icon: 'users' },
        { id: 'preferences', label: 'Preferences', icon: 'filters' },
        { id: 'tokens', label: 'API Tokens', icon: 'link' },
        { id: 'storage', label: 'Storage', icon: 'layers' },
      ],
    },
    {
      title: workspace.data.value?.name ?? 'Workspace',
      suffix: 'Workspace',
      items: [
        { id: 'general', label: 'General', icon: 'settings' },
        { id: 'skills', label: 'Skills', icon: 'book' },
        { id: 'connectors', label: 'Connectors', icon: 'link' },
      ],
    },
  ]
    .map((g) => ({ ...g, items: g.items.filter((i) => matchesQuery(i.label, query.value)) }))
    .filter((g) => g.items.length),
);
const titles: Record<string, string> = {
  profile: 'Profile',
  preferences: 'Preferences',
  tokens: 'API Tokens',
  storage: 'Storage',
  general: 'General',
  skills: 'Skills',
  connectors: 'Connectors',
};
function open(id: string) {
  void router.push({ path: '/settings', query: { tab: id } });
}
function hydrate(w: Workspace | undefined, reset = false) {
  const previous = reset ? undefined : baseline;
  if (!previous || wsName.value === previous.name) wsName.value = w?.name ?? '';
  if (!previous || description.value === previous.description)
    description.value = w?.description ?? '';
  if (!previous || context.value === previous.research_context)
    context.value = w?.research_context ?? '';
  if (!previous || prefix.value === previous.card_prefix) prefix.value = w?.card_prefix ?? '';
  baseline = w;
}
watch(
  [workspaceId, section, () => me.data.value?.id],
  () => {
    generation++;
    error.value = '';
    pending.value = 0;
    created.value = null;
    prefixEditing.value = false;
    name.value = me.data.value?.name ?? '';
    hydrate(
      workspace.data.value?.id === workspaceId.value ? workspace.data.value : undefined,
      true,
    );
  },
  { immediate: true, flush: 'sync' },
);
watch(
  () => workspace.data.value,
  (w) => {
    if (w?.id === workspaceId.value) hydrate(w);
  },
);
watch(
  () => me.data.value?.name,
  (value, old) => {
    if (name.value === (old ?? '')) name.value = value ?? '';
  },
  { immediate: true },
);
onUnmounted(() => {
  generation++;
  created.value = null;
});
function scope() {
  const workspace = workspaceId.value,
    visit = generation,
    account = me.data.value?.id;
  return {
    workspace,
    account,
    current: () =>
      generation === visit && workspaceId.value === workspace && me.data.value?.id === account,
  };
}
type Scope = ReturnType<typeof scope>;
async function save(fn: (own: Scope) => Promise<void>, done?: string, concurrent = false) {
  if (busy.value && !concurrent) return;
  const own = scope();
  pending.value++;
  error.value = '';
  try {
    await fn(own);
    if (own.current() && done) notify(done);
  } catch (e) {
    if (own.current()) error.value = (e as Error).message;
  } finally {
    if (own.current()) pending.value--;
  }
}
// Account queries remain current across settings tabs; local drafts belong to a visit.
const refreshAccount = (own: Scope, key: readonly string[]) =>
  own.account && me.data.value?.id === own.account
    ? client.invalidateQueries({ queryKey: key })
    : Promise.resolve();
function patchMe(body: Record<string, unknown>) {
  const submittedName = name.value;
  return save(
    async (own) => {
      await api('PATCH', '/api/me', body);
      await refreshAccount(own, keys.me);
      if (
        own.current() &&
        typeof body.name === 'string' &&
        name.value === submittedName &&
        me.data.value
      )
        name.value = me.data.value.name;
    },
    undefined,
    true,
  );
}
function changeCareer(el: HTMLSelectElement) {
  const requested = el.value;
  el.value = me.data.value?.career_stage ?? '';
  void patchMe({ career_stage: requested });
}
const timezone = computed({
  get: () => me.data.value?.timezone ?? 'UTC',
  set: (zone: string) => void patchMe({ timezone: zone }),
});
const createToken = (request: { name: string }) => {
  const body = { name: request.name };
  return save(async (own) => {
    const token = await api<NewToken>('POST', '/api/tokens', body);
    if (own.current()) created.value = token;
    await refreshAccount(own, keys.tokens);
  });
};
const revoke = (id: string) =>
  save(async (own) => {
    await api('DELETE', `/api/tokens/${id}`);
    if (own.current() && created.value?.id === id) created.value = null;
    await refreshAccount(own, keys.tokens);
  });
const liveTokens = computed(() => (tokens.data.value ?? []).filter((t) => !t.revoked_at));
const day = (iso: string) =>
  new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
async function copy(text: string) {
  const own = scope();
  try {
    await navigator.clipboard.writeText(text);
    if (own.current()) notify('Copied.');
  } catch {
    if (own.current()) error.value = 'Could not access your clipboard.';
  }
}
const dirty = computed(
  () =>
    !!workspace.data.value &&
    (wsName.value !== workspace.data.value.name ||
      description.value !== workspace.data.value.description ||
      context.value !== workspace.data.value.research_context),
);
const refreshWorkspace = (ws: string) =>
  Promise.all([
    client.invalidateQueries({ queryKey: keys.workspace(ws) }),
    client.invalidateQueries({ queryKey: keys.workspaces }),
  ]);
function saveDetails() {
  const submittedName = wsName.value;
  const body = {
    name: submittedName.trim(),
    description: description.value,
    research_context: context.value,
  };
  return save(async (own) => {
    await api('PATCH', `/api/workspaces/${own.workspace}`, body);
    if (!own.current()) return;
    await refreshWorkspace(own.workspace);
    if (
      own.current() &&
      wsName.value === submittedName &&
      workspace.data.value?.id === own.workspace
    )
      wsName.value = workspace.data.value.name;
  }, 'Workspace saved.');
}
async function savePrefix() {
  if (busy.value) return;
  if (prefix.value && !/^[A-Z]{2,6}$/.test(prefix.value)) {
    error.value = 'A card prefix is two to six capital letters, or empty to follow the URL.';
    return;
  }
  const value = prefix.value;
  await save(async (own) => {
    await api('PATCH', `/api/workspaces/${own.workspace}`, { card_prefix: value });
    if (!own.current()) return;
    await refreshWorkspace(own.workspace);
    if (own.current() && prefix.value === value) prefixEditing.value = false;
  }, 'Card prefix saved.');
}
async function logoChanged(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  input.value = '';
  if (!file || busy.value) return;
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 512 * 1024) {
    error.value = 'Choose a PNG, JPEG or WebP of at most 512 KB.';
    return;
  }
  await save(async (own) => {
    await api('PUT', `/api/workspaces/${own.workspace}/logo`, undefined, {
      data: file,
      type: file.type,
    });
    if (own.current()) await refreshWorkspace(own.workspace);
  });
}
async function leaveOrDelete(kind: 'leave' | 'delete') {
  if (busy.value) return;
  const label = workspace.data.value?.name ?? 'this workspace';
  if (
    !confirm(
      kind === 'leave'
        ? `Leave ${label}? You lose access until someone invites you again.`
        : `Delete ${label} and every study in it? This cannot be undone.`,
    )
  )
    return;
  await save(async (own) => {
    await api(
      kind === 'leave' ? 'POST' : 'DELETE',
      `/api/workspaces/${own.workspace}${kind === 'leave' ? '/leave' : ''}`,
    );
    if (!own.current()) return;
    await client.cancelQueries({ queryKey: keys.ws(own.workspace) });
    if (!own.current()) return;
    workspaceId.value = '';
    const transition = generation;
    await nextTick();
    if (generation !== transition || workspaceId.value) return;
    // Keep the shell's mounted observers while it selects a remaining workspace.
    const refreshed = client.resetQueries({ queryKey: keys.workspaces });
    void router.replace('/studies');
    await refreshed;
  });
}
async function copySlug() {
  await copy(workspace.data.value?.slug ?? '');
}
</script>
<template>
  <div class="desktop-settings">
    <nav class="settings-rail" aria-label="Settings">
      <h1>Settings</h1>
      <select
        class="settings-mobile-navigation"
        aria-label="Settings section"
        :value="section"
        @change="open(($event.target as HTMLSelectElement).value)"
      >
        <optgroup v-for="group in groups" :key="group.title" :label="group.title">
          <option v-for="item in group.items" :key="item.id" :value="item.id">
            {{ item.label }}
          </option>
        </optgroup>
      </select>
      <label class="settings-search"
        ><Icon name="search" :size="16" /><input
          v-model="query"
          type="search"
          placeholder="Search settings"
          aria-label="Search settings"
      /></label>
      <section v-for="group in groups" :key="group.title">
        <header>
          <span class="tiny-avatar">{{ group.title.charAt(0) }}</span
          ><strong>{{ group.title }}</strong
          ><small>{{ group.suffix }}</small>
        </header>
        <button
          v-for="item in group.items"
          :key="item.id"
          :class="{ selected: section === item.id }"
          :aria-current="section === item.id ? 'page' : undefined"
          @click="open(item.id)"
        >
          <Icon :name="item.icon" />{{ item.label }}
        </button>
      </section>
      <p v-if="!groups.length" class="no-match">No matching settings.</p>
    </nav>
    <div class="settings-scroll">
      <section
        class="settings-body"
        :class="{ wide: section === 'preferences', 'table-layout': section === 'tokens' }"
      >
        <header v-if="section !== 'skills' && section !== 'connectors'" class="settings-heading">
          <h2>{{ titles[section] ?? 'Settings' }}</h2>
          <span v-if="section === 'general'" class="scope-badge"
            ><Icon name="users" :size="11" />{{ workspace.data.value?.name }} workspace</span
          >
        </header>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>

        <StoragePanel v-if="section === 'storage'" />
        <SkillPanel v-else-if="section === 'skills'" />
        <ConnectorPanel v-else-if="section === 'connectors'" />
        <template v-else-if="section === 'profile'">
          <h3>Personal information</h3>
          <div class="setting-box">
            <label class="setting-row"
              ><span>Name</span
              ><input
                v-model="name"
                class="profile-name-input"
                aria-label="Name"
                placeholder="Your name"
                maxlength="120"
                @change="patchMe({ name: name.trim() })"
            /></label>
            <div class="setting-row">
              <span>Email</span><span class="muted">{{ me.data.value?.email }}</span>
            </div>
            <label class="setting-row"
              ><span>Your career stage</span
              ><select
                :value="me.data.value?.career_stage ?? ''"
                aria-label="Your career stage"
                @change="changeCareer($event.target as HTMLSelectElement)"
              >
                <option value="">Choose a stage (optional)</option>
                <option v-for="c in careerStages" :key="c" :value="c">
                  {{ careerStageLabels[c] }}
                </option>
              </select></label
            >
          </div>
          <AnalyticsPanel />
        </template>

        <template v-else-if="section === 'preferences'">
          <h3>Language & region</h3>
          <div class="setting-box">
            <label class="setting-row"
              ><span>Time zone<small>Used for your dates and schedules.</small></span
              ><TimeZonePicker v-model="timezone" :automatic="false"
            /></label>
          </div>
          <h3>
            Appearance
            <span class="scope-badge"><Icon name="monitor" :size="11" />This device only</span>
          </h3>
          <div class="theme-grid">
            <button
              v-for="theme in themes"
              :key="theme.id"
              class="theme-card"
              :class="{ chosen: prefs.theme === theme.id }"
              :aria-pressed="prefs.theme === theme.id"
              :aria-label="theme.name"
              :style="{
                '--swatch-bg': theme.surface,
                '--swatch-input': theme.input,
                '--swatch-accent': theme.accent,
                '--swatch-text': theme.text,
              }"
              @click="prefs.theme = theme.id"
            >
              <div class="theme-preview">
                <div><i></i><i></i><span>Primary</span><b></b></div>
              </div>
              <footer>
                {{ theme.name }}<small>{{ theme.description }}</small
                ><Icon v-if="prefs.theme === theme.id" name="check" :size="14" />
              </footer>
            </button>
          </div>
        </template>

        <template v-else-if="section === 'tokens'">
          <p class="table-description">
            Personal <code>lyk_</code> tokens for scripts. <code>lyk login</code> makes its own. A
            token expires after 90 days.
          </p>
          <div class="tokens-body">
            <TokenCreationForm
              :available="true"
              :pending="busy"
              :show-expiry="false"
              @create="createToken"
            />
            <div v-if="created" class="setting-box new-token">
              <div class="setting-row">
                <span
                  >Copy it now. It won't be shown again.<code>{{ created.token }}</code></span
                ><button class="button compact" @click="copy(created.token)">
                  <Icon name="copy" :size="13" />Copy
                </button>
              </div>
            </div>
            <h3>Your tokens</h3>
            <table class="setting-box tokens-table" aria-label="Your tokens">
              <tr v-for="t in liveTokens" :key="t.id">
                <td>{{ t.name }}</td>
                <td class="muted">Created {{ day(t.created_at) }}</td>
                <td class="muted">Expires {{ day(t.expires_at) }}</td>
                <td>
                  <button class="text-button danger-text" :disabled="busy" @click="revoke(t.id)">
                    Revoke
                  </button>
                </td>
              </tr>
            </table>
            <p v-if="tokens.isSuccess.value && !liveTokens.length" class="muted">No tokens yet.</p>
          </div>
        </template>

        <template v-else-if="section === 'general'">
          <h3>Your career stage</h3>
          <div class="setting-box">
            <label class="setting-row"
              ><span>Your career stage</span
              ><select
                :value="me.data.value?.career_stage ?? ''"
                aria-label="Your career stage"
                @change="changeCareer($event.target as HTMLSelectElement)"
              >
                <option value="">Choose a stage (optional)</option>
                <option v-for="c in careerStages" :key="c" :value="c">
                  {{ careerStageLabels[c] }}
                </option>
              </select></label
            >
          </div>
          <h3 class="subsequent-heading">Workspace details</h3>
          <form @submit.prevent="saveDetails">
            <div class="setting-box">
              <div class="setting-row avatar-row">
                <div>Workspace logo<small>Click the logo to upload a new image</small></div>
                <input
                  ref="logoInput"
                  hidden
                  type="file"
                  accept="image/png,image/jpeg,image/webp"
                  @change="logoChanged"
                /><button
                  type="button"
                  class="profile-avatar"
                  aria-label="Change workspace logo"
                  :disabled="!isAdmin || busy"
                  @click="logoInput?.click()"
                >
                  <img
                    v-if="workspace.data.value?.logo_blob"
                    :src="`/api/workspaces/${workspaceId}/logo?v=${workspace.data.value.logo_blob}`"
                    alt="Workspace logo"
                  /><template v-else>{{ wsName.charAt(0).toUpperCase() || 'L' }}</template>
                </button>
              </div>
              <label class="setting-row"
                ><span>Name</span
                ><input
                  v-model="wsName"
                  aria-label="Workspace name"
                  required
                  :disabled="!isAdmin || busy" /></label
              ><label class="setting-row about-row"
                ><span>Description</span
                ><textarea
                  v-model="description"
                  aria-label="Workspace description"
                  placeholder="What does this workspace focus on?"
                  :disabled="!isAdmin || busy"
                /></label
              ><label class="setting-row about-row"
                ><span
                  >Research context<small
                    >Given to every agent in this workspace on every run.</small
                  ></span
                ><textarea
                  v-model="context"
                  aria-label="Research context"
                  :disabled="!isAdmin || busy"
                />
              </label>
              <div class="setting-row">
                <span>Slug<small>Part of every workspace URL. It can't be changed.</small></span>
                <div class="inline-setting">
                  <code>{{ workspace.data.value?.slug }}</code
                  ><button
                    type="button"
                    class="icon-button"
                    aria-label="Copy slug"
                    @click="copySlug"
                  >
                    <Icon name="copy" :size="14" />
                  </button>
                </div>
              </div>
              <div class="setting-row prefix">
                <span
                  >Card prefix<small
                    >Used in study numbers like
                    {{ workspace.data.value?.display_prefix }}-38.</small
                  ></span
                >
                <div class="inline-setting">
                  <template v-if="prefixEditing"
                    ><input
                      v-model="prefix"
                      aria-label="Card prefix"
                      maxlength="6"
                      placeholder="Follows the URL"
                      @input="prefix = prefix.toUpperCase()"
                    /><button
                      type="button"
                      class="button compact"
                      :disabled="busy"
                      @click="savePrefix"
                    >
                      Save prefix
                    </button></template
                  ><template v-else
                    ><code>{{ workspace.data.value?.display_prefix }}</code
                    ><button
                      v-if="isAdmin"
                      type="button"
                      class="button compact"
                      @click="prefixEditing = true"
                    >
                      Change prefix…
                    </button></template
                  >
                </div>
              </div>
            </div>
            <button v-if="dirty" class="button primary save-button" :disabled="busy">
              {{ busy ? 'Saving…' : 'Save changes' }}
            </button>
          </form>
          <h3 class="subsequent-heading">Danger Zone</h3>
          <div class="setting-box">
            <div class="setting-row">
              <span>Leave workspace<small>The last owner cannot leave.</small></span
              ><button class="button" :disabled="busy" @click="leaveOrDelete('leave')">
                Leave workspace
              </button>
            </div>
            <div v-if="myRole === 'owner'" class="setting-row">
              <span>Delete workspace<small>Deletes every study, record and run in it.</small></span
              ><button
                class="button danger-button"
                :disabled="busy"
                @click="leaveOrDelete('delete')"
              >
                Delete workspace
              </button>
            </div>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>
<style scoped>
.setting-row .reply-mode-select {
  width: 192px;
  max-width: 48%;
  flex: none;
}

.notifications-layout .settings-heading {
  margin-bottom: 31px;
}
.code-layout .settings-heading {
  margin-bottom: 32px;
}
.code-layout .code-host {
  min-height: 106px;
}
.code-layout .repositories-empty {
  min-height: 78px;
}
.code-layout .subsection-toolbar {
  margin-top: 31px;
}
.code-layout .setting-row:not(.code-host) {
  min-height: 71px;
}

.desktop-settings {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
.settings-rail {
  width: 240px;
  flex: none;
  border-right: 1px solid var(--border);
  padding: 16px 12px;
  overflow: auto;
}
.settings-mobile-navigation {
  display: none;
}
.settings-rail h1 {
  font-size: 20px;
  line-height: 28px;
  font-weight: 600;
  padding: 0 8px;
  margin-bottom: 8px;
}
.settings-search {
  height: 32px;
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  color: var(--muted);
  margin-bottom: 12px;
}
.settings-search input {
  border: 0;
  background: none;
  outline: none;
  min-width: 0;
  width: 100%;
  font-size: 14px;
}
.settings-rail section + section {
  border-top: 1px solid var(--border);
  margin-top: 12px;
  padding-top: 12px;
}
.settings-rail section.extension-group {
  border: 0;
  padding: 0;
  margin-top: 8px;
}
.settings-rail section header {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 32px;
  padding: 0 12px;
  font-size: 14px;
}
.settings-rail section header strong {
  font-size: 14px;
  font-weight: 500;
}
.settings-rail section header small {
  font-size: 11px;
  margin-left: auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  max-width: 95px;
}
.tiny-avatar {
  width: 16px;
  height: 16px;
  background: var(--input);
  color: var(--muted);
  border-radius: 4px;
  display: grid;
  place-items: center;
  font-size: 9px;
}
.settings-rail section:first-of-type .tiny-avatar {
  border-radius: 50%;
}
.settings-rail .extension-group header {
  padding: 0 12px;
  font-size: 12px;
  color: var(--muted);
}
.settings-rail .extension-group header strong {
  font-size: 12px;
  font-weight: 400;
}
.settings-rail button {
  height: 32px;
  margin-bottom: 1px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 0 12px;
  text-align: left;
  color: var(--muted);
  font-size: 14px;
}
.settings-rail button:hover {
  background: var(--input);
}
.settings-rail button.selected {
  background: var(--selected);
  color: var(--text);
}
.settings-scroll {
  flex: 1;
  min-width: 0;
  overflow: auto;
  scrollbar-gutter: stable both-edges;
}
.settings-body {
  max-width: 1016px;
  padding: 16px 16px 56px;
  margin: 0 auto;
}
.settings-heading {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 34px;
  height: 28px;
}
.settings-heading h2 {
  font-size: 20px;
  font-weight: 600;
  line-height: 28px;
}
.scope-badge {
  font-size: 12px;
  font-weight: 400;
  background: var(--border);
  color: var(--muted);
  border-radius: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  line-height: 16px;
  white-space: nowrap;
}
.settings-body h3 {
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 2px 12px;
}
.setting-box {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}
.setting-row {
  min-height: 64px;
  padding: 14px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  font-size: 14px;
}
.setting-row + .setting-row {
  border-top: 1px solid var(--border);
}
.setting-row small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  margin-top: 4px;
}
.setting-row input:not([type='checkbox']),
.setting-row textarea,
.setting-row select {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 7px;
  font: inherit;
  padding: 6px 10px;
  width: 47%;
  min-width: 0;
  height: 32px;
}
.setting-row select {
  width: auto;
  max-width: 48%;
  min-width: 192px;
}
.setting-row input.profile-name-input {
  border: 0;
  text-align: right;
}
.setting-row.about-row {
  align-items: flex-start;
  min-height: 114px;
}
.setting-row textarea {
  height: 64px;
  resize: vertical;
  line-height: 20px;
}
.about-control {
  width: 47%;
}
.about-control textarea {
  width: 100%;
}
.about-control small {
  text-align: right;
  margin-top: 0;
}
.avatar-row {
  height: 92px;
}
.profile-avatar {
  height: 64px;
  width: 64px;
  border-radius: 50%;
  background: var(--border);
  font-size: 26px;
  color: var(--muted);
  overflow: hidden;
}
.profile-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.settings-body.wide h3:not(:first-of-type) {
  margin-top: 32px;
}
.settings-body.wide :deep(.timezone-trigger > span) {
  display: none;
}
.theme-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}
.theme-card {
  text-align: left;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  background: var(--input);
  min-width: 0;
  position: relative;
}
.theme-card.chosen {
  outline: 1px solid var(--primary);
  outline-offset: 0;
  border-color: var(--primary);
}
.theme-preview {
  height: 106px;
  padding: 12px;
  background: var(--swatch-bg);
}
.theme-preview > div {
  background: var(--swatch-input);
  height: 100%;
  padding: 12px;
  border-radius: 8px;
}
.theme-preview i {
  display: block;
  width: 66%;
  height: 8px;
  border-radius: 5px;
  background: var(--swatch-text);
  opacity: 0.9;
  margin-bottom: 6px;
}
.theme-preview i + i {
  width: 50%;
  opacity: 0.4;
  margin-bottom: 12px;
}
.theme-preview span {
  display: inline-block;
  background: var(--swatch-accent);
  color: white;
  border-radius: 6px;
  padding: 4px 7px;
  font-size: 12px;
  line-height: 16px;
}
.theme-preview b {
  display: inline-block;
  background: var(--swatch-accent);
  width: 16px;
  height: 16px;
  border-radius: 50%;
  vertical-align: middle;
  margin-left: 8px;
}
.theme-card footer {
  padding: 11px 12px 10px;
  font-size: 14px;
  position: relative;
}
.theme-card footer small {
  display: block;
  font-size: 11px;
  line-height: 16px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding-right: 10px;
}
.theme-card footer > .icon {
  position: absolute;
  right: 12px;
  top: 18px;
  background: var(--primary);
  color: #fff;
  border-radius: 50%;
  padding: 4px;
  width: 20px;
  height: 20px;
}
.setting-row input.setting-switch {
  appearance: none;
  flex: none;
  width: 32px;
  height: 18px;
  background: var(--border);
  border-radius: 12px;
  position: relative;
  cursor: pointer;
  margin: 0;
}
.setting-switch:checked {
  background: var(--primary) !important;
}
.setting-switch::after {
  content: '';
  position: absolute;
  width: 14px;
  height: 14px;
  left: 2px;
  top: 2px;
  background: white;
  box-shadow: 0 1px 2px #0002;
  border-radius: 50%;
}
.setting-switch:checked::after {
  left: 16px;
}
.segmented {
  padding: 3px;
  display: flex;
  border-radius: 7px;
  background: var(--border);
  flex: none;
}
.segmented button {
  padding: 4px 14px;
  min-width: 104px;
  font-size: 13px;
  border-radius: 4px;
  color: var(--muted);
}
.segmented button[aria-pressed='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 1px 3px #0001;
}
.save-button {
  margin-top: 16px;
}
.settings-note {
  font-size: 13px;
  color: var(--muted);
  line-height: 20px;
  margin-top: 16px;
}
.settings-unavailable {
  display: flex;
  align-items: center;
  flex-direction: column;
  text-align: center;
  gap: 16px;
  padding: 80px 20px;
  color: var(--muted);
}
.settings-unavailable h3 {
  margin: 0;
}
.settings-unavailable p {
  max-width: 430px;
  font-size: 14px;
  line-height: 22px;
}
.tokens-body {
  margin-top: 31px;
}
.tokens-body h3 {
  margin: 31px 0 12px;
}
.token-note {
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
  margin: 0 2px 20px;
}
.token-usage {
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 20px;
}
.token-usage summary {
  cursor: pointer;
}
.token-usage p {
  padding: 12px 16px;
  line-height: 20px;
}
.token-form {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  min-height: 66px;
}
.token-form input,
.token-form select {
  height: 32px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  padding: 0 10px;
  font-size: 14px;
  min-width: 0;
}
.token-form input {
  flex: 1;
}
.token-form select {
  width: 96px;
}
.token-form .button {
  margin-left: 26px;
}
.tokens-empty {
  margin-top: 12px;
  min-height: 72px;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  color: var(--muted);
  font-size: 13px;
}
.tokens-empty .text-button {
  margin-left: auto;
}
.section-description {
  font-size: 12px;
  color: var(--muted);
  line-height: 18px;
  margin: -7px 2px 14px;
}
.notification-box .setting-row {
  min-height: 71px;
}
.table-layout .settings-heading {
  margin-bottom: 8px;
}
.table-description {
  font-size: 14px;
  line-height: 22px;
  color: var(--muted);
  margin: 0;
}
.label-scope {
  width: max-content;
  margin: 0 0 16px;
}
.label-scope button {
  min-width: 64px;
  padding: 4px 10px;
}
.label-scope small {
  font-size: 12px;
}
.table-toolbar {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;
}
.table-search {
  display: flex;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 12px;
  height: 32px;
  width: 384px;
  max-width: 50%;
  color: var(--muted);
  margin-right: auto;
}
.table-search input {
  width: 100%;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
}
.table-toolbar > small {
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
.archived-control {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
  font-size: 12px;
  color: var(--muted);
}
.archived-control .setting-switch {
  appearance: none;
  width: 32px;
  height: 18px;
  background: var(--border);
  border-radius: 12px;
  position: relative;
  cursor: pointer;
  margin: 0;
}
.settings-data-table {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.settings-data-table table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  text-align: left;
  table-layout: fixed;
}
.settings-data-table th {
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
  height: 38px;
  background: var(--border);
}
.settings-data-table td,
.settings-data-table th {
  padding: 8px 16px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.settings-data-table th:first-child {
  width: 29%;
}
.settings-data-table th:nth-last-child(-n + 2) {
  width: 15%;
}
.settings-data-table td {
  border-top: 1px solid var(--border);
  height: 42px;
}
.settings-data-table td > button {
  padding: 0;
  text-align: left;
}
.settings-data-table td > button:hover {
  text-decoration: underline;
}
.label-color {
  display: inline-block;
  height: 10px;
  width: 10px;
  border-radius: 50%;
  margin-right: 8px;
}
.table-empty {
  height: 152px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  font-size: 14px;
}
.table-empty > .icon {
  color: var(--muted);
}
.table-empty small {
  font-size: 12px;
  color: var(--muted);
  margin-top: -6px;
}
.property-empty {
  height: 170px;
}
.settings-editor {
  padding: 24px;
  display: grid;
  gap: 18px;
}
.settings-editor header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.settings-editor h2 {
  font-size: 18px;
}
.settings-editor > p {
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
}
.settings-editor label {
  display: grid;
  gap: 8px;
  font-size: 13px;
}
.settings-editor input,
.settings-editor textarea,
.settings-editor select {
  border: 1px solid var(--border);
  background: var(--surface);
  padding: 8px 10px;
  border-radius: 6px;
  width: 100%;
}
.settings-editor textarea {
  height: 78px;
  resize: vertical;
}
.settings-editor input[type='color'] {
  height: 36px;
  width: 72px;
  padding: 3px;
}
.settings-editor footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.settings-editor .form-error {
  margin: 0;
}
.analytics-layout {
  max-width: none;
  padding: 0;
}
.analytics-layout .settings-heading {
  padding: 16px 20px;
  height: 56px;
  margin: 0;
}
.analytics-status {
  margin-left: auto;
  font-size: 12px;
  color: var(--muted);
  display: flex;
  align-items: center;
  gap: 8px;
}
.analytics-toolbar {
  height: 44px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
}
.analytics-tabs {
  display: flex;
  align-self: stretch;
  margin-right: auto;
}
.analytics-tabs button {
  padding: 0 12px;
  border-bottom: 2px solid transparent;
  color: var(--muted);
  font-size: 14px;
}
.analytics-tabs button[aria-selected='true'] {
  color: var(--text);
  border-color: var(--text);
}
.analytics-toolbar select {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  height: 28px;
  font-size: 13px;
  padding: 0 8px;
}
.analytics-content {
  max-width: 1136px;
  padding: 24px 16px;
  margin: 0 auto;
}
.usage-metrics {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 20px 0;
  margin-bottom: 20px;
}
.usage-metrics > div {
  padding: 0 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.usage-metrics > div + div {
  border-left: 1px solid var(--border);
}
.usage-metrics small {
  font-size: 11px;
  color: var(--muted);
}
.usage-metrics strong {
  font-size: 24px;
  font-weight: 500;
}
.analytics-panel {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 20px;
  padding: 20px;
}
.analytics-panel header {
  display: flex;
  align-items: center;
  gap: 12px;
}
.analytics-panel h3 {
  margin: 0 auto 0 0;
}
.analytics-panel .segmented button {
  min-width: 0;
  padding: 3px 9px;
  font-size: 12px;
}
.analytics-panel > p {
  height: 140px;
  display: grid;
  place-items: center;
  font-size: 13px;
  color: var(--muted);
}
.code-host {
  min-height: 108px;
  gap: 12px;
  align-items: flex-start;
}
.code-host > span:nth-child(2) {
  flex: 1;
  line-height: 22px;
  padding-top: 2px;
}
.code-host > .button {
  align-self: center;
}
.code-host-icon {
  background: var(--surface);
  border: 1px solid var(--border);
  height: 36px;
  width: 36px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  flex: none;
}
.inline-muted {
  display: inline !important;
  margin-left: 6px;
}
.subsection-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 32px;
}
.subsection-toolbar h3 {
  margin-bottom: 12px;
}
.subsection-toolbar .section-description {
  margin-bottom: 14px;
}
.repositories-empty {
  min-height: 80px;
  display: grid;
  place-items: center;
  color: var(--muted);
  font-size: 12px;
}
.repository-url {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.career-box {
  padding: 14px 16px 16px;
}
.career-box label {
  display: block;
  font-size: 14px;
}
.career-box select {
  display: block;
  width: 100%;
  height: 40px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  margin: 6px 0 16px;
  padding: 0 12px;
}
.subsequent-heading {
  margin-top: 32px !important;
}
.inline-setting {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}
.inline-setting code {
  font-size: 13px;
}
.inline-setting input {
  width: 100px !important;
}
.danger-button {
  color: #bd4444;
}
.no-match {
  font-size: 13px;
  padding: 10px;
  color: var(--muted);
}
.fields-description {
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
  margin: -5px 2px 14px;
}
.field-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  text-align: left;
}
.field-table th,
.field-table td {
  height: 42px;
  padding: 8px 16px;
  font-weight: 400;
  border-bottom: 1px solid var(--border);
}
.field-table thead th {
  font-size: 12px;
  color: var(--muted);
}
.field-table th:first-child {
  width: 50%;
}
.field-table td,
.field-table th:not(:first-child) {
  text-align: center;
}
.field-table tr:last-child th,
.field-table tr:last-child td {
  border-bottom: 0;
}
.field-table input {
  accent-color: var(--primary);
  width: 14px;
  height: 14px;
  cursor: pointer;
}
.field-table td > span {
  color: var(--muted);
}
@media (max-width: 1100px) {
  .settings-body {
    padding: 16px 24px 40px;
  }
  .settings-rail {
    width: 220px;
  }
  .theme-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .setting-row {
    gap: 12px;
  }
  .setting-row select {
    min-width: 140px;
  }
  .setting-row > span {
    max-width: 55%;
  }
}
:deep(.settings-dialog) {
  width: 514px;
  overflow: visible;
}
.settings-editor {
  padding: 16px;
  gap: 0;
}
.settings-editor header h2 {
  font-size: 16px;
  font-weight: 500;
}
.settings-editor header {
  height: 24px;
}
.settings-editor > p {
  font-size: 14px;
  line-height: 20px;
  margin-top: 4px;
}
.settings-editor-fields {
  display: grid;
  gap: 20px;
  margin-top: 24px;
  margin-bottom: 24px;
}
.settings-editor label,
.editor-color-field > span,
.property-options > span {
  font-size: 14px;
  line-height: 20px;
}
.settings-editor label {
  gap: 6px;
}
.settings-editor input,
.settings-editor textarea,
.settings-editor select {
  background: transparent;
  border-color: color-mix(in srgb, var(--surface) 40%, transparent);
  font-size: 14px;
  padding: 6px 10px;
  height: 32px;
}
.settings-editor textarea {
  height: 64px;
}
.settings-editor footer {
  border-top: 1px solid color-mix(in srgb, var(--surface) 30%, transparent);
  margin: 0 -16px -16px;
  padding: 16px;
  gap: 12px;
  background: #00000003;
  border-radius: 0 0 16px 16px;
}
.cancel-editor {
  font-size: 14px;
  padding: 4px 8px;
}
.property-editor-row {
  display: grid;
  grid-template-columns: 68px 1fr 160px;
  gap: 12px;
}
.property-editor-row label {
  min-width: 0;
}
.property-editor-row select {
  max-width: 100%;
}
.property-options {
  display: grid;
  gap: 6px;
}
.property-option-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.property-option-row > i {
  width: 22px;
  height: 22px;
  flex: none;
  border-radius: 50%;
}
.property-option-row input {
  flex: 1;
  min-width: 0;
}
.property-option-row .icon {
  color: var(--muted);
}
.add-option {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  padding: 8px 10px;
  text-align: left;
}
.editor-color-field {
  position: relative;
}
.editor-color-field > :deep(.settings-color) {
  margin-top: 6px;
}
.settings-editor .form-error {
  margin-bottom: 16px;
}
.field-table {
  table-layout: fixed;
}
.field-table th:first-child {
  width: calc(100% - 256px);
}
.field-table thead th {
  height: 48px;
  line-height: 16px;
}
.field-table thead th:not(:first-child) {
  width: 128px;
  padding-left: 22px;
  padding-right: 22px;
}
.field-table tbody th,
.field-table tbody td {
  height: 41px;
}
.field-table td {
  text-align: left !important;
  width: 128px;
}
.field-table input {
  width: 16px;
  height: 16px;
  margin: 0;
}
.field-table td > span {
  display: block;
  text-align: center;
}
.property-option-row > :deep(.settings-color) {
  flex: none;
}

.settings-editor-fields {
  margin-top: 18px;
  margin-bottom: 32px;
  gap: 18px;
}
.settings-editor label {
  gap: 4px;
}
.editor-color-field > :deep(.settings-color) {
  margin-top: 4px;
}
.catalog-storage-note {
  font-size: 11px;
  color: var(--muted);
  margin-right: auto;
  align-self: center;
}
.editor-color-field :deep(.color-surface) {
  gap: 9px;
}
.new-token {
  margin-top: 10px;
  background: #e6f1e8;
}
.new-token code {
  display: block;
  font-family: 'JetBrains Mono', ui-monospace, monospace;
  font-size: 13px;
  margin-top: 4px;
  overflow-wrap: anywhere;
}
.tokens-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}
.tokens-table td {
  padding: 10px 16px;
  border-top: 1px solid var(--border);
}
.tokens-table tr:first-child td {
  border-top: 0;
}
.danger-text {
  color: var(--error-text);
}
@media (width < 48rem) {
  .desktop-settings {
    flex-direction: column;
  }
  .settings-rail {
    width: 100%;
    border-right: 0;
    border-bottom: 1px solid var(--border);
    padding: 12px 16px 16px;
    overflow: visible;
  }
  .settings-rail h1 {
    padding: 0;
  }
  .settings-rail .settings-search,
  .settings-rail section,
  .settings-rail .no-match {
    display: none;
  }
  .settings-mobile-navigation {
    display: block;
    width: 100%;
    height: 40px;
    padding: 0 12px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--canvas);
    color: var(--text);
  }
  .settings-scroll {
    min-height: 0;
  }
  .settings-body {
    padding: 16px 16px 40px;
  }
}
</style>
