<script setup lang="ts">
import type { AgentDefinition } from '../../api/types';
import Aristotle from './Aristotle.vue';
import AvatarDisplay from '../../ui/AvatarDisplay.vue';
withDefaults(
  defineProps<{ agent?: AgentDefinition; size?: number; fallbackAristotle?: boolean }>(),
  { size: 64, fallbackAristotle: false },
);
</script>
<template>
  <span
    class="chat-agent-avatar"
    :class="{ 'is-aristotle': !agent?.avatar_url && (agent?.built_in_key === 'aristotle' || (!agent && fallbackAristotle)) }"
    :style="{ width: size + 'px', height: size + 'px' }"
    aria-hidden="true"
  >
    <AvatarDisplay
      v-if="agent?.avatar_url"
      :value="agent.avatar_url.slice(6)"
      :size="Math.round(size * 0.6)"
    />
    <Aristotle
      v-else-if="agent?.built_in_key === 'aristotle' || (!agent && fallbackAristotle)"
      still
      :style="{ width: Math.round(size * 0.75) + 'px', height: Math.round(size * 0.75) + 'px' }"
    />
    <AvatarDisplay v-else value="bot" :size="Math.round(size * 0.55)" />
  </span>
</template>
<style scoped>
.chat-agent-avatar {
  display: inline-grid;
  flex: none;
  place-items: center;
  overflow: hidden;
  border-radius: 50%;
  background: var(--input);
}
.chat-agent-avatar.is-aristotle {
  background: var(--palette-accent);
}
</style>
