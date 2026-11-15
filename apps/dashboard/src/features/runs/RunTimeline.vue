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
  studyColumn?: string;
  // A line for each earlier attempt of a retried run, in attempt order, drawn
  // before the first block of a later attempt, or last when none follows.
  dividers?: { attempt: number; text: string }[];
}>();
const placed = computed(() => {
  const dividers = props.dividers ?? [];
  let next = 0;
  const before = props.blocks.map((block) => {
    const attempt = block.messages[0]?.attempt ?? 0;
    const start = next;
    while (next < dividers.length && dividers[next]!.attempt < attempt) next++;
    return dividers.slice(start, next);
  });
  return { before, after: dividers.slice(next) };
});
const marked = computed(() =>
  props.blocks.map((block, index) => {
    const steps = block.kind === 'activity' ? activitySteps(block.messages) : [];
    const markedSteps = isActivityGroup(steps) ? steps : steps.slice(0, 1);
    const mark =
      block.kind === 'text' || block.kind === 'reask'
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
    return { ...block, mark, dividers: placed.value.before[index]! };
  }),
);
</script>
<template>
  <ol class="turn-timeline" aria-label="Transcript">
    <template v-for="block in marked" :key="block.key">
      <li
        v-for="d in block.dividers"
        :key="`divider-${d.attempt}`"
        class="turn-item attempt-divider mark-queued"
      >
        {{ d.text }}
      </li>
      <li class="turn-item" :class="block.mark">
        <slot v-if="block.kind === 'text'" name="text" :block="block" />
        <details v-else-if="block.kind === 'reask'" class="reask type-caption">
          <summary>{{ block.text }}</summary>
          <ul>
            <li v-for="problem in block.problems" :key="problem">{{ problem }}</li>
          </ul>
        </details>
        <RunActivity
          v-else
          :messages="block.messages"
          :study-column="studyColumn"
          :active="active && (attempt === undefined || block.messages[0]?.attempt === attempt)"
        />
      </li>
    </template>
    <li
      v-for="d in placed.after"
      :key="`divider-${d.attempt}`"
      class="turn-item attempt-divider mark-queued"
    >
      {{ d.text }}
    </li>
    <li v-if="ending" class="turn-item turn-ending" :class="ending.mark">
      <div :role="active || queued ? 'status' : undefined">
        <RunStatus :text="ending.text" :working="active" />
      </div>
    </li>
  </ol>
</template>
<style scoped>
.reask {
  color: var(--step-muted);
}
.reask ul {
  margin: 4px 0 0;
  padding-left: 18px;
}
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
.turn-item:has(> .activity-single)::before {
  top: 13.5px;
}
.turn-item.mark-fail:has(> .activity-single)::before {
  top: 12.5px;
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
