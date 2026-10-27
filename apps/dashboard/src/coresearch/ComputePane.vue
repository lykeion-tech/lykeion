<script setup lang="ts">
import { computed } from 'vue';
import { useRuntimes } from '../api/queries';
import { useCoresearchCells } from './api';
import { chatRuntime } from './notebook-model';
import type { ProjectRow, ChatRow } from './types';
import KernelControls from './KernelControls.vue';
const props = defineProps<{ project: ProjectRow; chat: ChatRow; owner: boolean }>();
const runtimes = useRuntimes(),
  cells = useCoresearchCells(
    () => props.project.id,
    () => props.chat.id,
  );
const runtime = computed(() => chatRuntime(props.chat, runtimes.data.value ?? []));
const queued = computed(() => cells.data.value?.filter((c) => c.status === 'queued').length ?? 0);
</script>
<template>
  <section class="compute-panel" data-testid="compute-panel" role="tabpanel" aria-label="Compute">
    <header class="art-header">
      <span class="art-title"
        ><svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path
            d="M4 4h8v8H4zM6 1v3M10 1v3M6 12v3M10 12v3M1 6h3M1 10h3M12 6h3M12 10h3"
            stroke="currentColor"
            stroke-width="1.2"
          /></svg
        >Compute</span
      >
    </header>
    <div class="compute-panel-body">
      <p v-if="!chat.daemon_id" class="px-4 pb-4 type-small text-fg-subtle">
        No machine bound<span class="mt-1 block type-meta text-fg-tertiary"
          >Run a cell or send a turn to bind this chat to its owner's machine.</span
        >
      </p>
      <p v-else-if="runtimes.error.value" class="px-4 py-2 type-small text-danger" role="alert">
        {{ runtimes.error.value.message }}
      </p>
      <p
        v-else-if="runtimes.isPending.value"
        class="px-4 py-2 type-small text-fg-subtle"
        role="status"
      >
        Loading machine…
      </p>
      <p v-else-if="!runtime" class="px-4 pb-4 type-small text-fg-subtle">
        Machine unavailable<span
          v-if="chat.machine_name"
          class="mt-1 block type-meta text-fg-tertiary"
          >{{ chat.machine_name }}</span
        >
      </p>
      <div v-else class="px-4 pb-4">
        <div class="flex items-center justify-between gap-2">
          <span class="type-body">{{ runtime.custom_name || runtime.device_name }}</span
          ><span
            class="type-small"
            :class="runtime.status === 'online' ? 'text-success' : 'text-fg-subtle'"
            >{{ runtime.status }}</span
          >
        </div>
        <p class="mt-1 type-meta text-fg-tertiary">
          Machine last seen:
          <time class="break-all" :datetime="runtime.last_seen_at">{{ runtime.last_seen_at }}</time>
        </p>
      </div>
      <div class="border-t border-hairline px-4 py-3">
        <p class="type-small text-fg-subtle">Kernel · {{ chat.kernel_state }}</p>
        <p class="mt-1 type-meta text-fg-tertiary">
          Kernel last seen:
          <time v-if="chat.kernel_seen_at" class="break-all" :datetime="chat.kernel_seen_at">{{
            chat.kernel_seen_at
          }}</time
          ><span v-else>Not reported</span>
        </p>
        <p v-if="cells.error.value" class="mt-2 type-small text-danger" role="alert">
          {{ cells.error.value.message }}
        </p>
        <p v-else-if="cells.isPending.value" class="mt-2 type-small text-fg-subtle">
          Loading cell queue…
        </p>
        <p v-else class="mt-2 type-small text-fg-subtle">
          {{ queued === 1 ? '1 queued cell' : `${queued} queued cells` }}
        </p>
        <div v-if="owner" class="mt-3">
          <KernelControls
            :project-id="project.id"
            :chat-id="chat.id"
            :disabled="!!project.archived_at"
          />
        </div>
      </div>
    </div>
  </section>
</template>
