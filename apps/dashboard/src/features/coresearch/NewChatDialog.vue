<script setup lang="ts">
import { computed, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { useMe, useRuntimes } from '../../api/queries';
import { workspaceId } from '../../api/session';
import { backendLabel } from '../../lib/backends';
import { NO_MACHINE, ownOnlineClis, projectPath, keys } from './api';
import type { ChatRow, Cli } from './types';
import CoresearchModal from './CoresearchModal.vue';
import CliIcon from './CliIcon.vue';
const props = defineProps<{ projectId: string }>();
const emit = defineEmits<{ close: []; created: [id: string] }>();
const me = useMe(),
  runtimes = useRuntimes(),
  client = useQueryClient();
const clis = computed(() => ownOnlineClis(runtimes.data.value ?? [], me.data.value?.id));
const busy = ref(false),
  error = ref('');
async function create(cli: Cli) {
  if (busy.value || !clis.value.includes(cli)) return;
  const workspace = workspaceId.value,
    project = props.projectId;
  busy.value = true;
  error.value = '';
  try {
    const chat = await api<ChatRow>('POST', `${projectPath(project)}/chats`, { cli }, undefined, {
      workspace,
    });
    await client.invalidateQueries({ queryKey: keys.coresearchChats(workspace, project) });
    if (workspaceId.value === workspace && props.projectId === project) emit('created', chat.id);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <CoresearchModal label="New chat" @close="emit('close')"
    ><template #default="{ close }"
      ><div
        class="w-full max-w-[480px] overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
      >
        <div class="flex items-center justify-between px-5 pb-1 pt-4">
          <h2 class="type-heading text-fg">New chat</h2>
          <button
            type="button"
            aria-label="Close new chat"
            class="grid h-7 w-7 place-items-center rounded-md text-fg-subtle hover:bg-surface-2"
            @click="close"
          >
            ×
          </button>
        </div>
        <div class="px-5 py-4">
          <p v-if="error || runtimes.error.value" role="alert" class="type-small text-fg-subtle">
            {{ error || runtimes.error.value?.message }}
          </p>
          <p v-if="!clis.length" class="type-body text-fg-subtle">{{ NO_MACHINE }}</p>
          <div v-else class="cli-dock">
            <button
              v-for="cli in clis"
              :key="cli"
              type="button"
              class="cli-dock-btn"
              :data-cli="cli"
              :aria-label="`Start ${backendLabel(cli)} chat`"
              :title="backendLabel(cli)"
              :disabled="busy"
              @click="create(cli)"
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
          </div>
        </div></div></template
  ></CoresearchModal>
</template>
