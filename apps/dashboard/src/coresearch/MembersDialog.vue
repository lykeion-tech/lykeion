<script setup lang="ts">
import SelectPicker from '../ui/SelectPicker.vue';
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useMembers } from '../api/queries';
import { api } from '../api/client';
import { workspaceId } from '../api/session';
import { useCoresearchMembers, projectPath } from './api';
import CoresearchModal from './CoresearchModal.vue';
const props = defineProps<{ projectId: string; editable: boolean }>();
const emit = defineEmits<{ close: [] }>();
const members = useCoresearchMembers(() => props.projectId),
  workspaceMembers = useMembers(),
  client = useQueryClient();
const selected = ref(''),
  busy = ref(false),
  error = ref('');
const candidates = computed(() =>
  (workspaceMembers.data.value ?? []).filter(
    (m) => !members.data.value?.some((p) => p.user_id === m.user_id),
  ),
);
const name = (id: string) => {
  const member = workspaceMembers.data.value?.find((m) => m.user_id === id);
  return member?.name?.trim() || member?.email?.trim() || id;
};
const lastOwner = (id: string) =>
  members.data.value?.find((m) => m.user_id === id)?.role === 'owner' &&
  members.data.value.filter((m) => m.role === 'owner').length === 1;
async function change(id: string, remove = false) {
  if (!props.editable || busy.value || !id || (remove && lastOwner(id))) return;
  const workspace = workspaceId.value,
    project = props.projectId;
  busy.value = true;
  error.value = '';
  try {
    await api(
      remove ? 'DELETE' : 'POST',
      `${projectPath(project)}/members${remove ? `/${encodeURIComponent(id)}` : ''}`,
      remove ? undefined : { user_id: id, role: 'member' },
      undefined,
      { workspace },
    );
    selected.value = '';
    await client.invalidateQueries({ queryKey: ['ws', workspace, 'coresearch', project] });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <CoresearchModal label="Project members" @close="emit('close')"
    ><template #default="{ close }">
      <div
        class="w-full max-w-[480px] overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
      >
        <div class="flex items-center justify-between px-5 pb-1 pt-4">
          <h2 class="type-heading text-fg">Project members</h2>
          <button
            type="button"
            aria-label="Close members"
            class="grid h-7 w-7 place-items-center rounded-md text-fg-subtle hover:bg-surface-2 hover:text-fg"
            @click="close"
          >
            ×
          </button>
        </div>
        <div class="space-y-5 px-5 py-4">
          <p
            v-if="error || members.error.value || workspaceMembers.error.value"
            role="alert"
            class="type-small text-fg-subtle"
          >
            {{ error || members.error.value?.message || workspaceMembers.error.value?.message }}
          </p>
          <p v-if="members.isPending.value" role="status">Loading members…</p>
          <ul v-else class="space-y-2">
            <li
              v-for="member in members.data.value"
              :key="member.user_id"
              class="flex items-center gap-2 py-1 type-body"
            >
              <span class="min-w-0 flex-1 truncate">{{ name(member.user_id) }}</span
              ><span v-if="member.role === 'owner'" class="type-small text-fg-subtle">Owner</span
              ><button
                v-if="editable"
                type="button"
                class="quiet-action"
                :aria-label="`Remove ${name(member.user_id)}`"
                :disabled="busy || lastOwner(member.user_id)"
                :title="
                  lastOwner(member.user_id) ? 'The last project owner cannot be removed' : undefined
                "
                @click="change(member.user_id, true)"
              >
                Remove
              </button>
            </li>
          </ul>
          <div v-if="editable" class="flex items-center gap-2">
            <SelectPicker
              ><select
                v-model="selected"
                aria-label="Workspace member"
                class="min-w-0 flex-1 rounded-md border border-line bg-surface px-2 py-1 type-body"
                :disabled="busy || !candidates.length"
              >
                <option value="">
                  {{ candidates.length ? 'Choose a member' : 'All workspace members are included' }}
                </option>
                <option
                  v-for="candidate in candidates"
                  :key="candidate.user_id"
                  :value="candidate.user_id"
                >
                  {{ name(candidate.user_id) }}
                </option>
              </select></SelectPicker
            ><button
              type="button"
              class="rounded-md bg-surface-2 px-3 py-1 type-body"
              :disabled="busy || !selected"
              @click="change(selected)"
            >
              Add member
            </button>
          </div>
          <p v-else class="type-small text-fg-subtle">Only project owners can manage members.</p>
        </div>
      </div>
    </template></CoresearchModal
  >
</template>
