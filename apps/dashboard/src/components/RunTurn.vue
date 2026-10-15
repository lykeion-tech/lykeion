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
// A command's own text when the tool carried one, else its input, shortened.
function preview(m: RunMessage) {
  const c = m.content as { command?: unknown } | null;
  return c && typeof c === 'object' && typeof c.command === 'string'
    ? c.command
    : JSON.stringify(m.content).slice(0, 200);
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
    <button v-if="commands.length" class="ran" :aria-expanded="open" @click="open = !open">
      <Icon :name="open ? 'down' : 'right'" :size="12" />Ran {{ commands.length }}
      {{ commands.length === 1 ? 'command' : 'commands' }}
    </button>
    <ol v-if="open" class="commands">
      <li v-for="m in commands" :key="m.seq">
        <code>{{ m.tool }}</code> {{ preview(m) }}
      </li>
    </ol>
    <p v-if="said">{{ said }}</p>
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
</style>
