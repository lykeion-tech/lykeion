<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, onUpdated, ref, useId } from 'vue';
import Icon from './Icon.vue';

const props = withDefaults(
  defineProps<{ minWidth?: number; align?: 'start' | 'end'; fitContentWidth?: number }>(),
  { minWidth: 180 },
);

type Choice = { value: string; label: string; disabled: boolean; group: string };
const root = ref<HTMLElement>(),
  trigger = ref<HTMLButtonElement>(),
  popup = ref<HTMLElement>();
const input = ref<HTMLSelectElement>();
const visible = ref(true);
const choices = ref<Choice[]>([]),
  value = ref(''),
  disabled = ref(false),
  required = ref(false),
  accessibleName = ref('');
const open = ref(false),
  position = ref({ left: 0, top: 0, width: 200 });
const activeOption = ref('');
const target = ref<HTMLElement | string>('body');
const id = useId();
let observer: MutationObserver | undefined;
let typeahead = '',
  typeTimer: ReturnType<typeof setTimeout> | undefined;
const selected = computed(() => choices.value.find((choice) => choice.value === value.value));
function labelOf(select: HTMLSelectElement) {
  const label = select.getAttribute('aria-label');
  if (label) return label;
  const node = select.labels?.[0];
  if (!node) return 'Choose an option';
  return (
    Array.from(node.childNodes)
      .filter((child) => child !== root.value)
      .map((child) => child.textContent?.trim() ?? '')
      .filter(Boolean)
      .join(' ') || 'Choose an option'
  );
}
function sync() {
  const select = root.value?.querySelector<HTMLSelectElement>(':scope > select');
  if (!select) {
    input.value = undefined;
    return;
  }
  if (input.value !== select) {
    observer?.disconnect();
    input.value = select;
    select.setAttribute('tabindex', '-1');
    select.setAttribute('aria-hidden', 'true');
    observer = new MutationObserver(() => sync());
    observer.observe(select, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
      attributeFilter: ['disabled', 'label', 'value', 'selected', 'required', 'aria-label'],
    });
  }
  visible.value = getComputedStyle(select).display !== 'none';
  value.value = select.value;
  disabled.value = select.matches(':disabled');
  required.value = select.required;
  accessibleName.value = labelOf(select);
  const next = Array.from(select.options, (option) => ({
    value: option.value,
    label: option.label,
    disabled:
      option.disabled ||
      (option.parentElement instanceof HTMLOptGroupElement && option.parentElement.disabled),
    group: option.parentElement instanceof HTMLOptGroupElement ? option.parentElement.label : '',
  }));
  if (JSON.stringify(next) !== JSON.stringify(choices.value)) choices.value = next;
}
function close(restore = false) {
  open.value = false;
  activeOption.value = '';
  if (restore) trigger.value?.focus();
}
async function show() {
  sync();
  if (!input.value || disabled.value) return;
  if (open.value) {
    close(true);
    return;
  }
  positionPopup();
  target.value = trigger.value?.closest('dialog') || 'body';
  open.value = true;
  await nextTick();
  positionPopup();
  await nextTick();
  focusOption(
    popup.value?.querySelector<HTMLButtonElement>('[aria-selected="true"]:not(:disabled)') ??
      popup.value?.querySelector<HTMLButtonElement>('button:not(:disabled)'),
  );
}
function positionPopup() {
  if (!trigger.value) return;
  const bounds = trigger.value.getBoundingClientRect();
  const popupBounds = popup.value?.getBoundingClientRect();
  const width = props.fitContentWidth === undefined
    ? Math.min(Math.max(bounds.width, props.minWidth), 280, window.innerWidth - 16)
    : popupBounds?.width ?? bounds.width;
  const height = popupBounds?.height ?? Math.min(choices.value.length * 32 + 12, 300, window.innerHeight - 16);
  const top = bounds.bottom + height + 6 <= window.innerHeight - 8
    ? bounds.bottom + 6
    : bounds.top - height - 6;
  const left = props.align === 'end' ? bounds.right - width : bounds.left;
  position.value = {
    width,
    left: Math.max(8, Math.min(left, window.innerWidth - width - 8)),
    top: Math.max(8, Math.min(top, window.innerHeight - height - 8)),
  };
}
function focusOption(option?: HTMLButtonElement | null) {
  if (!option) return;
  activeOption.value = option.id;
  if (popup.value) {
    const top = option.offsetTop,
      bottom = top + option.offsetHeight;
    if (top < popup.value.scrollTop) popup.value.scrollTop = top;
    else if (bottom > popup.value.scrollTop + popup.value.clientHeight)
      popup.value.scrollTop = bottom - popup.value.clientHeight;
  }
}
function choose(choice: Choice) {
  if (choice.disabled || !input.value) return;
  input.value.value = choice.value;
  input.value.dispatchEvent(new Event('change', { bubbles: true }));
  sync();
  close(true);
}
function key(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) {
    event.preventDefault();
    event.stopPropagation();
    close(true);
    return;
  }
  if (event.key === 'Tab') {
    close();
    return;
  }
  if (!open.value && ['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {
    event.preventDefault();
    void show();
    return;
  }
  if (!open.value) return;
  const items = Array.from(
    popup.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [],
  );
  if (!items.length) return;
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault();
    event.stopPropagation();
    const index = items.findIndex((item) => item.id === activeOption.value);
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
    focusOption(items[next]);
  } else if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    items.find((item) => item.id === activeOption.value)?.click();
  } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey) {
    typeahead += event.key.toLowerCase();
    clearTimeout(typeTimer);
    typeTimer = setTimeout(() => {
      typeahead = '';
    }, 600);
    focusOption(items.find((item) => item.textContent?.trim().toLowerCase().startsWith(typeahead)));
  }
}
function outside(event: PointerEvent) {
  if (!root.value?.contains(event.target as Node) && !popup.value?.contains(event.target as Node))
    close();
}
async function dismiss(event: Event) {
  if (event.type === 'resize') sync();
  if (open.value && (event.type !== 'scroll' || !popup.value?.contains(event.target as Node))) {
    positionPopup();
    await nextTick();
    if (open.value) positionPopup();
  }
}
function nativeFocus(event: FocusEvent) {
  if (event.target === input.value) trigger.value?.focus();
}
function nativeClick(event: MouseEvent) {
  if (event.target !== input.value) return;
  event.preventDefault();
  event.stopPropagation();
  void show();
}
function invalid(event: Event) {
  event.preventDefault();
  trigger.value?.focus();
  void show();
}
onMounted(() => {
  if (typeof MutationObserver !== 'undefined') sync();
  document.addEventListener?.('pointerdown', outside);
  window.addEventListener?.('resize', dismiss);
  window.addEventListener?.('scroll', dismiss, true);
});
onUpdated(sync);
onUnmounted(() => {
  observer?.disconnect();
  clearTimeout(typeTimer);
  document.removeEventListener?.('pointerdown', outside);
  window.removeEventListener?.('resize', dismiss);
  window.removeEventListener?.('scroll', dismiss, true);
});
</script>
<template>
  <span
    ref="root"
    class="select-picker"
    @change="sync"
    @invalid.capture="invalid"
    @focusin.capture="nativeFocus"
    @click.capture="nativeClick"
  >
    <slot />
    <button
      v-if="input && visible"
      ref="trigger"
      type="button"
      class="select-picker-trigger"
      role="combobox"
      :aria-label="accessibleName"
      :aria-expanded="open"
      :aria-controls="id"
      :aria-activedescendant="open && activeOption ? activeOption : undefined"
      :aria-required="required || undefined"
      :data-value="value"
      aria-haspopup="listbox"
      :disabled="disabled"
      @click="show"
      @keydown="key"
    >
      <span>{{ selected?.label ?? value }}</span
      ><Icon name="down" :size="13" />
    </button>
    <Teleport v-if="open" :to="target">
      <div
        :id="id"
        ref="popup"
        class="select-picker-popup"
        role="listbox"
        :aria-label="accessibleName"
        :style="{
          left: position.left + 'px',
          top: position.top + 'px',
          width: fitContentWidth === undefined ? position.width + 'px' : 'max-content',
          maxWidth: fitContentWidth === undefined ? undefined : `min(${fitContentWidth}px, calc(100vw - 16px))`,
        }"
        @pointerdown.stop
        @click.stop
        @keydown="key"
      >
        <template v-for="(choice, index) in choices" :key="index">
          <div
            v-if="choice.group && (index === 0 || choices[index - 1]?.group !== choice.group)"
            class="select-picker-group"
          >
            {{ choice.group }}
          </div>
          <button
            type="button"
            role="option"
            :id="`${id}-option-${index}`"
            tabindex="-1"
            :class="{ 'is-active': activeOption === `${id}-option-${index}` }"
            :aria-selected="choice.value === value"
            :data-value="choice.value"
            :disabled="choice.disabled"
            @mousedown.prevent
            @click="choose(choice)"
          >
            <span>{{ choice.label }}</span
            ><Icon v-if="choice.value === value" name="check" :size="14" />
          </button>
        </template>
      </div>
    </Teleport>
  </span>
</template>
<style scoped>
.select-picker {
  display: inline-flex;
  position: relative;
  min-width: 0;
  max-width: 100%;
}
.select-picker :deep(> select) {
  position: absolute !important;
  width: 1px !important;
  height: 1px !important;
  min-width: 0 !important;
  padding: 0 !important;
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
}
.select-picker-trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  min-width: 0;
  min-height: 32px;
  padding: 4px 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text);
  background: var(--surface);
  font: inherit;
  text-align: left;
}
.select-picker-trigger > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.select-picker-trigger > .icon {
  flex: none;
}
.select-picker-popup {
  position: fixed;
  z-index: 200;
  max-height: min(300px, calc(100vh - 16px));
  overflow: auto;
  padding: 4px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--fill-menu);
  box-shadow: 0 6px 20px #0002;
  color: var(--text);
}
.select-picker-popup button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 28px;
  padding: 5px 8px;
  border-radius: 5px;
  font: inherit;
  font-size: 13px;
  text-align: left;
}
.select-picker-popup button span {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.select-picker-popup button:disabled {
  opacity: 0.5;
}
.select-picker-popup button:hover:not(:disabled),
.select-picker-popup button.is-active:not(:disabled),
.select-picker-popup button:focus-visible {
  background: var(--selected);
  outline: none;
}
.select-picker-group {
  padding: 6px 8px 3px;
  color: var(--muted);
  font-size: 11px;
}
</style>
