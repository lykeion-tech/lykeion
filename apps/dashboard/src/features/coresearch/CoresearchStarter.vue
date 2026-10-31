<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { useMe, useRuntimes } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { backendLabel } from '../../lib/backends';
import { chatPath, keys, NO_MACHINE, ownOnlineClis, projectPath, titleFromPrompt } from './api';
import type { ChatRow, Cli, ProjectRow } from './types';
import CliIcon from './CliIcon.vue';
import './l2.css';
const me = useMe();
const runtimes = useRuntimes();
const router = useRouter();
const client = useQueryClient();
const clis = computed(() => ownOnlineClis(runtimes.data.value ?? [], me.data.value?.id));
const selected = ref<Cli | null>(null);
const effectiveCli = computed(() =>
  created.value
    ? clis.value.find((c) => c === created.value?.cli)
    : (clis.value.find((c) => c === selected.value) ?? clis.value[0]),
);
const draft = ref('');
const busy = ref(false);
const error = ref('');
// Retain completed creation steps so a rejected turn can be retried without
// silently creating duplicate projects or chats.
const created = ref<{ workspace: string; project: string; chat?: string; cli: Cli } | null>(null);
watch(workspaceId, () => {
  created.value = null;
  error.value = '';
});
const blocker = computed(() => (clis.value.length ? null : NO_MACHINE));
const canSend = computed(() => !!draft.value.trim() && !!effectiveCli.value && !busy.value);
const hour = new Date().getHours();
const greeting = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
function magnify(event: MouseEvent) {
  const root = event.currentTarget as HTMLElement;
  for (const tile of root.querySelectorAll<HTMLElement>('.cli-dock-tile')) {
    const rect = tile.getBoundingClientRect();
    const t = Math.max(0, 1 - Math.abs(event.clientX - (rect.left + rect.width / 2)) / 150);
    tile.style.transform = `scale(${1 + t * 0.6}) translateY(${-t * 7}px)`;
    tile.style.zIndex = String(10 + Math.round(t * 10));
  }
}
function resetDock(event: MouseEvent) {
  for (const tile of (event.currentTarget as HTMLElement).querySelectorAll<HTMLElement>(
    '.cli-dock-tile',
  )) {
    tile.style.transform = '';
    tile.style.zIndex = '';
  }
}
async function send() {
  if (!canSend.value || !effectiveCli.value) return;
  const workspace = workspaceId.value;
  const cli = effectiveCli.value;
  const body = draft.value.trim();
  busy.value = true;
  error.value = '';
  try {
    let progress = created.value;
    if (!progress || progress.workspace !== workspace) {
      const project = await api<ProjectRow>(
        'POST',
        '/api/coresearch/projects',
        { name: titleFromPrompt(body) },
        undefined,
        { workspace },
      );
      progress = { workspace, project: project.id, cli };
      if (workspaceId.value === workspace) created.value = progress;
    }
    if (!progress.chat) {
      const chat = await api<ChatRow>(
        'POST',
        `${projectPath(progress.project)}/chats`,
        { cli: progress.cli },
        undefined,
        { workspace },
      );
      progress.chat = chat.id;
    }
    await api('POST', `${chatPath(progress.project, progress.chat)}/turns`, { body }, undefined, {
      workspace,
    });
    await client.invalidateQueries({ queryKey: keys.coresearchProjects(workspace) });
    if (workspaceId.value === workspace) {
      draft.value = '';
      await router.push(`/coresearch/${progress.project}/${progress.chat}`);
    }
  } catch (e) {
    if (workspaceId.value === workspace) error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section
    class="coresearch flex min-h-0 flex-1 flex-col items-center justify-start gap-4 px-6 pt-[169px]"
  >
    <h1 class="type-title text-fg">
      {{ greeting }}{{ me.data.value ? `, ${me.data.value.name}` : '' }}
    </h1>
    <div class="w-full max-w-[660px]">
      <div class="composer-above">
        <div v-if="clis.length" class="cli-dock-scroll">
          <div class="cli-dock" @mousemove="magnify" @mouseleave="resetDock">
            <template v-for="cli in clis" :key="cli">
              <div v-if="cli === effectiveCli" class="cli-dock-selected" :data-cli="cli">
                <span
                  class="cli-tile"
                  :style="{
                    background: cli === 'claude' ? '#d97757' : '#ffffff',
                    color: cli === 'claude' ? '#ffffff' : '#1a1a1f',
                  }"
                  ><CliIcon :cli="cli" class="cli-tile-icon" /></span
                ><span class="cli-dock-name">{{ backendLabel(cli) }}</span>
              </div>
              <button
                v-else
                type="button"
                class="cli-dock-btn"
                :data-cli="cli"
                :title="backendLabel(cli)"
                :aria-label="backendLabel(cli)"
                :disabled="busy || !!created"
                @click="selected = cli"
              >
                <span
                  class="cli-tile cli-dock-tile"
                  :style="{
                    background: cli === 'claude' ? '#d97757' : '#ffffff',
                    color: cli === 'claude' ? '#ffffff' : '#1a1a1f',
                  }"
                  ><CliIcon :cli="cli" class="cli-tile-icon"
                /></span>
              </button>
            </template>
          </div>
        </div>
        <div v-else class="cli-dock cli-dock--empty" role="status">
          No agent CLIs detected — install Claude Code or Codex
        </div>
      </div>
      <div class="composer-card composer-card--hero">
        <div v-if="blocker" class="composer-blocker" role="status">{{ blocker }}</div>
        <textarea
          v-model="draft"
          class="composer-textarea"
          :rows="2"
          aria-label="Message the agent"
          placeholder="What are you running?"
          :disabled="busy"
          @keydown.enter.exact.prevent="!$event.isComposing && send()"
        />
        <div class="composer-toolbar">
          <div class="composer-tools">
            <div class="composer-mode" role="group" aria-label="Mode">
              <button type="button" class="composer-mode-tab is-on" :aria-pressed="true">
                Experiment
              </button>
            </div>
          </div>
          <div class="composer-tools">
            <button
              type="button"
              class="composer-send"
              :class="{ 'is-idle': !draft.trim() }"
              title="Send"
              aria-label="Send"
              :disabled="!canSend"
              @click="send"
            >
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                <path
                  d="M8 13V3.5M8 3.5 4.5 7M8 3.5 11.5 7"
                  stroke="currentColor"
                  stroke-width="1.5"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
            </button>
          </div>
        </div>
      </div>
      <p v-if="error || runtimes.error.value" class="composer-start-error" role="alert">
        {{ error || runtimes.error.value?.message }}
      </p>
    </div>
  </section>
</template>
