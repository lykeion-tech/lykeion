<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { SmilePlus } from '@lucide/vue';
import AvatarPicker from '../../ui/AvatarPicker.vue';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';

type Reaction = { emoji: string; count: number; mine: boolean };
const props = defineProps<{ studyId: string; runId: string }>();
const client = useQueryClient();
const queryKey = computed(() => [
  'ws',
  workspaceId.value,
  'run-reactions',
  props.studyId,
  props.runId,
]);
const reactions = useQuery({
  queryKey,
  queryFn: ({ queryKey: key }) =>
    api<Reaction[]>('GET', `/api/cards/${key[3]}/runs/${key[4]}/reactions`, undefined, undefined, {
      workspace: String(key[1]),
    }),
});
const host = ref<HTMLElement>(),
  picker = ref(false),
  full = ref(false),
  busy = ref(false),
  error = ref('');
const quick = ['👍', '👌', '❤️', '✅', '🎉', '😕', '🚀', '👀'];
function outside(event: PointerEvent) {
  if (host.value && !host.value.contains(event.target as Node)) {
    picker.value = false;
    full.value = false;
  }
}
onMounted(() => window.addEventListener('pointerdown', outside));
onUnmounted(() => window.removeEventListener('pointerdown', outside));
async function toggle(emoji: string) {
  if (busy.value) return;
  const ws = workspaceId.value,
    study = props.studyId,
    run = props.runId;
  const mine = reactions.data.value?.some((reaction) => reaction.emoji === emoji && reaction.mine);
  picker.value = false;
  full.value = false;
  busy.value = true;
  error.value = '';
  try {
    await api(
      mine ? 'DELETE' : 'PUT',
      `/api/cards/${study}/runs/${run}/reactions`,
      { emoji },
      undefined,
      { workspace: ws },
    );
    await client.invalidateQueries({ queryKey: ['ws', ws, 'run-reactions', study, run] });
  } catch (e) {
    if (ws === workspaceId.value && study === props.studyId && run === props.runId)
      error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div
    ref="host"
    class="run-reactions"
    @keydown.esc.stop="
      picker = false;
      full = false;
    "
  >
    <button
      v-for="reaction in reactions.data.value ?? []"
      :key="reaction.emoji"
      class="reaction-chip"
      :class="{ mine: reaction.mine }"
      :aria-pressed="reaction.mine"
      :disabled="busy"
      @click="toggle(reaction.emoji)"
    >
      {{ reaction.emoji }} {{ reaction.count }}
    </button>
    <div class="picker-anchor">
      <button
        class="reaction-action"
        aria-label="Add run reaction"
        :aria-expanded="picker"
        @click="
          picker = !picker;
          full = false;
        "
      >
        <SmilePlus :size="15" />
      </button>
      <div
        v-if="picker && !full"
        class="reaction-picker"
        role="group"
        aria-label="Choose a reaction"
      >
        <div class="quick">
          <button v-for="emoji in quick" :key="emoji" :aria-label="emoji" @click="toggle(emoji)">
            {{ emoji }}
          </button>
        </div>
        <button class="more" @click="full = true">More emojis…</button>
      </div>
      <AvatarPicker
        v-if="picker && full"
        model-value=""
        emoji-only
        start-expanded
        @update:model-value="toggle"
        @close="
          picker = false;
          full = false;
        "
      />
    </div>
    <span v-if="error || reactions.isError?.value" class="reaction-error" role="alert"
      >{{ error || 'Reactions could not be loaded.' }}
      <button v-if="reactions.isError?.value" @click="reactions.refetch()">Retry</button></span
    >
  </div>
</template>
<style scoped>
.run-reactions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 5px;
  color: var(--muted);
}
.picker-anchor {
  position: relative;
}
.reaction-action {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.reaction-action:hover,
.reaction-action:focus-visible {
  background: var(--hover);
  color: var(--text);
}
.reaction-chip {
  border: 1px solid var(--border);
  border-radius: 20px;
  padding: 3px 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.reaction-chip.mine {
  color: var(--accent);
  border-color: var(--accent);
}
.reaction-picker {
  position: absolute;
  z-index: 20;
  top: 28px;
  right: 0;
  padding: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 8px 24px #0004;
}
.picker-anchor :deep(.avatar-picker) {
  left: auto;
  right: 0;
  top: 28px;
}
.quick {
  display: flex;
  gap: 3px;
}
.quick button {
  width: 32px;
  height: 32px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  font-size: 18px;
  cursor: pointer;
}
.quick button:hover,
.more:hover {
  background: var(--hover);
}
.more {
  width: 100%;
  margin-top: 4px;
  padding: 5px;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.reaction-error {
  color: var(--error);
  font-size: 12px;
}
</style>
