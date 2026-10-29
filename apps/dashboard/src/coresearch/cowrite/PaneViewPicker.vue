<script setup lang="ts">
import { computed, ref, nextTick, onMounted, onBeforeUnmount } from 'vue';
import PaneIcon from './PaneIcon.vue';
const props = defineProps<{
  label: string;
  views: { id: string; label: string }[];
  value: string;
}>();
const emit = defineEmits<{ change: [id: string] }>();
const open = ref(false),
  root = ref<HTMLElement>(),
  menu = ref<HTMLElement>(),
  style = ref({ top: '0px', left: '0px' });
const current = computed(() => props.views.find((v) => v.id === props.value) ?? props.views[0]!);
async function toggle() {
  open.value = !open.value;
  await nextTick();
  if (!open.value) return;
  menu.value?.showPopover?.();
  const a = root.value?.getBoundingClientRect?.(),
    b = menu.value?.getBoundingClientRect?.();
  if (a && b)
    style.value = {
      top: `${Math.max(8, Math.min(a.bottom + 6, window.innerHeight - b.height - 8))}px`,
      left: `${Math.max(8, a.right - b.width)}px`,
    };
  menu.value?.querySelector<HTMLElement>('[aria-checked=true]')?.focus();
}
function close(e: Event) {
  if (!root.value?.contains(e.target as Node)) open.value = false;
}
function keys(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    open.value = false;
    root.value?.querySelector('button')?.focus();
  }
  if (['ArrowDown', 'ArrowUp'].includes(e.key)) {
    e.preventDefault();
    const buttons = Array.from(menu.value?.querySelectorAll<HTMLElement>('button') ?? []);
    const at = buttons.indexOf(document.activeElement as HTMLElement);
    buttons[(at + (e.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length]?.focus();
  }
}
onMounted(() => window.addEventListener('pointerdown', close));
onBeforeUnmount(() => window.removeEventListener('pointerdown', close));
</script>
<template>
  <div ref="root" class="ms-view-picker">
    <button
      type="button"
      class="ms-view-picker-head"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-label="`${label}: ${current.label}`"
      @click="toggle"
    >
      <h2 class="ms-view-picker-title">{{ current.label }}</h2>
      <PaneIcon name="ChevronDown" />
    </button>
    <div
      v-if="open"
      ref="menu"
      popover="auto"
      :style="style"
      class="ms-view-picker-menu"
      role="menu"
      :aria-label="label"
      @keydown="keys"
    >
      <button
        v-for="view in views"
        :key="view.id"
        type="button"
        role="menuitemradio"
        :aria-checked="view.id === value"
        class="ms-view-picker-item"
        @click="
          emit('change', view.id);
          open = false;
        "
      >
        {{ view.label }}
      </button>
    </div>
  </div>
</template>
