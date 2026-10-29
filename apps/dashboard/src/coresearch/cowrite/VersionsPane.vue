<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { Version } from './types';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { filePath } from '../api';
import { boundedUnifiedDiff } from '../diff';
import { formatAgo } from './lib/pane-time';
import PaneIcon from './PaneIcon.vue';
const props = defineProps<{
  projectId: string;
  versions: Version[];
  people: { id: string; name: string }[];
  restore: (v: Version) => Promise<void>;
  disabled?: boolean;
}>();
const restoring = ref<number>(),
  menu = ref<number>(),
  lines = ref(new Map<number, { added: number; removed: number }>());
const days = computed(() => {
  const groups: { day: string; versions: Version[] }[] = [];
  for (const version of [...props.versions].sort((a, b) => b.seq - a.seq)) {
    const day = new Date(version.created_at).toLocaleDateString(undefined, {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
    });
    if (groups.at(-1)?.day === day) groups.at(-1)!.versions.push(version);
    else groups.push({ day, versions: [version] });
  }
  return groups;
});
const textFile = /\.(tex|bib|cls|sty|bst|txt)$/i;
watch(
  () => [workspaceId.value, props.projectId, props.versions] as const,
  async (_, __, cleanup) => {
    const ws = workspaceId.value,
      p = props.projectId,
      abort = new AbortController();
    let cancelled = false;
    cleanup(() => {
      cancelled = true;
      abort.abort();
    });
    lines.value = new Map();
    const saved = new Map<string, Promise<string>>();
    const read = (file: Version['files'][number]) => {
      const key = `${file.file_id}:${file.n}`;
      let held = saved.get(key);
      if (!held) {
        held = api<Blob>(
          'GET',
          `${filePath(p, file.file_id)}/versions/${file.n}/content`,
          undefined,
          undefined,
          { workspace: ws, signal: abort.signal, response: 'blob' },
        ).then((b) => b.text());
        saved.set(key, held);
      }
      return held;
    };
    const count = (text: string) => (text === '' ? 0 : text.replace(/\n$/, '').split('\n').length);
    const versions = [...props.versions].sort((a, b) => a.seq - b.seq);
    for (let i = 0; i < versions.length; i++) {
      const current = versions[i]!,
        previous = versions[i - 1];
      try {
        const before = new Map(
            (previous?.files ?? []).filter((f) => textFile.test(f.path)).map((f) => [f.path, f]),
          ),
          after = new Map(
            current.files.filter((f) => textFile.test(f.path)).map((f) => [f.path, f]),
          );
        let added = 0,
          removed = 0,
          tooLarge = false;
        for (const path of new Set([...before.keys(), ...after.keys()])) {
          const was = before.get(path),
            now = after.get(path);
          if (was && now && was.file_id === now.file_id && was.n === now.n) continue;
          if (!was) added += count(await read(now!));
          else if (!now) removed += count(await read(was));
          else {
            const diff = boundedUnifiedDiff(await read(was), await read(now));
            if (diff.kind === 'too-large') {
              tooLarge = true;
              break;
            }
            for (const line of diff.text.split('\n')) {
              if (line.startsWith('+')) added++;
              else if (line.startsWith('-')) removed++;
            }
          }
        }
        if (cancelled) return;
        if (!tooLarge) lines.value.set(current.seq, { added, removed });
      } catch {
        if (cancelled) return;
      }
    }
  },
  { immediate: true, deep: true },
);
async function restoreVersion(v: Version) {
  if (restoring.value !== undefined || props.disabled) return;
  restoring.value = v.seq;
  menu.value = undefined;
  try {
    await props.restore(v);
  } finally {
    restoring.value = undefined;
  }
}
</script>
<template>
  <div class="ms-pane" data-testid="versions-pane">
    <header class="art-header">
      <span class="art-title"><PaneIcon name="Clock" />Versions</span>
    </header>
    <div class="ms-version-list">
      <p v-if="!versions.length" class="ms-pane-empty">
        No versions yet — a build records one, and so does Save version.
      </p>
      <section v-for="day in days" v-else :key="day.day" class="ms-version-day">
        <h3 class="ms-version-dayhead">{{ day.day }}</h3>
        <div class="ms-version-card">
          <div
            v-for="v in day.versions"
            :key="v.id"
            class="ms-version"
            data-testid="manuscript-version"
          >
            <div class="ms-version-body">
              <span class="ms-version-chip">v{{ v.seq }}</span
              ><span class="ms-version-title">{{
                people.find((p) => p.id === v.by_user_id)?.name ?? v.title
              }}</span
              ><span
                v-if="lines.has(v.seq)"
                class="ms-version-lines"
                :aria-label="`${lines.get(v.seq)!.added} lines added, ${lines.get(v.seq)!.removed} removed`"
                ><span class="ms-version-added">+{{ lines.get(v.seq)!.added }}</span>
                <span class="ms-version-removed">−{{ lines.get(v.seq)!.removed }}</span></span
              ><span class="ms-version-meta">{{ formatAgo(Date.parse(v.created_at) / 1000) }}</span>
            </div>
            <div class="relative">
              <button
                type="button"
                class="ms-version-more"
                :aria-label="`More for version ${v.seq}`"
                aria-haspopup="menu"
                :aria-expanded="menu === v.seq"
                @click="menu = menu === v.seq ? undefined : v.seq"
              >
                ···
              </button>
              <div
                v-if="menu === v.seq"
                role="menu"
                class="absolute right-0 z-20 w-52 rounded-[10px] border border-line-strong bg-surface p-1 shadow-[0_18px_48px_rgba(0,0,0,0.6)]"
              >
                <button
                  type="button"
                  role="menuitem"
                  class="flex min-h-[28px] w-full items-center gap-2 rounded-md py-1 pl-2 pr-1.5 text-left hover:bg-surface-2"
                  :disabled="restoring !== undefined || disabled"
                  @click="restoreVersion(v)"
                >
                  <PaneIcon
                    name="GitBranch"
                    width="13"
                    height="13"
                    class="shrink-0 text-fg-subtle"
                  /><span class="type-small text-fg">{{
                    restoring === v.seq ? 'Restoring…' : 'Restore this version'
                  }}</span>
                </button>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
    <p v-if="versions.length" class="ms-version-count">
      Showing {{ versions.length }} of {{ versions.length }} version{{
        versions.length === 1 ? '' : 's'
      }}
    </p>
  </div>
</template>
