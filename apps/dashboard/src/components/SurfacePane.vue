<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import Icon from './Icon.vue';
import { usePaneResize } from '../lib/resize';
const props = defineProps<{
  open: boolean;
  active: string | null;
  focused: boolean;
  surfaces: { id: string; label: string; icon: string }[];
  widthKey?: string;
}>();
const emit = defineEmits<{
  'update:open': [boolean];
  'update:active': [string | null];
  'update:focused': [boolean];
}>();
const resize = usePaneResize(
  props.widthKey ?? 'lykeion.surface-pane-width',
  364,
  240,
  640,
  -1,
  () =>
    typeof window === 'undefined'
      ? 640
      : window.innerWidth <= 980
        ? window.innerWidth
        : Math.max(160, window.innerWidth - 420),
);
const title = computed(() => props.surfaces.find((s) => s.id === props.active)?.label ?? '');
const pane = ref<HTMLElement>();
const narrow = ref(typeof window !== 'undefined' && window.innerWidth <= 980);
let opener: HTMLElement | null = null;
const updateNarrow = () => {
  narrow.value = window.innerWidth <= 980;
};
onMounted(() => {
  window.addEventListener('resize', updateNarrow);
});
onUnmounted(() => {
  window.removeEventListener('resize', updateNarrow);
});
watch(
  () => props.open,
  async (open) => {
    if (open) {
      opener =
        typeof document !== 'undefined' ? (document.activeElement as HTMLElement | null) : null;
      await nextTick();
      if (narrow.value) pane.value?.querySelector<HTMLElement>('button')?.focus();
    } else {
      opener?.focus?.();
      opener = null;
    }
  },
  { immediate: true },
);
watch(narrow, async (isNarrow) => {
  if (!isNarrow || !props.open) return;
  const current =
    typeof document === 'undefined' ? null : (document.activeElement as HTMLElement | null);
  if (current && !pane.value?.contains(current)) opener = current;
  await nextTick();
  if (current && pane.value?.contains(current)) return;
  pane.value?.querySelector<HTMLElement>('button')?.focus();
});
watch(
  () => props.active,
  async () => {
    if (!props.open) return;
    await nextTick();
    pane.value?.querySelector<HTMLElement>('button')?.focus();
  },
);
function keyboard(event: KeyboardEvent) {
  if (!narrow.value) return;
  if (event.key === 'Escape') {
    event.preventDefault();
    close();
    return;
  }
  if (event.key !== 'Tab' || !pane.value) return;
  const controls = Array.from(
    pane.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]',
    ),
  ).filter((el) => el.getClientRects().length > 0);
  if (!controls.length) return;
  const first = controls[0]!,
    last = controls[controls.length - 1]!;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}
function close() {
  emit('update:open', false);
}
</script>
<template>
  <button v-if="open" class="surface-backdrop" aria-label="Close pane overlay" @click="close" />
  <aside
    v-if="open"
    ref="pane"
    class="surface-pane"
    :role="narrow ? 'dialog' : undefined"
    :aria-modal="narrow ? true : undefined"
    @keydown="keyboard"
    :class="{ 'surface-focused': focused }"
    :style="{ '--surface-width': resize.width.value + 'px' }"
    aria-label="Right pane"
  >
    <div
      v-if="!focused"
      class="surface-separator"
      role="separator"
      tabindex="0"
      aria-label="Resize right pane"
      aria-orientation="vertical"
      :aria-valuenow="resize.width.value"
      :aria-valuemin="resize.minimum.value"
      :aria-valuemax="resize.maximum.value"
      @pointerdown="resize.pointerdown($event)"
      @keydown="resize.keydown"
    />
    <header class="surface-header">
      <button
        v-if="active"
        class="surface-icon"
        aria-label="Back to surfaces"
        @click="emit('update:active', null)"
      >
        <Icon name="back" />
      </button>
      <span class="surface-title">{{ title }}</span>
      <span class="surface-spacer" />
      <button
        class="surface-icon"
        :aria-label="focused ? 'Restore pane size' : 'Maximize pane'"
        :aria-pressed="focused"
        @click="emit('update:focused', !focused)"
      >
        <Icon name="expand" />
      </button>
      <button class="surface-icon" aria-label="Close pane" @click="close">
        <Icon name="panel" />
      </button>
    </header>
    <div class="surface-body">
      <div v-if="!active" class="surface-chooser">
        <h2>Open a surface</h2>
        <button
          v-for="item in surfaces"
          :key="item.id"
          class="surface-choice"
          @click="emit('update:active', item.id)"
        >
          <Icon :name="item.icon" />{{ item.label }}
        </button>
      </div>
      <slot v-else :active="active" />
    </div>
  </aside>
</template>
<style scoped>
.surface-backdrop {
  display: none;
}
.surface-pane {
  position: relative;
  flex: 0 0 var(--surface-width);
  width: var(--surface-width);
  border-left: 1px solid var(--border, #ddd);
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  background: var(--surface, #fff);
  color: var(--text, inherit);
}
.surface-focused {
  flex: 1;
  width: auto;
}
.surface-separator {
  position: absolute;
  inset: 0 auto 0 -4px;
  width: 8px;
  z-index: 2;
  cursor: col-resize;
}
.surface-separator:hover,
.surface-separator:focus-visible {
  background: var(--border, #ddd);
}
@media (max-width: 980px) {
  .surface-backdrop {
    display: block;
    position: absolute;
    inset: 0;
    z-index: 19;
    border: 0;
    background: rgb(0 0 0 / 0.24);
  }
  .surface-pane {
    position: absolute;
    z-index: 20;
    right: 0;
    top: 0;
    bottom: 0;
    width: min(var(--surface-width), 100%);
  }
  .surface-focused {
    width: 100%;
  }
  .surface-separator {
    display: none;
  }
}
.surface-header {
  display: flex;
  align-items: center;
  height: 52px;
  flex: none;
  padding: 0 10px;
  border-bottom: 1px solid var(--border, #ddd);
  gap: 4px;
}
.surface-title {
  color: var(--muted);
  font-size: 12px;
}
.surface-spacer {
  flex: 1;
}
.surface-icon {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border: 0;
  background: transparent;
  color: inherit;
  border-radius: 5px;
  cursor: pointer;
}
.surface-icon:hover,
.surface-choice:hover {
  background: var(--surface-2, #eee);
}
.surface-icon:focus-visible,
.surface-choice:focus-visible {
  outline: 2px solid currentColor;
  outline-offset: 1px;
}
.surface-body {
  min-height: 0;
  flex: 1;
  overflow: auto;
  display: flex;
  flex-direction: column;
}
.surface-chooser {
  margin: auto;
  width: min(270px, 90%);
  padding: 18px 0;
}
.surface-chooser h2 {
  text-align: center;
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 12px;
}
.surface-choice {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 12px;
  border: 0;
  border-radius: 5px;
  padding: 9px;
  background: transparent;
  color: inherit;
  text-align: left;
  font: inherit;
  cursor: pointer;
}
</style>
