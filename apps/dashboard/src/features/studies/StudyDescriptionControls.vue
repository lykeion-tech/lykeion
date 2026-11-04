<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { SmilePlus } from '@lucide/vue';
import AvatarPicker from '../../ui/AvatarPicker.vue';
import Icon from '../../ui/Icon.vue';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { formatSize } from '../../lib/attachments';

type Reaction = { emoji: string; count: number; mine: boolean };
type Attachment = { id: string; name: string; size_bytes: number };
const props = defineProps<{ cardId: string; attachments: Attachment[] }>();
const emit = defineEmits<{ download: [Attachment] }>();
const client = useQueryClient();
const key = computed(() => ['ws', workspaceId.value, 'study-reactions', props.cardId]);
const reactions = useQuery({
  queryKey: key,
  queryFn: ({ queryKey }) =>
    api<Reaction[]>('GET', `/api/cards/${queryKey[3]}/reactions`, undefined, undefined, {
      workspace: String(queryKey[1]),
    }),
});
const picker = ref(false),
  full = ref(false),
  busy = ref(false),
  uploading = ref(false),
  error = ref('');
const host = ref<HTMLElement>();
const input = ref<HTMLInputElement>();
const quick = ['👍', '👌', '❤️', '✅', '🎉', '😕', '🚀', '👀'];
function outside(e: PointerEvent) {
  if (host.value && !host.value.contains(e.target as Node)) {
    picker.value = false;
    full.value = false;
  }
}
onMounted(() => window.addEventListener('pointerdown', outside));
onUnmounted(() => window.removeEventListener('pointerdown', outside));
async function toggle(emoji: string) {
  if (busy.value) return;
  const ws = workspaceId.value,
    id = props.cardId;
  const mine = reactions.data.value?.some((r) => r.emoji === emoji && r.mine);
  picker.value = false;
  full.value = false;
  busy.value = true;
  error.value = '';
  try {
    await api(mine ? 'DELETE' : 'PUT', `/api/cards/${id}/reactions`, { emoji }, undefined, {
      workspace: ws,
    });
    await client.invalidateQueries({ queryKey: ['ws', ws, 'study-reactions', id] });
  } catch (e) {
    if (ws === workspaceId.value && id === props.cardId) error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function attach(event: Event) {
  const element = event.target as HTMLInputElement;
  const files = Array.from(element.files ?? []);
  element.value = '';
  if (!files.length || uploading.value) return;
  const ws = workspaceId.value,
    id = props.cardId;
  uploading.value = true;
  error.value = '';
  try {
    const ids: string[] = [];
    for (const file of files) {
      const form = new FormData();
      form.append('file', file);
      const uploaded = await api<{ id: string }>('POST', '/api/attachments', form, undefined, {
        workspace: ws,
      });
      ids.push(uploaded.id);
    }
    await api('POST', `/api/cards/${id}/attachments`, { attachment_ids: ids }, undefined, {
      workspace: ws,
    });
    await client.invalidateQueries({ queryKey: keys.card(ws, id) });
  } catch (e) {
    if (ws === workspaceId.value && id === props.cardId) error.value = (e as Error).message;
  } finally {
    uploading.value = false;
  }
}
</script>

<template>
  <div
    ref="host"
    class="description-controls"
    @keydown.esc="
      picker = false;
      full = false;
    "
  >
    <div class="description-actions">
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
          class="description-action"
          aria-label="Add reaction"
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
          <div class="reaction-quick">
            <button v-for="emoji in quick" :key="emoji" :aria-label="emoji" @click="toggle(emoji)">
              {{ emoji }}
            </button>
          </div>
          <button class="reaction-more" @click="full = true">More emojis…</button>
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
      <button
        class="description-action"
        aria-label="Attach files"
        :disabled="uploading"
        @click="input?.click()"
      >
        <Icon name="attachment" :size="15" />
      </button>
      <input ref="input" type="file" multiple hidden @change="attach" />
      <span v-if="uploading" role="status" class="attachment-status">Uploading…</span>
    </div>
    <ul v-if="attachments.length" class="description-files" aria-label="Study attachments">
      <li v-for="file in attachments" :key="file.id">
        <button @click="emit('download', file)">{{ file.name }}</button
        ><span>{{ formatSize(file.size_bytes) }}</span>
      </li>
    </ul>
    <p v-if="reactions.isError?.value" class="inline-error" role="alert">
      Reactions could not be loaded.
      <button class="reaction-more" @click="reactions.refetch()">Retry</button>
    </p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.description-controls {
  margin: 10px 0 20px;
  color: var(--muted);
}
.description-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
}
.description-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.description-action:hover,
.description-action:focus-visible {
  background: var(--hover);
  color: var(--text);
}
.description-action:disabled {
  opacity: 0.5;
  cursor: wait;
}
.picker-anchor {
  position: relative;
}
.reaction-picker {
  position: absolute;
  z-index: 20;
  top: 28px;
  left: 0;
  padding: 8px;
  background: var(--fill-menu);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 8px 24px #0004;
}
.reaction-quick {
  display: flex;
  gap: 3px;
}
.reaction-quick button {
  width: 32px;
  height: 32px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  font-size: 18px;
  cursor: pointer;
}
.reaction-quick button:hover,
.reaction-more:hover {
  background: var(--hover);
}
.reaction-more {
  width: 100%;
  margin-top: 4px;
  padding: 5px;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.reaction-chip {
  border: 1px solid var(--border);
  border-radius: 20px;
  padding: 3px 8px;
  background: var(--fill-chip);
  color: inherit;
  cursor: pointer;
}
.reaction-chip.mine {
  color: var(--accent);
  border-color: var(--accent);
}
.description-files {
  list-style: none;
  margin: 8px 0;
  padding: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.description-files li {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 8px;
  font-size: 12px;
}
.description-files button {
  border: 0;
  padding: 0;
  color: var(--accent);
  background: transparent;
  cursor: pointer;
}
.attachment-status {
  font-size: 11px;
  margin: 5px 0 0;
}
</style>
