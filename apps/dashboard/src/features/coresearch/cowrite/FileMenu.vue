<script setup lang="ts">
import { ref, nextTick, onMounted, onBeforeUnmount } from 'vue';
const props = defineProps<{ label: string; adding?: boolean }>();
const emit = defineEmits<{ select: [string] }>();
const open = ref(false),
  trigger = ref<HTMLElement>(),
  panel = ref<HTMLElement>(),
  position = ref({ top: '0px', left: '0px' });
async function toggle() {
  open.value = !open.value;
  if (!open.value) return;
  const rect = trigger.value?.getBoundingClientRect?.();
  if (rect)
    position.value = {
      top: `${rect.bottom + 8}px`,
      left: `${Math.max(8, Math.min(window.innerWidth - (props.adding ? 224 : 176) - 8, rect.right - (props.adding ? 224 : 176)))}px`,
    };
  await nextTick();
  panel.value?.showPopover?.();
  const height = panel.value?.getBoundingClientRect?.().height;
  if (rect && height && rect.bottom + 8 + height > window.innerHeight - 8)
    position.value.top = `${Math.max(8, rect.top - height - 8)}px`;
  panel.value?.querySelector<HTMLElement>('[role=menuitem]')?.focus();
}
function select(value: string) {
  open.value = false;
  emit('select', value);
  trigger.value?.focus();
}
function outside(e: Event) {
  if (!panel.value?.contains(e.target as Node) && !trigger.value?.contains(e.target as Node))
    open.value = false;
}
function key(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    open.value = false;
    trigger.value?.focus();
  }
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    const buttons = Array.from(panel.value?.querySelectorAll<HTMLElement>('[role=menuitem]') ?? []);
    const at = buttons.indexOf(document.activeElement as HTMLElement);
    buttons[(at + (e.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length]?.focus();
  }
}
onMounted(() => window.addEventListener('pointerdown', outside));
onBeforeUnmount(() => window.removeEventListener('pointerdown', outside));
</script>
<template>
  <div class="relative">
    <button
      ref="trigger"
      type="button"
      :class="adding ? 'tsb-switch-action' : 'art-menu-trigger'"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-label="label"
      :title="label"
      @click="toggle"
    >
      <svg v-if="adding" width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path
          d="M12 5v14M5 12h14"
          stroke="currentColor"
          stroke-width="1.75"
          stroke-linecap="round"
        /></svg
      ><svg
        v-else
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.75"
        aria-hidden="true"
      >
        <circle cx="12" cy="5" r="1.2" />
        <circle cx="12" cy="12" r="1.2" />
        <circle cx="12" cy="19" r="1.2" />
      </svg>
    </button>
    <div
      v-if="open"
      ref="panel"
      popover="auto"
      role="menu"
      class="fixed rounded-[10px] border border-line-strong bg-surface p-1 shadow-[0_18px_48px_rgba(0,0,0,0.6)]"
      :class="adding ? 'w-56' : 'w-44'"
      :style="position"
      @keydown="key"
    >
      <template
        v-for="(item, i) in adding ? ['New file', 'Upload'] : ['Rename', 'Delete']"
        :key="item"
        ><div v-if="!adding && i === 1" class="mx-[5px] my-1 h-px bg-line" />
        <button
          type="button"
          role="menuitem"
          class="flex min-h-[28px] min-h-[26px] w-full items-center gap-2 rounded-md py-1 pl-2 pr-1.5 text-left hover:bg-surface-2"
          :class="{ 'text-danger': item === 'Delete' }"
          @click="select(item)"
        >
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.75"
            stroke-linecap="round"
            stroke-linejoin="round"
            class="shrink-0"
            :class="item === 'Delete' ? 'text-danger' : 'text-fg-subtle'"
            aria-hidden="true"
          >
            <path
              v-if="item === 'New file'"
              d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z M14 3v5h5"
            />
            <path
              v-else-if="item === 'Upload'"
              d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2 M12 15V3M8 7l4-4 4 4"
            />
            <path
              v-else-if="item === 'Rename'"
              d="M12 20h9 M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"
            />
            <path
              v-else
              d="M4 7h16 M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2 M6 7v12a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V7 M10 11v6M14 11v6"
            /></svg
          ><span class="flex min-w-0 flex-1 flex-col"
            ><span class="type-small" :class="item === 'Delete' ? 'text-danger' : 'text-fg'">{{
              item
            }}</span></span
          >
        </button></template
      >
    </div>
  </div>
</template>
