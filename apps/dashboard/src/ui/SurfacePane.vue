<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import Icon from './Icon.vue';
import { usePaneResize } from '../lib/resize';
const props = withDefaults(
  defineProps<{
    open: boolean;
    active: string | null;
    focused: boolean;
    surfaces: { id: string; label: string; icon: string }[];
    widthKey?: string;
    containerWidth?: number;
    focusKey?: string;
    hideTitle?: boolean;
    defaultWidth?: number;
    restoreWidth?: boolean;
    maximizable?: boolean;
  }>(),
  { restoreWidth: true, maximizable: true },
);
const emit = defineEmits<{
  'update:open': [boolean];
  'update:active': [string | null];
  'update:focused': [boolean];
}>();
const resize = usePaneResize(
  props.widthKey ?? 'lykeion.surface-pane-width',
  props.defaultWidth ?? 364,
  240,
  640,
  -1,
  () => {
    const viewport = typeof window === 'undefined' ? 1500 : window.innerWidth;
    const available = props.containerWidth || viewport;
    return viewport <= 980 || (props.containerWidth !== undefined && available < 640)
      ? available
      : Math.max(160, available - 400);
  },
  props.restoreWidth !== false,
);
const title = computed(() => props.surfaces.find((s) => s.id === props.active)?.label ?? '');
const pane = ref<HTMLElement>();
const viewport = ref(typeof window === 'undefined' ? 1500 : window.innerWidth);
const narrow = computed(
  () =>
    viewport.value <= 980 ||
    (props.containerWidth !== undefined && props.containerWidth > 0 && props.containerWidth < 640),
);
let opener: HTMLElement | null = null;
const updateNarrow = () => {
  viewport.value = window.innerWidth;
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
      if (props.restoreWidth === false) resize.reset();
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
watch(
  () => props.focusKey,
  async () => {
    if (!props.open || !narrow.value) return;
    await nextTick();
    pane.value?.querySelector<HTMLElement>('button')?.focus();
  },
);
function keyboard(event: KeyboardEvent) {
  if (!narrow.value) return;
  if ((event.target as Element | null)?.closest?.('dialog[open]')) return;
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
function showChooser() {
  if (props.restoreWidth === false) resize.reset();
  emit('update:focused', false);
  emit('update:active', null);
}
const animations = new WeakMap<Element, Animation>();
function slide(element: Element, done: () => void, opening: boolean) {
  const el = element as HTMLElement;
  if (
    typeof el.animate !== 'function' ||
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  ) {
    done();
    return;
  }
  const width = `${el.getBoundingClientRect().width}px`;
  const frames = narrow.value
    ? [{ transform: 'translateX(100%)' }, { transform: 'translateX(0)' }]
    : [
        { flexGrow: '0', flexShrink: '0', flexBasis: '0px', width: '0px', overflow: 'hidden' },
        { flexGrow: '0', flexShrink: '0', flexBasis: width, width, overflow: 'hidden' },
      ];
  const animation = el.animate(opening ? frames : frames.reverse(), {
    duration: 220,
    easing: 'cubic-bezier(.22, 1, .36, 1)',
  });
  animations.set(el, animation);
  animation.onfinish = () => {
    animations.delete(el);
    done();
  };
}
function cancelSlide(element: Element) {
  animations.get(element)?.cancel();
  animations.delete(element);
}
</script>
<template>
  <button
    v-if="open"
    class="surface-backdrop"
    :class="{ 'is-overlay': narrow }"
    aria-label="Close pane overlay"
    @click="close"
  />
  <Transition
    :css="false"
    @enter="(el, done) => slide(el, done, true)"
    @leave="(el, done) => slide(el, done, false)"
    @enter-cancelled="cancelSlide"
    @leave-cancelled="cancelSlide"
  >
    <aside
      v-if="open"
      ref="pane"
      class="surface-pane"
      :role="narrow ? 'dialog' : undefined"
      :aria-modal="narrow ? true : undefined"
      @keydown="keyboard"
      :class="{
        'surface-focused': focused,
        'surface-overlay': narrow,
        'surface-resizing': resize.dragging.value,
      }"
      :style="{ '--surface-width': resize.width.value + 'px' }"
      aria-label="Right pane"
    >
      <div
        v-if="active && !focused"
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
          @click="showChooser"
        >
          <Icon name="back" />
        </button>
        <span v-if="!hideTitle" class="surface-title">{{ title }}</span>
        <span class="surface-spacer" />
        <button
          v-if="active && maximizable"
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
            <Icon :name="item.icon" /><span>{{ item.label }}</span>
          </button>
        </div>
        <slot v-else :active="active" />
      </div>
    </aside>
  </Transition>
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
.surface-backdrop.is-overlay {
  display: block;
  position: absolute;
  inset: 0;
  z-index: 19;
  border: 0;
  background: rgb(0 0 0 / 0.24);
}
.surface-pane.surface-overlay {
  position: absolute;
  z-index: 20;
  right: 0;
  top: 0;
  bottom: 0;
  width: min(var(--surface-width), 100%);
}
.surface-pane.surface-overlay.surface-focused {
  width: 100%;
}
.surface-overlay .surface-separator {
  display: none;
}
.surface-header {
  display: flex;
  align-items: center;
  height: 56px;
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
.surface-pane .surface-icon:hover,
.surface-pane .surface-choice:hover {
  background: color-mix(in srgb, var(--text) 12%, transparent);
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

.surface-body :deep(.task-rightpane-card .art-header) {
  padding: 24px 20px 16px;
}
.surface-body :deep(.experiment-files-header) {
  margin: 0;
  padding: 24px 20px 16px;
}
.surface-body :deep(.experiment-files-header + .tsb-files .tsb-tasks-empty) {
  padding: 2px 20px 16px;
}
.surface-body :deep(.compute-panel-body) {
  padding-inline: 0;
}
.surface-body :deep(.compute-panel-body .compute-empty) {
  padding: 2px 20px 16px;
}

.surface-chooser {
  margin: 0 auto auto;
  position: relative;
  top: calc(35% - 28px);
  width: calc(100% - 40px);
  max-width: 90%;
  padding: 18px 0;
}
.surface-pane .surface-chooser h2 {
  text-align: center;
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 12px;
}
.surface-pane .surface-choice {
  width: 100%;
  display: grid;
  grid-template-columns: 16px minmax(0, max-content);
  justify-content: center;
  align-items: center;
  gap: 8px;
  border: 0;
  border-radius: 5px;
  padding: 9px 4px;
  background: transparent;
  color: var(--text);
  text-align: left;
  font: inherit;
  cursor: pointer;
}
.surface-choice span {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
