<script setup lang="ts">
import SelectPicker from '../../ui/SelectPicker.vue';
import { computed, onUnmounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../../ui/Icon.vue';
import Modal from '../../ui/Modal.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { useCards, useInvitations, useMe, useMembers, useWorkspaces } from '../../api/queries';
import type { Member, Role } from '../../api/types';
import { timeAgo } from '../../lib/studies';

const client = useQueryClient();
const me = useMe();
const workspaces = useWorkspaces();
const members = useMembers();
const cards = useCards();
const route = useRoute();
const search = ref('');
const roleFilter = ref<'all' | Role>('all');
const detailTab = ref<'overview' | 'studies' | 'access'>('overview');
const selectedMember = computed(() =>
  (members.data.value ?? []).find((m) => m.user_id === route.params.memberId),
);
const filteredMembers = computed(() =>
  (members.data.value ?? []).filter(
    (m) =>
      (roleFilter.value === 'all' || roleFilter.value === m.role) &&
      `${m.name} ${m.email}`.toLowerCase().includes(search.value.trim().toLowerCase()),
  ),
);
const memberStudies = computed(() =>
  (cards.data.value ?? []).filter(
    (c) =>
      selectedMember.value &&
      (c.owner_id === selectedMember.value.user_id ||
        (c.assignee_type === 'member' && c.assignee_id === selectedMember.value.user_id)),
  ),
);
watch(
  () => route.params.memberId,
  () => {
    detailTab.value = 'overview';
  },
);
const myRole = computed(() => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role);
const isAdmin = computed(() => myRole.value === 'owner' || myRole.value === 'admin');
const invitations = useInvitations(isAdmin);
const error = ref('');
const inviteError = ref('');
const inviting = ref(false);
const email = ref('');
const role = ref<'member' | 'admin'>('member');
const busy = ref(false);
const roleNames: Record<Role, string> = { owner: 'Owner', admin: 'Admin', member: 'Member' };
let generation = 0;
watch(
  workspaceId,
  () => {
    generation++;
    inviting.value = false;
    email.value = '';
    role.value = 'member';
    error.value = '';
    inviteError.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
watch(
  inviting,
  () => {
    generation++;
    error.value = '';
    inviteError.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
watch(
  isAdmin,
  (allowed) => {
    if (!allowed) inviting.value = false;
  },
  { flush: 'sync' },
);
onUnmounted(() => generation++);

const label = (m: Member) => m.name || m.email;
const refresh = (workspace: string) =>
  Promise.all([
    client.invalidateQueries({ queryKey: keys.members(workspace) }),
    client.invalidateQueries({ queryKey: keys.invitations(workspace) }),
    client.invalidateQueries({ queryKey: keys.workspaces }),
  ]);
async function act(method: string, path: string, payload?: unknown, invitation = false) {
  if (busy.value) return;
  const workspace = workspaceId.value;
  const visit = generation;
  const current = () => generation === visit && workspaceId.value === workspace;
  const actionError = invitation ? inviteError : error;
  actionError.value = '';
  busy.value = true;
  try {
    await api(method, `/api/workspaces/${workspace}/${path}`, payload);
    if (!current()) return;
    await refresh(workspace);
    if (current() && invitation) {
      email.value = '';
      inviting.value = false;
    }
  } catch (e) {
    if (!current()) return;
    actionError.value = (e as Error).message;
    await refresh(workspace);
  } finally {
    if (current()) busy.value = false;
  }
}
function setRole(m: Member, el: HTMLSelectElement) {
  const requested = el.value;
  // Keep the server's role visible until the change is accepted and read back.
  el.value = m.role;
  return act('PATCH', `members/${m.user_id}`, { role: requested });
}
function removeMember(m: Member) {
  if (busy.value) return;
  if (confirm(`Remove ${label(m)} from this workspace?`))
    return act('DELETE', `members/${m.user_id}`);
}
const revoke = (id: string) => act('DELETE', `invitations/${id}`);
const invite = () =>
  act('POST', 'invitations', { email: email.value.trim(), role: role.value }, true);
</script>
<template>
  <section class="resources resource-colleagues">
    <div v-if="isAdmin && !route.params.memberId" class="resource-header-actions">
      <button class="button primary" :disabled="busy" @click="inviting = true">
        <Icon name="plus" />Invite
      </button>
    </div>
    <div v-if="route.params.memberId" class="colleague-detail">
      <header class="colleague-page-header">
        <nav class="colleague-breadcrumb" aria-label="Colleague navigation">
          <router-link to="/colleagues"><Icon name="back" :size="14" />Colleagues</router-link>
          <template v-if="selectedMember"
            ><span aria-hidden="true">/</span
            ><span aria-current="page">{{ label(selectedMember) }}</span></template
          >
        </nav>
      </header>
      <p v-if="members.isPending.value" class="muted">Loading colleague…</p>
      <p v-else-if="members.error.value" class="inline-error" role="alert">
        {{ members.error.value.message }}
      </p>
      <p v-else-if="!selectedMember" class="muted">Colleague not found.</p>
      <template v-else>
        <div class="colleague-layout">
          <aside class="colleague-card colleague-info">
            <header class="colleague-hero">
              <span class="colleague-avatar large"
                ><img
                  v-if="selectedMember.avatar_url"
                  :src="selectedMember.avatar_url"
                  alt=""
                /><template v-else>{{ label(selectedMember)[0].toUpperCase() }}</template></span
              >
              <div>
                <h1>{{ label(selectedMember) }}</h1>
                <span class="muted">{{ selectedMember.email }}</span>
              </div>
            </header>
            <div class="colleague-properties">
              <h3>Properties</h3>
              <dl>
                <dt>Name</dt>
                <dd>{{ selectedMember.name || '—' }}</dd>
                <dt>Email</dt>
                <dd>{{ selectedMember.email }}</dd>
                <dt>Workspace role</dt>
                <dd>{{ roleNames[selectedMember.role] }}</dd>
              </dl>
            </div>
          </aside>
          <div class="colleague-card colleague-content">
            <nav class="colleague-tabs" aria-label="Colleague sections">
              <button
                v-for="item in ['overview', 'studies', 'access'] as const"
                :key="item"
                :class="{ active: detailTab === item }"
                @click="detailTab = item"
              >
                {{ item[0].toUpperCase() + item.slice(1) }}
              </button>
            </nav>
            <div class="colleague-panel" v-if="detailTab === 'overview'">
              <h3>Studies</h3>
              <p class="muted">
                {{
                  memberStudies.length
                    ? `${memberStudies.length} visible studies owned by or assigned to this colleague`
                    : 'No visible studies owned by or assigned to this colleague.'
                }}
              </p>
              <hr />
              <h3>Workspace</h3>
              <p class="muted">{{ roleNames[selectedMember.role] }}</p>
            </div>
            <div class="colleague-panel" v-else-if="detailTab === 'studies'">
              <h3>Studies</h3>
              <p v-if="!memberStudies.length" class="muted">
                No visible studies owned by or assigned to this colleague.
              </p>
              <ul v-else>
                <li v-for="study in memberStudies" :key="study.id">
                  <router-link :to="`/studies/${study.id}`">{{ study.title }}</router-link>
                </li>
              </ul>
            </div>
            <div class="colleague-panel" v-else>
              <h3>Workspace access</h3>
              <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
              <p v-if="!isAdmin">{{ roleNames[selectedMember.role] }}</p>
              <SelectPicker v-if="isAdmin"
                ><select
                  :value="selectedMember.role"
                  :disabled="busy"
                  :aria-label="'Role of ' + label(selectedMember)"
                  @change="setRole(selectedMember, $event.target as HTMLSelectElement)"
                >
                  <option
                    v-if="myRole === 'owner' || selectedMember.role === 'owner'"
                    value="owner"
                    :disabled="myRole !== 'owner'"
                  >
                    Owner
                  </option>
                  <option value="admin">Admin</option>
                  <option value="member">Member</option>
                </select></SelectPicker
              >
            </div>
          </div>
        </div>
      </template>
    </div>
    <div v-else class="colleagues">
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <p v-if="members.error.value" class="inline-error" role="alert">
        {{ members.error.value.message }}
      </p>
      <div class="colleague-toolbar">
        <label class="agent-search"
          ><Icon name="search" :size="14" /><input
            v-model="search"
            aria-label="Search colleagues"
            placeholder="Search colleagues..." /></label
        ><SelectPicker
          ><select v-model="roleFilter" aria-label="Filter colleagues by role">
            <option value="all">All roles</option>
            <option value="owner">Owner</option>
            <option value="admin">Admin</option>
            <option value="member">Member</option>
          </select></SelectPicker
        >
      </div>
      <table class="people" aria-label="Members">
        <thead>
          <tr>
            <th>Name</th>
            <th>Email</th>
            <th>Role</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in filteredMembers" :key="m.user_id">
            <td>
              <router-link class="colleague-name" :to="`/colleagues/${m.user_id}`"
                ><span class="colleague-avatar"
                  ><img v-if="m.avatar_url" :src="m.avatar_url" alt="" /><template v-else>{{
                    label(m)[0].toUpperCase()
                  }}</template></span
                ><b>{{ label(m) }}</b></router-link
              >
            </td>
            <td class="muted">{{ m.email }}</td>
            <td>
              <SelectPicker v-if="isAdmin"
                ><select
                  :value="m.role"
                  :disabled="busy"
                  :aria-label="'Role of ' + label(m)"
                  @change="setRole(m, $event.target as HTMLSelectElement)"
                >
                  <option
                    v-if="myRole === 'owner' || m.role === 'owner'"
                    value="owner"
                    :disabled="myRole !== 'owner'"
                  >
                    Owner
                  </option>
                  <option value="admin">Admin</option>
                  <option value="member">Member</option>
                </select></SelectPicker
              ><span v-else>{{ roleNames[m.role] }}</span>
            </td>
            <td>
              <button
                v-if="isAdmin && m.user_id !== me.data.value?.id"
                class="icon-button"
                :disabled="busy"
                :aria-label="'Remove ' + label(m)"
                @click="removeMember(m)"
              >
                <Icon name="more" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="members.isSuccess.value && !filteredMembers.length" class="muted">
        No colleagues found.
      </p>
      <template v-if="isAdmin">
        <h3>Pending invitations</h3>
        <p v-if="invitations.error.value" class="inline-error" role="alert">
          {{ invitations.error.value.message }}
        </p>
        <table class="people" aria-label="Pending invitations">
          <tr v-for="inv in invitations.data.value ?? []" :key="inv.id">
            <td class="muted">{{ inv.email }}</td>
            <td class="muted">Sent {{ timeAgo(inv.created_at) }}</td>
            <td>{{ roleNames[inv.role] }}</td>
            <td>
              <button class="text-button danger" :disabled="busy" @click="revoke(inv.id)">
                Revoke
              </button>
            </td>
          </tr>
        </table>
        <p v-if="invitations.isSuccess.value && !invitations.data.value?.length" class="muted">
          No pending invitations
        </p>
      </template>
    </div>
  </section>
  <Modal v-if="inviting" title="Invite a colleague" @close="inviting = false">
    <form class="resource-form" @submit.prevent="invite">
      <h2>Invite a colleague</h2>
      <label
        >Email<input v-model="email" type="email" aria-label="Email" required autofocus
      /></label>
      <label
        >Role<SelectPicker
          ><select v-model="role" aria-label="Role">
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </select></SelectPicker
        ></label
      >
      <p v-if="inviteError" class="inline-error" role="alert">{{ inviteError }}</p>
      <button class="button primary" :disabled="busy">Send invitation</button>
    </form>
  </Modal>
</template>
<style scoped src="../../ui/resources.css"></style>
<style scoped>
.colleagues {
  padding: 16px 24px;
}
.people {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}
.people th {
  color: var(--muted);
  text-align: left;
  font-size: 12px;
  font-weight: 400;
  padding: 10px 16px;
}
.people td {
  padding: 10px 16px;
  border-top: 1px solid var(--border);
}
.people tbody tr:hover {
  background: var(--fill-card);
}
.colleague-toolbar {
  margin-bottom: 20px;
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.colleague-toolbar .agent-search {
  width: 245px;
  max-width: 100%;
}
.colleague-name {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--text);
  text-decoration: none;
}
.colleague-avatar {
  display: inline-grid;
  place-items: center;
  flex: none;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  background: var(--fill-avatar);
  color: var(--muted);
  overflow: hidden;
}
.colleague-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.colleague-avatar.large {
  width: 56px;
  height: 56px;
  font-size: 20px;
}
.colleague-detail {
  display: flex;
  flex-direction: column;
  min-height: 100%;
  padding: 0 16px 16px;
}
.colleague-page-header {
  display: flex;
  align-items: center;
  min-height: 64px;
}
.colleague-breadcrumb {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--muted);
  font-size: 12px;
}
.colleague-breadcrumb a {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: inherit;
  text-decoration: none;
}
.colleague-breadcrumb [aria-current='page'] {
  color: var(--text);
}
.colleague-hero {
  display: flex;
  gap: 16px;
  align-items: flex-start;
  padding: 24px 0;
  margin: 0 20px;
  border-bottom: 1px solid var(--border);
}
.colleague-hero h1 {
  font-size: 20px;
  line-height: 27px;
  margin: 0;
  overflow-wrap: anywhere;
}
.colleague-layout {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  gap: 16px;
  flex: 1;
}
.colleague-card {
  border: 1px solid var(--border);
  border-radius: 12px;
  min-width: 0;
}
.colleague-info {
  overflow: auto;
}
.colleague-properties {
  padding: 20px;
}
.colleague-properties h3 {
  margin-top: 0;
}
.colleague-info dl {
  display: grid;
  grid-template-columns: 95px minmax(0, 1fr);
  gap: 14px 10px;
  font-size: 12px;
}
.colleague-info dt {
  color: var(--muted);
}
.colleague-info dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.colleague-content {
  min-height: 500px;
  overflow: auto;
}
.colleague-tabs {
  display: flex;
  gap: 26px;
  padding: 0 24px;
  border-bottom: 1px solid var(--border);
}
.colleague-tabs button {
  border: 0;
  background: none;
  color: var(--muted);
  padding: 16px 0 14px;
  cursor: pointer;
}
.colleague-tabs button.active {
  color: var(--text);
  border-bottom: 2px solid var(--text);
}
.colleague-panel {
  padding: 24px;
}
.colleague-panel h3 {
  margin-top: 0;
}
.colleague-panel hr {
  border: 0;
  border-top: 1px solid var(--border);
  margin: 24px 0;
}
@media (max-width: 800px) {
  .colleague-layout {
    grid-template-columns: 1fr;
  }
}
.people select {
  border: 0;
  background: transparent;
  font: inherit;
}
h3 {
  font-size: 13.5px;
  margin: 18px 0 8px;
}
.danger {
  color: var(--error-text);
}
</style>
