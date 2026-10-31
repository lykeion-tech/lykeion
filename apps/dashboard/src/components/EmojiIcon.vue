<script setup lang="ts">
import { computed, ref } from 'vue';
import AvatarDisplay from './AvatarDisplay.vue';
import AvatarPicker from './AvatarPicker.vue';
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
  border: 0;
  padding: 0;
  cursor: pointer;
}
.emoji-icon-button:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
