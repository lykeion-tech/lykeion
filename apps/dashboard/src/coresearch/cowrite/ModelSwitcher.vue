<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import type { RuntimeModel } from '../../api/types';
const props = defineProps<{ models: RuntimeModel[]; modelValue: string; disabled?: boolean }>();
const emit = defineEmits<{ 'update:modelValue': [string] }>();
const open = ref(false),
  root = ref<HTMLElement>();
const label = computed(
  () =>
    props.models.find((m) => m.value === props.modelValue)?.label ??
    (props.disabled && props.modelValue ? props.modelValue : 'Default'),
);
function outside(e: Event) {
  if (!root.value?.contains(e.target as Node)) open.value = false;
}
function key(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false;
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  document.addEventListener('keydown', key);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside);
  document.removeEventListener('keydown', key);
});
</script>
<template>
  <span v-if="!models.length && (!disabled || !modelValue)" class="cli-pill cli-pill--empty"
    ><span class="cli-pill-name">Default</span></span
  >
  <div v-else ref="root" class="cli-switcher">
    <button
      type="button"
      class="cli-pill"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-label="`Model: ${label}`"
      :disabled="disabled"
      @click="open = !open"
    >
      <span class="cli-pill-name">{{ label }}</span>
      <svg
        class="cli-chevron"
        :data-open="open"
        width="10"
        height="10"
        viewBox="0 0 10 10"
        fill="none"
      >
        <path
          d="M2.5 4 5 6.5 7.5 4"
          stroke="currentColor"
          stroke-width="1.3"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
    </button>
    <div v-if="open && !disabled" class="cli-menu" role="listbox" aria-label="Models">
      <div class="cli-menu-head"><span class="cli-menu-eyebrow">Model</span></div>
      <button
        v-for="model in models"
        :key="model.value"
        type="button"
        role="option"
        :aria-selected="model.value === modelValue"
        :aria-label="model.description ? `${model.label} — ${model.description}` : model.label"
        class="cli-row"
        :class="{ 'is-selected': model.value === modelValue }"
        @click="
          emit('update:modelValue', model.value);
          open = false;
        "
      >
        <span class="cli-row-main"
          ><span class="cli-row-name">{{ model.label }}</span
          ><span v-if="model.description" class="cli-row-desc">{{ model.description }}</span></span
        >
        <span class="cli-row-status"
          ><svg
            v-if="model.value === modelValue"
            width="14"
            height="14"
            viewBox="0 0 14 14"
            fill="none"
          >
            <path
              d="M2.5 7.5 5.5 10.5 11.5 3.5"
              stroke="var(--accent)"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
            /></svg
        ></span>
      </button>
    </div>
  </div>
</template>
