<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import type { RuntimeModel } from '../../../api/types';
import CliIcon from '../CliIcon.vue';
const props = defineProps<{
  models: RuntimeModel[];
  modelValue: string;
  disabled?: boolean;
  cli?: string;
  clis?: string[];
  label?: string;
  openUp?: boolean;
  cliOnly?: boolean;
}>();
const emit = defineEmits<{ 'update:modelValue': [string]; 'update:cli': [string] }>();
const cliLabel = (cli: string) => (cli === 'codex' ? 'Codex' : 'Claude Code');
const choices = computed(() =>
  props.models.length ? props.models : [{ value: '', label: 'Default' }],
);
const open = ref(false),
  up = ref(false),
  root = ref<HTMLElement>(),
  menuEl = ref<HTMLElement>();
// The menu drops below the pill; where the window has no room for it there,
// it opens upward as it does from L2's docked composer.
async function toggle() {
  up.value = !!props.openUp;
  open.value = !open.value;
  await nextTick();
  const menu = menuEl.value?.getBoundingClientRect?.();
  const frame = root.value?.closest?.('.aristotle-chat')?.getBoundingClientRect();
  if (menu && menu.bottom > Math.min(window.innerHeight, frame?.bottom ?? window.innerHeight))
    up.value = true;
}
const label = computed(
  () =>
    props.models.find((m) => m.value === props.modelValue)?.label ??
    (props.modelValue || 'Default'),
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
  <span v-if="!cli && !models.length && (!disabled || !modelValue)" class="cli-pill cli-pill--empty"
    ><span class="cli-pill-name">Default</span></span
  >
  <div v-else ref="root" class="cli-switcher">
    <button
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
      <span class="cli-pill-name">{{ cli ? cliLabel(cli) : label }}</span>
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
              boxShadow: '0 -18px 48px rgba(0, 0, 0, 0.6)',
            }
          : undefined
      "
    >
      <template v-if="cli">
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
            <span aria-hidden="true">{{ item === 'claude' ? '✳' : '◎' }}</span>
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
