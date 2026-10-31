<script setup lang="ts">
import SelectPicker from '../components/SelectPicker.vue';
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import Modal from '../components/Modal.vue';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useInvitations, useMe, useMembers, useWorkspaces } from '../api/queries';
import type { Member, Role } from '../api/types';
import { timeAgo } from '../lib/studies';

const client = useQueryClient();
const me = useMe();
const workspaces = useWorkspaces();
const members = useMembers();
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
    <div v-if="isAdmin" class="resource-header-actions">
      <button class="button primary" :disabled="busy" @click="inviting = true">
        <Icon name="plus" />Invite
      </button>
    </div>
    <div class="colleagues">
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <p v-if="members.error.value" class="inline-error" role="alert">
        {{ members.error.value.message }}
      </p>
      <table class="people" aria-label="Members">
        <tr v-for="m in members.data.value ?? []" :key="m.user_id">
          <td>
            <span class="workspace-avatar">{{ label(m)[0] }}</span> {{ label(m) }}
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
      </table>
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
<style scoped src="./resources.css"></style>
<style scoped>
.colleagues {
  max-width: 842px;
  margin: 0 auto;
  padding: 16px 20px;
}
.people {
  width: 100%;
  background: var(--input);
  border-radius: 10px;
  border-collapse: collapse;
  font-size: 13.5px;
}
.people td {
  padding: 10px 16px;
  border-top: 1px solid var(--border);
}
.people tr:first-child td {
  border-top: 0;
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
