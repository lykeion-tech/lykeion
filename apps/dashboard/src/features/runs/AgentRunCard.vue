<script setup lang="ts">
import { computed } from 'vue';
import Icon from '../../ui/Icon.vue';
import type { AgentRun } from '../../api/types';
import { roleLabel, timeAgo } from '../../lib/studies';
import { agentDisplayName, agentNameWithoutCli } from '../../lib/agent-name';

const props = defineProps<{ run: AgentRun; study: string; position: number }>();
const emit = defineEmits<{ open: [] }>();
const name = computed(() => agentDisplayName(props.run.agent_name || roleLabel(props.run.role)));
const state = computed(
  () =>
    ({
      queued: 'Queued',
      dispatched: 'Working',
      running: 'Working',
      completed: 'Done',
      failed: 'Failed',
      cancelled: 'Cancelled',
    })[props.run.status],
);
// The latest thing the agent wrote, as plain text for a three-line preview.
// The runs listing carries it, so a card never reads the run's messages.
const preview = computed(() => {
  const text = props.run.latest_text;
  if (text)
    return text
      .replace(/```[\s\S]*?(```|$)/g, ' ')
      .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/[#>*_`|]+/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
  if (props.run.status === 'queued')
    return props.run.queue_reason ? `Queued: ${props.run.queue_reason.replace(/_/g, ' ')}` : '';
  return props.run.error;
});
const lastActive = computed(
  () => props.run.last_activity_at ?? props.run.started_at ?? props.run.created_at,
);
</script>
<template>
  <article class="agent-card" :data-status="run.status">
    <header>
      <span class="status-dot" :title="state" />
      <span class="avatar agent">{{ name[0] }}</span>
      <h3>{{ agentNameWithoutCli(run.agent_name || roleLabel(run.role), run.harness) }}</h3>
      <button
        class="icon-button open-run"
        aria-haspopup="dialog"
        :aria-label="`Open transcript: ${name} (${state}), Run ${position}`"
        @click="emit('open')"
      >
        <Icon name="external" :size="15" />
      </button>
    </header>
    <p class="preview">{{ preview }}</p>
    <footer>
      <time :datetime="lastActive">{{ timeAgo(lastActive) }}</time>
    </footer>
  </article>
</template>
<style scoped>
.agent-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 176px;
  padding: 14px 12px;
  border: 1px solid var(--card-border);
  border-radius: 8px;
  box-shadow: 0 1px 2px #00000014;
  background: #ffffff16;
  font-size: 14px;
  line-height: 19px;
  min-width: 0;
}
header {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}
h3 {
  flex: 1;
  min-width: 0;
  font-weight: 500;
  overflow-wrap: anywhere;
  line-height: 20px;
}
.status-dot {
  margin-top: 6px;
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--icon-faint);
}
[data-status='running'] .status-dot,
[data-status='dispatched'] .status-dot {
  margin-top: 6px;
  background: var(--primary);
}
[data-status='completed'] .status-dot {
  margin-top: 6px;
  background: var(--stage-validation-dot);
}
[data-status='failed'] .status-dot {
  margin-top: 6px;
  background: var(--error-text);
}
[data-status='queued'] .status-dot,
[data-status='cancelled'] .status-dot {
  margin-top: 6px;
  background: none;
  box-shadow: inset 0 0 0 1.5px var(--control-border);
}
.avatar.agent {
  background: #fff3d6;
  color: #b5841b;
}
.open-run:hover {
  background: var(--hover);
  color: var(--text);
}
small {
  color: var(--muted);
  font-size: 12px;
}
.preview {
  flex: 1;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
footer {
  padding-top: 10px;
  border-top: 1px solid var(--border);
  font-size: 11px;
  color: var(--muted);
}
</style>
