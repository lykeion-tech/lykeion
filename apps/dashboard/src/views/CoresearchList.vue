<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useQueries, useQueryClient } from '@tanstack/vue-query';
import { api } from '../api/client';
import { useMembers } from '../api/queries';
import { workspaceId } from '../api/session';
import { keys, projectPath, useCoresearchProjects } from '../coresearch/api';
import type { ChatRow, ProjectRow } from '../coresearch/types';
import CliIcon from '../coresearch/CliIcon.vue';
import { backendLabel } from '../lib/backends';
import '../coresearch/l2.css';
import '../coresearch/l2-tokens.css';
const projects = useCoresearchProjects();
const members = useMembers();
const client = useQueryClient();
const rows = computed(() =>
  [...(projects.data.value ?? [])]
    .filter((p) => !p.archived_at)
    .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at)),
);
const chats = useQueries({
  queries: computed(() =>
    rows.value.map((p) => ({
      queryKey: keys.coresearchChats(workspaceId.value, p.id),
      queryFn: async ({ signal }: { signal: AbortSignal }) =>
        (await api<ChatRow[] | null>('GET', `${projectPath(p.id)}/chats`, undefined, undefined, {
          workspace: workspaceId.value,
          signal,
        })) ?? [],
    })),
  ),
});
const latestCli = computed(
  () =>
    new Map(
      rows.value.map((p, i) => [
        p.id,
        [...(chats.value[i]?.data ?? [])].sort(
          (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
        )[0]?.cli,
      ]),
    ),
);
const renaming = ref<string | null>(null);
const renameValue = ref('');
const renameInput = ref<HTMLInputElement | null>(null);
const pendingArchive = ref<ProjectRow | null>(null);
const archiveDialog = ref<HTMLDialogElement | null>(null);
watch(pendingArchive, async (p) => {
  if (p) {
    await nextTick();
    archiveDialog.value?.showModal();
  } else {
    archiveDialog.value?.close();
  }
});
onBeforeUnmount(() => archiveDialog.value?.close());
const busy = ref(false);
const error = ref('');
const filterReason = 'Coresearch currently supports Experiment only';
const grid = 'grid-cols-[minmax(0,1fr)_160px_160px_140px_58px]';
const rowClass = 'grid items-center gap-3 px-3 py-2.5 type-body';
function lead(id: string) {
  const label =
    members.data.value?.find((m) => m.user_id === id)?.name ??
    (members.data.value ? 'Unknown member' : '');
  let hash = 0;
  for (const c of `user:${id}`) hash = (hash * 31 + c.charCodeAt(0)) >>> 0;
  const gradients = [
    ['#3D8FCB', '#6FB9EC'],
    ['#8a86d8', '#b6b2ee'],
    ['#27a644', '#5fd39a'],
    ['#d9a441', '#efc773'],
    ['#e5705b', '#f2a08f'],
  ];
  return {
    label,
    initial: label.charAt(0).toUpperCase(),
    gradient: `linear-gradient(135deg, ${gradients[hash % gradients.length]!.join(', ')})`,
  };
}
function updatedAt(ts: string) {
  const at = new Date(ts);
  const time = at.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
  return at.toDateString() === new Date().toDateString()
    ? time
    : `${at.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}, ${time}`;
}
async function startRename(p: ProjectRow) {
  renaming.value = p.id;
  renameValue.value = p.name;
  await nextTick();
  const input = Array.isArray(renameInput.value) ? renameInput.value[0] : renameInput.value;
  input?.select();
}
async function rename() {
  const id = renaming.value;
  const name = renameValue.value.trim();
  renaming.value = null;
  if (!id || !name) return;
  const workspace = workspaceId.value;
  error.value = '';
  try {
    await api('PATCH', projectPath(id), { name }, undefined, { workspace });
    await client.invalidateQueries({ queryKey: keys.coresearchProjects(workspace) });
    await client.invalidateQueries({ queryKey: keys.coresearchProject(workspace, id) });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}
async function archive() {
  if (!pendingArchive.value || busy.value) return;
  const workspace = workspaceId.value;
  const id = pendingArchive.value.id;
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `${projectPath(id)}/archive`, undefined, undefined, { workspace });
    pendingArchive.value = null;
    await client.invalidateQueries({ queryKey: keys.coresearchProjects(workspace) });
    await client.invalidateQueries({ queryKey: keys.coresearchProject(workspace, id) });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section class="coresearch flex min-h-0 flex-1 flex-col">
    <div class="flex min-h-[52px] shrink-0 items-center gap-2 px-5 pb-2 pt-3">
      <h1 class="type-title text-fg">Coresearch</h1>
    </div>
    <div class="flex shrink-0 flex-wrap items-center gap-2 px-5 pb-3 pt-2">
      <button
        type="button"
        class="inline-flex h-8 items-center gap-1.5 rounded-md border border-line bg-surface-2 px-2.5 type-body text-fg-subtle transition-colors hover:bg-surface-3"
        disabled
        :title="filterReason"
        aria-describedby="coresearch-filter-reason"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.75"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M4 6h16M7 12h10M10 18h4" /></svg
        >Filter
      </button>
      <span id="coresearch-filter-reason" class="sr-only">{{ filterReason }}</span>
    </div>
    <p v-if="projects.error.value || error" class="px-5 type-small text-danger" role="alert">
      {{ error || projects.error.value?.message }}
    </p>
    <p v-if="projects.isPending.value" class="px-5 type-small text-fg-subtle" role="status">
      Loading projects…
    </p>
    <div
      v-else-if="!rows.length"
      class="flex flex-1 items-center justify-center type-small text-fg-subtle"
    >
      Nothing yet — projects you start from New land here.
    </div>
    <div v-else class="flex-1 overflow-auto px-5 pb-5">
      <div
        :class="[
          'grid items-center gap-3 border-b border-line px-3 py-2 type-caption text-fg-tertiary',
          grid,
        ]"
      >
        <span>Name</span><span>CLI</span><span>Lead</span><span>Updated</span><span />
      </div>
      <div
        v-for="p in rows"
        :key="p.id"
        :data-project="p.id"
        class="group relative border-b border-line-soft hover:bg-surface-2"
      >
        <div v-if="renaming === p.id" :class="[rowClass, grid]">
          <input
            ref="renameInput"
            v-model="renameValue"
            :aria-label="`Rename ${p.name}`"
            maxlength="120"
            class="min-w-0 rounded-md border border-line-strong bg-surface-2 px-1.5 py-0.5 type-label text-fg outline-none"
            @blur="rename"
            @keydown.enter.prevent="rename"
            @keydown.esc.prevent="renaming = null"
          />
        </div>
        <RouterLink v-else :to="`/coresearch/${p.id}`" :class="[rowClass, grid]">
          <span class="truncate type-label text-fg">{{ p.name }}</span>
          <span class="flex min-w-0 items-center gap-1.5 text-fg-muted">
            <template v-if="latestCli.get(p.id)"
              ><span
                class="grid shrink-0 place-items-center"
                :style="{ color: latestCli.get(p.id) === 'claude' ? '#d97757' : 'var(--ink)' }"
                ><CliIcon :cli="latestCli.get(p.id)!" class="h-3.5 w-3.5" /></span
              ><span class="truncate">{{ backendLabel(latestCli.get(p.id)!) }}</span></template
            >
            <span v-else class="text-fg-tertiary">—</span>
          </span>
          <span class="flex min-w-0 items-center gap-1.5 text-fg-muted"
            ><span
              aria-hidden="true"
              class="grid h-4 w-4 shrink-0 place-items-center rounded-[5px] type-caption text-white"
              :style="{ backgroundImage: lead(p.created_by).gradient }"
              >{{ lead(p.created_by).initial }}</span
            ><span class="truncate">{{ lead(p.created_by).label }}</span></span
          >
          <span class="truncate text-fg-tertiary">{{ updatedAt(p.updated_at) }}</span
          ><span />
        </RouterLink>
        <span
          v-if="p.role === 'owner' && renaming !== p.id"
          class="absolute inset-y-0 right-3 flex items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100"
        >
          <button
            :aria-label="`Rename ${p.name}`"
            :title="`Rename ${p.name}`"
            class="grid h-6 w-6 place-items-center rounded-md text-fg-subtle hover:bg-surface-3 hover:text-fg"
            @click="startRename(p)"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.75"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z" />
            </svg>
          </button>
          <button
            :aria-label="`Archive ${p.name}`"
            :title="`Archive ${p.name}`"
            class="grid h-6 w-6 place-items-center rounded-md text-fg-subtle hover:bg-surface-3 hover:text-danger"
            @click="pendingArchive = p"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.75"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M3 6a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v2H3V6Z" />
              <path d="M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8" />
              <path d="M10 12h4" />
            </svg>
          </button>
        </span>
      </div>
    </div>
    <dialog
      v-if="pendingArchive"
      ref="archiveDialog"
      aria-label="Archive project"
      tabindex="-1"
      class="m-0 size-full max-h-none max-w-none bg-transparent text-inherit outline-none items-center justify-center p-4 backdrop:bg-black/55 open:flex"
      @cancel.prevent="!busy && (pendingArchive = null)"
      @click.self="!busy && (pendingArchive = null)"
    >
      <section
        aria-labelledby="archive-title"
        class="w-full max-w-[460px] overflow-hidden rounded-xl border border-line bg-surface p-5 shadow-2xl"
      >
        <h2 id="archive-title" class="type-title">Archive project?</h2>
        <p class="mt-2 type-body text-fg-muted">
          {{ pendingArchive.name }} will leave the active list. Its saved work is kept.
        </p>
        <p v-if="error" role="alert" class="type-small text-danger">{{ error }}</p>
        <div class="mt-4 flex justify-end gap-2">
          <button
            class="rounded-md border border-line px-3 py-1.5 type-body"
            :disabled="busy"
            @click="pendingArchive = null"
          >
            Cancel</button
          ><button
            class="rounded-md bg-danger px-3 py-1.5 type-body text-white"
            :disabled="busy"
            @click="archive"
          >
            {{ busy ? 'Archiving…' : 'Archive' }}
          </button>
        </div>
      </section>
    </dialog>
  </section>
</template>
