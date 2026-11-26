<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import type { RuntimeModel } from '../../../api/types';
import CliIcon from '../CliIcon.vue';
const props = defineProps<{
  models: (RuntimeModel & { disabled?: boolean })[];
  modelValue: string;
  disabled?: boolean;
  cli?: string;
  clis?: string[];
  label?: string;
  openUp?: boolean;
  cliOnly?: boolean;
  modelOnly?: boolean;
  emptyLabel?: string;
  defaultLabel?: string;
}>();
const emit = defineEmits<{ 'update:modelValue': [string]; 'update:cli': [string] }>();
const cliLabel = (cli: string) => (cli === 'codex' ? 'Codex' : 'Claude Code');
const defaultLabelFor = (value: string) =>
  value === '' || value === 'default' ? props.defaultLabel : undefined;
const choices = computed(() =>
  props.models.length
    ? props.models.map((model) => ({ ...model, label: defaultLabelFor(model.value) ?? model.label }))
    : [{ value: '', label: props.defaultLabel ?? 'Default' }],
);
const open = ref(false),
  up = ref(false),
  upHeight = ref<number>(),
  root = ref<HTMLElement>(),
  trigger = ref<HTMLButtonElement>(),
  menuEl = ref<HTMLElement>();
// The menu drops below the pill; where the window has no room for it there,
// it opens upward as it does from L2's docked composer.
async function toggle() {
  up.value = !!props.openUp;
  const anchor = root.value?.getBoundingClientRect?.();
  const scrollFrame = root.value?.closest?.('.aristotle-scroll, dialog')?.getBoundingClientRect();
  upHeight.value = anchor
    ? Math.max(0, Math.min(320, anchor.top - Math.max(0, scrollFrame?.top ?? 0) - 8))
    : undefined;
  open.value = !open.value;
  await nextTick();
  const menu = menuEl.value?.getBoundingClientRect?.();
  const frame = root.value?.closest?.('.aristotle-chat')?.getBoundingClientRect();
  if (menu && menu.bottom > Math.min(window.innerHeight, frame?.bottom ?? window.innerHeight))
    up.value = true;
  if (open.value && !props.cli) {
    (menuEl.value?.querySelector<HTMLButtonElement>('[aria-selected="true"]:not(:disabled)') ??
      options()[0])?.focus();
  }
}
function options() {
  return Array.from(menuEl.value?.querySelectorAll<HTMLButtonElement>('[role="option"]:not(:disabled)') ?? []);
}
function navigateOptions(event: KeyboardEvent) {
  if (props.cli || !['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const items = options();
  const index = items.findIndex((item) => item === document.activeElement);
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1
    : (index + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
  items[next]?.focus();
}
const label = computed(
  () =>
    defaultLabelFor(props.modelValue) ??
    props.models.find((m) => m.value === props.modelValue)?.label ??
    (props.modelValue || 'Default'),
);
function outside(e: Event) {
  if (!root.value?.contains(e.target as Node)) open.value = false;
}
function key(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false;
}
function closeOnEscape(event: KeyboardEvent) {
  if (!open.value) return;
  event.preventDefault();
  event.stopPropagation();
  open.value = false;
  trigger.value?.focus();
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
  <span v-if="!cli && !models.length && (!disabled || !modelValue)" class="cli-pill cli-pill--empty"
    ><span class="cli-pill-name">{{ emptyLabel || 'Default' }}</span></span
  >
  <div v-else ref="root" class="cli-switcher" @keydown.esc="closeOnEscape">
    <button
      ref="trigger"
      type="button"
      class="cli-pill"
      :aria-haspopup="cli ? 'dialog' : 'listbox'"
      :aria-expanded="open"
      :aria-label="
        cli
          ? cliOnly
            ? `CLI: ${cliLabel(cli)}`
            : `CLI and model: ${cliLabel(cli)} · ${label}`
          : `${props.label || 'Model'}: ${label}`
      "
      :disabled="disabled"
      @click="toggle"
    >
      <CliIcon v-if="cli === 'claude' || cli === 'codex'" :cli="cli" />
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
    <div
      v-if="open && !disabled"
      ref="menuEl"
      class="cli-menu"
      :class="{ 'cli-menu--with-cli': cli }"
      :role="cli ? 'dialog' : 'listbox'"
      :aria-label="
        cli
          ? cliOnly
            ? 'Choose CLI'
            : 'CLI and model'
          : props.label
            ? `${props.label} options`
            : 'Models'
      "
      :style="
        up
          ? {
              top: 'auto',
              bottom: 'calc(100% + 8px)',
              maxHeight: upHeight === undefined ? undefined : `${upHeight}px`,
              boxSizing: 'border-box',
              boxShadow: '0 -18px 48px rgba(0, 0, 0, 0.6)',
            }
          : undefined
      "
      @keydown="navigateOptions"
    >
      <template v-if="cli && !modelOnly">
        <div class="cli-menu-head"><span class="cli-menu-eyebrow">CLI</span></div>
        <div class="cli-options" role="group" aria-label="CLI">
          <button
            v-for="item in clis"
            :key="item"
            type="button"
            class="cli-choice"
            :class="{ 'is-selected': item === cli }"
            :aria-label="cliLabel(item)"
            :aria-pressed="item === cli"
            @click="emit('update:cli', item)"
          >
            <CliIcon v-if="item === 'claude' || item === 'codex'" :cli="item" />
            {{ cliLabel(item) }}
          </button>
        </div>
        <div v-if="!cliOnly" class="cli-divider" />
      </template>
      <div v-if="!cliOnly" class="cli-menu-head">
        <span class="cli-menu-eyebrow">{{ props.label || 'Model' }}</span>
      </div>
      <button
        v-for="model in cliOnly ? [] : choices"
        :key="model.value"
        type="button"
        :role="cli ? undefined : 'option'"
        :aria-selected="cli ? undefined : model.value === modelValue"
        :aria-pressed="cli ? model.value === modelValue : undefined"
        :aria-label="model.description ? `${model.label} — ${model.description}` : model.label"
        :disabled="model.disabled"
        class="cli-row"
        :class="{ 'is-selected': model.value === modelValue }"
        @click="
          emit('update:modelValue', model.value);
          open = false;
          trigger?.focus();
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

<style>
.coresearch.agent-model-picker {
  height: auto;
  background: transparent;
  --surface-1: var(--surface);
  --surface-2: var(--input);
  --ink: var(--text);
  --ink-subtle: var(--muted);
  --ink-tertiary: var(--muted);
  --hairline-strong: var(--border);
  --type-body: 14px;
  --type-small: 13px;
  --type-meta: 12px;
}
.coresearch.agent-model-picker .cli-switcher,
.coresearch.agent-model-picker .cli-pill {
  width: 100%;
}
.coresearch.agent-model-picker .cli-pill {
  justify-content: space-between;
  background: var(--surface);
  color: var(--text);
  height: 37px;
  padding: 8px 10px;
  border-radius: 5px;
  font-weight: 400;
}
.coresearch.agent-model-picker .cli-menu {
  background: var(--surface);
  max-width: 100%;
}
.coresearch.agent-model-picker
  .cli-menu:is([aria-label='Stage options'], [aria-label='Harness options']) {
  width: 200px;
}
</style>
