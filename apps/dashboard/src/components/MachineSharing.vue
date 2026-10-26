<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useMe, useMembers } from '../api/queries';
import type { AffectedMachineRun, Runtime } from '../api/types';
import { backendLabel } from '../lib/backends';
import Modal from './Modal.vue';
const props = defineProps<{ machineId: string; runtimes: Runtime[]; hideHeading?: boolean }>();
const me = useMe();
const members = useMembers();
const client = useQueryClient();
const owned = computed(() => props.runtimes.filter((r) => r.owner_id === me.data.value?.id));
const mode = ref<'chosen' | 'workspace' | 'mixed'>('chosen');
const selected = ref<string[]>([]);
const colleagues = ref<string[]>([]);
const busy = ref(false);
const error = ref('');
const affected = ref<AffectedMachineRun[] | null>(null);
type Change = { id: string; visibility: 'private' | 'workspace'; shared_with: string[] };
let pending: Change[] = [];
let generation = 0;
const shared = (r: Pick<Runtime, 'visibility' | 'shared_with'>) =>
  r.visibility === 'workspace' || !!r.shared_with?.length;
function grantKey(r: { visibility?: string; shared_with?: string[] }) {
  return JSON.stringify([r.visibility ?? 'private', [...(r.shared_with ?? [])].sort()]);
}
function reset() {
  generation++;
  pending = [];
  affected.value = null;
  error.value = '';
  busy.value = false;
  const active = owned.value.filter(shared);
  const grants = new Set(active.map(grantKey));
  mode.value =
    grants.size > 1 ? 'mixed' : active[0]?.visibility === 'workspace' ? 'workspace' : 'chosen';
  selected.value = active.map((r) => r.id);
  colleagues.value = mode.value === 'chosen' ? [...(active[0]?.shared_with ?? [])] : [];
}
watch([workspaceId, () => props.machineId, () => me.data.value?.id], reset, {
  immediate: true,
  flush: 'sync',
});
watch(
  () =>
    JSON.stringify(
      owned.value.map((r) => [r.id, r.os, r.visibility ?? 'private', r.shared_with ?? []]),
    ),
  () => {
    if (!busy.value && !affected.value) reset();
  },
);
onUnmounted(() => generation++);
const supported = computed(() => owned.value.some((r) => r.os === 'linux'));
const unsupported = computed(() => owned.value.some((r) => r.os !== 'linux'));
const unsupportedSelected = computed(() =>
  owned.value.some((r) => r.os !== 'linux' && selected.value.includes(r.id)),
);
const clearOnly = computed(() => unsupportedSelected.value);
const warnings = computed(() => owned.value.some(shared) || selected.value.length > 0);
const locked = computed(() => busy.value || affected.value !== null);
defineExpose({ canClose: () => !locked.value });
const people = computed(() =>
  (members.data.value ?? []).filter((m) => m.user_id !== me.data.value?.id),
);
function chooseMode() {
  if (selected.value.length === 0)
    selected.value = owned.value.filter((r) => r.os === 'linux').map((r) => r.id);
}
const changes = computed(() =>
  owned.value
    .map((r): Change => {
      if (mode.value === 'mixed' && selected.value.includes(r.id))
        return {
          id: r.id,
          visibility: r.visibility ?? 'private',
          shared_with: [...(r.shared_with ?? [])],
        };
      return {
        id: r.id,
        visibility:
          mode.value === 'workspace' && selected.value.includes(r.id) ? 'workspace' : 'private',
        shared_with:
          mode.value === 'chosen' && selected.value.includes(r.id)
            ? colleagues.value.filter((id) => id !== me.data.value?.id)
            : [],
      };
    })
    .filter(
      (change) => grantKey(change) !== grantKey(owned.value.find((r) => r.id === change.id)!),
    ),
);
const hasSharing = computed(() => owned.value.some(shared));
const stopping = computed(
  () =>
    hasSharing.value &&
    (changes.value.length === 0 ||
      owned.value.every((r) => {
        const next = changes.value.find((c) => c.id === r.id) ?? r;
        return !shared(next);
      })),
);
async function save(confirm?: string[], stop = false) {
  if (busy.value || !owned.value.length) return;
  if (!confirm) {
    if (affected.value || (clearOnly.value && !stop)) return;
    pending = stop
      ? owned.value
          .filter(shared)
          .map((r) => ({ id: r.id, visibility: 'private' as const, shared_with: [] }))
      : [...changes.value];
    if (!pending.length) return;
  }
  const workspace = workspaceId.value;
  const machine = props.machineId;
  const visit = generation;
  const current = () =>
    generation === visit && workspaceId.value === workspace && props.machineId === machine;
  const queue = pending;
  busy.value = true;
  error.value = '';
  try {
    while (queue.length) {
      if (!current()) return;
      const change = queue[0]!;
      try {
        await api(
          'PUT',
          `/api/runtimes/${encodeURIComponent(change.id)}/sharing`,
          {
            visibility: change.visibility,
            shared_with: change.shared_with,
            ...(confirm ? { confirm } : {}),
          },
          undefined,
          { workspace },
        );
      } catch (e) {
        if (!current()) return;
        if (e instanceof ApiError && e.status === 409 && Array.isArray(e.body.affected)) {
          affected.value = e.body.affected as AffectedMachineRun[];
          return;
        }
        throw e;
      }
      if (!current()) return;
      queue.shift();
      confirm = undefined;
      affected.value = null;
    }
    await client.invalidateQueries({ queryKey: keys.runtimes(workspace) });
    if (current())
      await client.invalidateQueries({ queryKey: keys.machineColleagues(workspace, machine) });
    if (current() && client.getQueryState(keys.runtimes(workspace))?.status === 'success') reset();
  } catch (e) {
    if (current()) error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
function cancel() {
  if (!busy.value) {
    pending = [];
    affected.value = null;
  }
}
</script>
<template>
  <section v-if="owned.length" class="machine-sharing type-body" aria-label="Shared machines">
    <p v-if="hideHeading && unsupported" class="sharing-platform-intro type-caption">
      Shared runs require Linux. On a Mac, register a Linux VM as a separate machine.
    </p>
    <div class="runtime-heading">
      <div>
        <h2 v-if="!hideHeading">Shared machines</h2>
        <p>Choose who can run agents on this machine.</p>
      </div>
    </div>
    <div class="sharing-fields">
      <label
        >Share
        <select v-model="mode" aria-label="Share" :disabled="locked" @change="chooseMode">
          <option v-if="mode === 'mixed'" value="mixed" disabled>
            Different permissions per CLI
          </option>
          <option value="chosen" :disabled="!supported">Choose colleagues</option>
          <option value="workspace" :disabled="!supported">Everyone in the workspace</option>
        </select>
      </label>
      <fieldset :disabled="locked">
        <legend class="type-caption">Agent CLIs</legend>
        <label v-for="runtime in owned" :key="runtime.id" class="sharing-check">
          <input
            v-model="selected"
            type="checkbox"
            :value="runtime.id"
            :aria-label="`Share ${backendLabel(runtime.harness)}`"
            :disabled="
              (runtime.os !== 'linux' || mode === 'mixed') && !selected.includes(runtime.id)
            "
          />
          {{ backendLabel(runtime.harness) }}
        </label>
      </fieldset>
      <fieldset :disabled="locked || mode !== 'chosen' || clearOnly">
        <legend class="type-caption">Colleagues</legend>
        <label v-for="member in people" :key="member.user_id" class="sharing-check">
          <input
            v-model="colleagues"
            type="checkbox"
            :value="member.user_id"
            :aria-label="`Share with ${member.name || member.email}`"
            :disabled="locked || mode !== 'chosen' || clearOnly"
          />
          {{ member.name || member.email }}
        </label>
        <p v-if="!people.length" class="type-caption">No other workspace members.</p>
      </fieldset>
    </div>
    <p v-if="mode === 'mixed'" class="sharing-note type-caption">
      Each CLI keeps its current permissions. Uncheck a CLI to stop sharing it, or choose a sharing
      option to apply the same permissions to all checked CLIs.
    </p>
    <p v-if="unsupported && !hideHeading" class="sharing-note type-caption">
      Shared runs require Linux. On a Mac, register a Linux VM as a separate machine.
    </p>
    <div v-if="warnings" class="sharing-warnings type-caption">
      <p>
        Colleagues' agents can read this CLI's sign-in. Share only with people you trust. After you
        stop sharing, sign out of the CLI and sign in again to cut off any copy.
      </p>
      <p>
        This machine's owner can read colleagues' study files stored here. Shared runs are isolated
        from the owner's private files and other runs' processes.
      </p>
    </div>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <div class="sharing-save">
      <button
        class="button"
        :class="{ primary: hideHeading }"
        :disabled="locked || (clearOnly && !stopping)"
        @click="save(undefined, stopping)"
      >
        {{ busy ? 'Saving…' : stopping ? 'Stop sharing' : 'Save sharing' }}
      </button>
    </div>
    <Modal v-if="affected" title="Stop sharing" @close="cancel">
      <div class="resource-form">
        <h2>Stop sharing</h2>
        <p>
          These colleagues' studies run on this machine. Their waiting and running runs will be
          cancelled.
        </p>
        <ul>
          <li v-for="run in affected" :key="run.run_id">
            {{ run.study_title }} · {{ run.colleague }}
          </li>
        </ul>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <div class="sharing-actions">
          <button class="button" :disabled="busy" @click="cancel">Cancel</button
          ><button
            class="button primary"
            :disabled="busy"
            @click="save(affected!.map((r) => r.run_id))"
          >
            Confirm stop sharing
          </button>
        </div>
      </div>
    </Modal>
  </section>
</template>
<style scoped src="../views/resources.css"></style>
<style scoped src="../views/machines.css"></style>
