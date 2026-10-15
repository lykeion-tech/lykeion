<script setup lang="ts">
import { computed, ref } from 'vue';
import Icon from './Icon.vue';
import { useRunMessages } from '../api/queries';
import type { AgentRun, RunMessage } from '../api/types';
import { roleLabel } from '../lib/studies';

const props = defineProps<{ run: AgentRun; machine: string }>();
const open = ref(false);
const messages = useRunMessages(() => props.run, true);
const commands = computed(() => (messages.data.value ?? []).filter((m) => m.type === 'tool_use'));
const said = computed(() => {
  const text = [...(messages.data.value ?? [])].reverse().find((m) => m.type === 'text');
  return typeof text?.content === 'string' ? text.content : '';
});
const harness = computed(
  () => ({ claude: 'Claude Code', codex: 'Codex' })[props.run.harness] ?? props.run.harness,
);
const status = computed(() =>
  props.run.status === 'completed'
    ? ''
    : props.run.status === 'queued' && props.run.queue_reason
      ? `Queued: ${props.run.queue_reason.replace(/_/g, ' ')}`
      : props.run.status[0]!.toUpperCase() + props.run.status.slice(1),
);
const time = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
// Preserve the full recorded content for inspection in sequence order.
function content(m: RunMessage) {
  return typeof m.content === 'string' ? m.content : JSON.stringify(m.content, null, 2);
}
</script>
<template>
  <div class="who">
    <span class="avatar agent">{{ roleLabel(run.role)[0] }}</span
    ><span>{{ roleLabel(run.role) }}</span
    ><span class="tm"
      >{{ harness }}<template v-if="machine"> on {{ machine }}</template> ·
      {{ time(run.started_at ?? run.created_at) }}</span
    ><span v-if="status" class="chip">{{ status }}</span>
  </div>
  <div class="agent-turn">
    <p v-if="messages.isError.value" class="run-error run-read-error" role="alert">
      Run messages: {{ messages.error.value?.message }}
      <button class="text-button" :disabled="messages.isFetching.value" @click="messages.refetch()">
        Retry run messages
      </button>
    </p>
    <p v-if="messages.isPending.value && run.attempt > 0" role="status">Loading run messages…</p>
    <button
      v-if="messages.data.value?.length"
      class="ran"
      :aria-expanded="open"
      @click="open = !open"
    >
      <Icon :name="open ? 'down' : 'right'" :size="12" /><template v-if="commands.length"
        >Ran {{ commands.length }} {{ commands.length === 1 ? 'command' : 'commands' }}</template
      ><template v-else>Recorded messages</template>
    </button>
    <ol v-if="open" class="commands run-messages" aria-label="Recorded run messages">
      <li v-for="m in messages.data.value ?? []" :key="m.seq" :data-message-type="m.type">
        <strong
          >{{ m.type.replace(/_/g, ' ') }}<template v-if="m.tool"> · {{ m.tool }}</template></strong
        >
        <pre>{{ content(m) }}</pre>
      </li>
    </ol>
    <p v-if="said && !open">{{ said }}</p>
    <p v-if="run.error" class="run-error">{{ run.error }}</p>
  </div>
</template>
<style scoped>
.who {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13.5px;
  margin: 8px 0 6px 14px;
}
.tm {
  color: var(--muted);
  font-size: 12.5px;
}
.avatar.agent {
  background: #fff3d6;
  color: #b5841b;
}
.agent-turn {
  margin: 0 0 14px 24px;
  border-left: 1.5px solid var(--border);
  padding-left: 14px;
  font-size: 13.5px;
  overflow-wrap: anywhere;
}
.ran {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 500;
  margin-bottom: 6px;
}
.commands {
  margin: 0 0 8px;
  padding-left: 18px;
  font-size: 12.5px;
  color: var(--muted);
}
.commands code {
  font-family: 'JetBrains Mono', ui-monospace, monospace;
  font-size: 12px;
}
.run-error {
  color: var(--error-text);
}
.run-messages pre {
  margin: 4px 0 10px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: inherit;
}
</style>
