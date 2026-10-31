<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { chatPath } from './api';
const props = defineProps<{
  projectId: string;
  chatId: string;
  owner: boolean;
  activeRun?: string;
  disabled?: boolean;
}>();
const emit = defineEmits<{ sent: [] }>();
const client = useQueryClient();
const draft = ref(''),
  error = ref(''),
  busy = ref(false);
const canSend = computed(
  () =>
    !!draft.value.trim() && !busy.value && !props.disabled && (!props.owner || !props.activeRun),
);
const textarea = ref<HTMLTextAreaElement>();
watch(draft, async () => {
  await nextTick();
  if (textarea.value?.style) resize(textarea.value);
});
const label = computed(() => (props.owner ? 'Send' : 'Comment'));
watch(
  () => [workspaceId.value, props.projectId, props.chatId],
  () => {
    draft.value = '';
    error.value = '';
  },
);
async function perform(stop = false) {
  if (
    busy.value ||
    props.disabled ||
    (!stop && !canSend.value) ||
    (stop && (!props.owner || !props.activeRun))
  )
    return;
  const workspace = workspaceId.value,
    project = props.projectId,
    chat = props.chatId,
    body = draft.value.trim();
  const current = () =>
    workspaceId.value === workspace && props.projectId === project && props.chatId === chat;
  busy.value = true;
  error.value = '';
  try {
    const suffix = stop
      ? `turns/${encodeURIComponent(props.activeRun!)}/cancel`
      : props.owner
        ? 'turns'
        : 'comments';
    await api(
      'POST',
      `${chatPath(project, chat)}/${suffix}`,
      stop ? undefined : { body },
      undefined,
      { workspace },
    );
    if (current()) {
      if (!stop && draft.value.trim() === body) draft.value = '';
      emit('sent');
    }
    await client.invalidateQueries({ queryKey: ['ws', workspace, 'coresearch', project] });
  } catch (e) {
    if (current()) error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
function resize(el: HTMLTextAreaElement) {
  el.style.height = 'auto';
  el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
}
</script>
<template>
  <div class="composer-card composer-card--docked">
    <p v-if="error" class="composer-blocker" role="alert">{{ error }}</p>
    <textarea
      ref="textarea"
      v-model="draft"
      class="composer-textarea"
      :rows="1"
      :aria-label="owner ? 'Message the agent' : 'Comment on this chat'"
      :placeholder="owner ? 'What would you like to do next?' : 'Add a comment…'"
      :disabled="disabled || busy"
      @keydown.enter.exact.prevent="!$event.isComposing && perform()"
    />
    <div class="composer-toolbar">
      <div class="composer-tools">
        <span v-if="!owner" class="type-small text-fg-subtle">Comment</span>
      </div>
      <div class="composer-tools">
        <button
          v-if="owner && activeRun"
          type="button"
          class="composer-send composer-send--stop"
          title="Stop"
          aria-label="Stop"
          :disabled="busy || disabled"
          @click="perform(true)"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <rect x="4.75" y="4.75" width="6.5" height="6.5" rx="1.5" fill="currentColor" />
          </svg>
        </button>
        <button
          type="button"
          class="composer-send"
          :class="{ 'is-idle': !draft.trim() }"
          :title="label"
          :aria-label="label"
          :disabled="!canSend"
          @click="perform()"
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path
              d="M8 13V3.5M8 3.5 4.5 7M8 3.5 11.5 7"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>
