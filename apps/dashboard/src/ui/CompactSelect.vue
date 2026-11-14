<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, useId } from 'vue';
import Icon from './Icon.vue';
defineOptions({ inheritAttrs: false });
const props = withDefaults(
  defineProps<{
    modelValue: string;
    options: { value: string; label: string; disabled?: boolean }[];
    label: string;
    width?: number;
  }>(),
  { width: 148 },
);
const emit = defineEmits<{ 'update:modelValue': [string] }>();
const id = useId(),
  expanded = ref(false),
  trigger = ref<HTMLButtonElement>(),
  menu = ref<HTMLElement>(),
  target = ref<HTMLElement | string>('body'),
  position = ref({ left: 0, top: 0 });
const selected = computed(() => props.options.find((x) => x.value === props.modelValue));
let typed = '',
  typeTimer: ReturnType<typeof setTimeout> | undefined;
function close(restore = true) {
  expanded.value = false;
  if (restore) trigger.value?.focus();
}
async function open() {
  if (expanded.value) return;
  target.value = trigger.value?.closest('dialog') || 'body';
  const rect = trigger.value!.getBoundingClientRect(),
    index = Math.max(
      0,
      props.options.findIndex((x) => x.value === props.modelValue),
    ),
    height = props.options.length * 28 + 4;
  position.value = {
    left: Math.max(8, Math.min(window.innerWidth - props.width - 8, rect.left)),
    top: Math.max(8, Math.min(window.innerHeight - height - 8, rect.top - index * 28 - 2)),
  };
  expanded.value = true;
  await nextTick();
  const selected = menu.value?.querySelector<HTMLButtonElement>(
    '[aria-selected=true]:not(:disabled)',
  );
  (selected ?? menu.value?.querySelector<HTMLButtonElement>('button:not(:disabled)'))?.focus();
}
function select(value: string) {
  emit('update:modelValue', value);
  close();
}
function key(event: KeyboardEvent) {
  if (event.key === 'Escape' && expanded.value) {
    event.preventDefault();
    event.stopPropagation();
    close();
    return;
  }
  if (event.key === 'Tab') {
    close(false);
    return;
  }
  if (!expanded.value && ['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {
    event.preventDefault();
    void open();
    return;
  }
  if (!expanded.value) return;
  const items = [...menu.value!.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')];
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault();
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    items[
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
    ]?.focus();
  } else if (event.key.length === 1 && !event.metaKey && !event.ctrlKey) {
    typed += event.key.toLowerCase();
    clearTimeout(typeTimer);
    typeTimer = setTimeout(() => (typed = ''), 600);
    items.find((item) => item.textContent?.trim().toLowerCase().startsWith(typed))?.focus();
  }
}
function outside(event: PointerEvent) {
  if (!menu.value?.contains(event.target as Node) && !trigger.value?.contains(event.target as Node))
    close(false);
}
function dismiss(event: Event) {
  if (event.type === 'scroll' && menu.value?.contains(event.target as Node)) return;
  close(false);
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  window.addEventListener('resize', dismiss);
  window.addEventListener('scroll', dismiss, true);
});
onUnmounted(() => {
  clearTimeout(typeTimer);
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', dismiss);
  window.removeEventListener('scroll', dismiss, true);
});
</script>
<template>
  <button
    v-bind="$attrs"
    ref="trigger"
    type="button"
    role="combobox"
    class="compact-select"
    :aria-label="label"
    :aria-expanded="expanded"
    aria-haspopup="listbox"
    :aria-controls="id"
    :data-value="modelValue"
    @click="expanded ? close() : open()"
    @keydown="key"
  >
    <span>{{ selected?.label || modelValue }}</span
    ><Icon name="down" :size="13" /></button
  ><Teleport v-if="expanded" :to="target"
    ><div
      :id="id"
      ref="menu"
      role="listbox"
      :aria-label="label"
      class="compact-select-menu"
      :style="{ left: position.left + 'px', top: position.top + 'px', width: width + 'px' }"
      @keydown="key"
    >
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        role="option"
        :aria-selected="modelValue === option.value"
        :data-value="option.value"
        :disabled="option.disabled"
        @click="select(option.value)"
      >
        <span>{{ option.label }}</span
        ><Icon v-if="modelValue === option.value" class="push-right" name="check" :size="14" />
      </button></div
  ></Teleport>
</template>
<style scoped>
.compact-select {
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 0 10px;
  background: transparent;
  text-align: left;
  font-size: 14px;
}
.compact-select > span {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.compact-select-menu {
  position: fixed;
  z-index: 110;
  background: var(--fill-menu);
  border: 1px solid color-mix(in srgb, var(--surface) 35%, transparent);
  border-radius: 8px;
  box-shadow: 0 6px 20px #0001;
  padding: 1px;
  max-height: calc(100vh - 16px);
  overflow: auto;
}
.compact-select-menu > button {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 28px;
  width: 100%;
  padding: 0 6px;
  border-radius: 5px;
  font-size: 14px;
  text-align: left;
  white-space: nowrap;
}
.compact-select-menu > button > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.compact-select-menu > button > .icon {
  flex: none;
  margin-left: auto;
}
.compact-select-menu > button:disabled {
  opacity: 0.55;
}
.compact-select-menu > button:hover:not(:disabled),
.compact-select-menu > button:focus-visible {
  background: var(--selected);
  outline: none;
}
</style>
