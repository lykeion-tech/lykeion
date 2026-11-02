<script setup lang="ts">
import { computed } from 'vue';
import RunActivity from './RunActivity.vue';
import RunStatus from './RunStatus.vue';
import { activitySteps, isActivityGroup, type TranscriptBlock } from './run-transcript';
const props = defineProps<{
  blocks: TranscriptBlock[];
  active: boolean;
  attempt?: number;
  ending?: { mark: string; text: string };
  queued?: boolean;
  // The line drawn where one attempt's blocks end and the next attempt's begin.
  divider?: (endedAttempt: number) => string;
}>();
const marked = computed(() =>
  props.blocks.map((block, index) => {
    const steps = block.kind === 'activity' ? activitySteps(block.messages) : [];
    const markedSteps = isActivityGroup(steps) ? steps : steps.slice(0, 1);
    const mark =
      block.kind === 'text'
        ? 'mark-message'
        : markedSteps.some((step) => step.error)
          ? 'mark-fail'
          : !markedSteps.some((step) => step.tool)
            ? 'mark-message'
            : props.active &&
                (props.attempt === undefined || block.messages[0]?.attempt === props.attempt) &&
                index === props.blocks.length - 1 &&
                markedSteps.some((step) => step.running)
              ? 'mark-run'
              : 'mark-ok';
    const attempt = block.messages[0]?.attempt;
    const before = props.blocks[index - 1]?.messages[0]?.attempt;
    const divider =
      props.divider && before !== undefined && attempt !== before ? props.divider(before) : '';
    return { ...block, mark, divider };
  }),
);
</script>
<template>
  <ol class="turn-timeline" aria-label="Transcript">
    <template v-for="block in marked" :key="block.key">
      <li v-if="block.divider" class="turn-item attempt-divider mark-queued">
        {{ block.divider }}
      </li>
      <li class="turn-item" :class="block.mark">
        <slot v-if="block.kind === 'text'" name="text" :block="block" />
        <RunActivity
          v-else
          :messages="block.messages"
          :active="active && (attempt === undefined || block.messages[0]?.attempt === attempt)"
        />
      </li>
    </template>
    <li v-if="ending" class="turn-item turn-ending" :class="ending.mark">
      <div :role="active || queued ? 'status' : undefined">
        <RunStatus :text="ending.text" :working="active" />
      </div>
    </li>
  </ol>
</template>
<style scoped>
.turn-timeline {
  position: relative;
  list-style: none;
  margin: 0;
  padding: 0 0 0 28px;
  display: flex;
  flex-direction: column;
  gap: 0;
}
/* The rail the marks sit on. */
.turn-timeline::before {
  content: '';
  position: absolute;
  left: 10px;
  top: 0;
  bottom: 0;
  width: 1px;
  background: var(--step-line);
}
.turn-item {
  position: relative;
  min-width: 0;
  padding-block: 5px;
}
.turn-item:has(> .run-activity) {
  padding-block: 0;
}
.turn-item:has(> .run-activity)::before {
  top: 9px;
}
.turn-item::before {
  content: '';
  position: absolute;
  left: -21px;
  top: 12px;
  width: 7px;
  height: 7px;
  box-sizing: border-box;
  border-radius: 50%;
  box-shadow: 0 0 0 4px var(--step-canvas);
}
.mark-message::before {
  border: 1px solid var(--step-muted);
  background: var(--step-canvas);
}
.mark-ok::before {
  background: var(--step-done);
}
.mark-queued::before {
  border: 1px dashed var(--step-muted);
  background: var(--step-canvas);
}
.mark-live::before {
  background: var(--stage-experimentation-dot);
  animation: turn-pulse 1.6s ease-in-out infinite;
}
.mark-run::before {
  border: 1.5px solid var(--primary);
  border-right-color: transparent;
  animation: turn-spin 1s linear infinite;
}
.mark-fail::before {
  left: -22px;
  top: 11px;
  width: 9px;
  height: 9px;
  border-radius: 0;
  background:
    linear-gradient(45deg, transparent 43%, var(--error-text) 43% 57%, transparent 57%),
    linear-gradient(-45deg, transparent 43%, var(--error-text) 43% 57%, transparent 57%),
    var(--step-canvas);
}
.turn-ending,
.attempt-divider {
  font-size: 12.5px;
  color: var(--muted);
}
@keyframes turn-pulse {
  50% {
    opacity: 0.35;
  }
}
@keyframes turn-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .turn-item::before {
    animation: none;
  }
}
</style>
