<script setup lang="ts">
import { computed, ref } from 'vue';
import AvatarDisplay from './AvatarDisplay.vue';
import AvatarPicker from './AvatarPicker.vue';
import Icon from './Icon.vue';
const props = defineProps<{ value: string; editable: boolean; fallback: string; busy?: boolean }>();
const emit = defineEmits<{ select: [value: string] }>();
const open = ref(false);
const emoji = computed(() => (props.value.startsWith('emoji:') ? props.value.slice(6) : ''));
function choose(value: string) {
  open.value = false;
  emit('select', value ? `emoji:${value}` : '');
}
</script>
<template>
  <div class="emoji-icon-wrap">
    <button
      v-if="editable"
      type="button"
      class="agent-avatar emoji-icon-button"
      aria-label="Change avatar"
      :disabled="busy"
      @click="open = !open"
    >
      <slot v-if="!emoji" /><AvatarDisplay
        v-if="emoji || !$slots.default"
        :value="emoji || fallback"
        :size="26"
      />
      <span class="emoji-icon-edit" aria-hidden="true"><Icon name="edit" :size="18" /></span>
    </button>
    <span v-else class="agent-avatar"
      ><slot v-if="!emoji" /><AvatarDisplay
        v-if="emoji || !$slots.default"
        :value="emoji || fallback"
        :size="26"
    /></span>
    <AvatarPicker
      v-if="open && editable"
      :model-value="emoji"
      emoji-only
      @update:model-value="choose"
      @close="open = false"
    />
  </div>
</template>
<style scoped>
.emoji-icon-wrap {
  position: relative;
  flex: none;
}
.emoji-icon-button {
  position: relative;
  overflow: hidden;
  border-radius: 50%;
  border: 0;
  padding: 0;
  cursor: pointer;
}
.emoji-icon-edit {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  border-radius: inherit;
  color: var(--text);
  background: var(--input);
  opacity: 0;
  transition: opacity 120ms ease;
}
.emoji-icon-button:not(:disabled):hover .emoji-icon-edit,
.emoji-icon-button:focus-visible .emoji-icon-edit {
  opacity: 1;
}
.emoji-icon-button:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
