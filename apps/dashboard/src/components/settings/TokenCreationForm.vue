<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue';
import Icon from '../Icon.vue';
type TokenExpiry = '30' | '90' | '365' | 'none';
const props = withDefaults(
  defineProps<{
    available?: boolean;
    pending?: boolean;
    showExpiry?: boolean;
    unavailableReason?: string;
  }>(),
  {
    available: false,
    pending: false,
    showExpiry: true,
    unavailableReason: 'Token creation requires a connected identity API.',
  },
);
const emit = defineEmits<{
  create: [request: { name: string; expiresInDays: 30 | 90 | 365 | null }];
}>();
const name = ref(''),
  expiry = ref<TokenExpiry>('90'),
  expanded = ref(false),
  trigger = ref<HTMLButtonElement>(),
  menu = ref<HTMLElement>(),
  position = ref({ left: 0, top: 0 });
const options: { value: TokenExpiry; label: string }[] = [
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '1 year' },
  { value: 'none', label: 'No expiry' },
];
function close() {
  expanded.value = false;
  trigger.value?.focus();
}
async function toggle() {
  if (expanded.value) {
    close();
    return;
  }
  const rect = trigger.value!.getBoundingClientRect();
  const index = options.findIndex((x) => x.value === expiry.value);
  position.value = {
    left: Math.max(12, Math.min(window.innerWidth - 156, rect.left + 4)),
    top: Math.max(12, Math.min(window.innerHeight - 126, rect.top - index * 28 - 2)),
  };
  expanded.value = true;
  await nextTick();
  menu.value?.querySelector<HTMLButtonElement>('[aria-selected=true]')?.focus();
}
function select(value: TokenExpiry) {
  expiry.value = value;
  close();
}
function keys(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    close();
    return;
  }
  if (event.key === 'Tab') {
    expanded.value = false;
    return;
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const items = [...menu.value!.querySelectorAll<HTMLButtonElement>('button')],
    index = items.indexOf(document.activeElement as HTMLButtonElement);
  items[
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? items.length - 1
        : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
  ]?.focus();
}
function outside(event: PointerEvent) {
  if (!menu.value?.contains(event.target as Node) && !trigger.value?.contains(event.target as Node))
    expanded.value = false;
}
function submit() {
  if (!props.available || props.pending || !name.value.trim()) return;
  emit('create', {
    name: name.value.trim(),
    expiresInDays: expiry.value === 'none' ? null : (Number(expiry.value) as 30 | 90 | 365),
  });
}
function dismiss() {
  expanded.value = false;
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  window.addEventListener('resize', dismiss);
  window.addEventListener('scroll', dismiss, true);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', dismiss);
  window.removeEventListener('scroll', dismiss, true);
});
</script>
<template>
  <form
    class="token-creation-form"
    :aria-description="!available ? unavailableReason : undefined"
    @submit.prevent="submit"
  >
    <input
      v-model="name"
      aria-label="Token name"
      placeholder="Token name (e.g. My CLI)"
      maxlength="120"
      autocomplete="off"
    /><button
      v-if="showExpiry"
      ref="trigger"
      type="button"
      class="expiry-trigger"
      aria-label="Token expiry"
      aria-haspopup="listbox"
      :aria-expanded="expanded"
      @click="toggle"
      @keydown.down.prevent="toggle"
    >
      {{ options.find((x) => x.value === expiry)?.label }}<Icon name="down" :size="13" /></button
    ><button
      class="button primary"
      type="submit"
      :disabled="!available || pending || !name.trim()"
      :title="!available ? unavailableReason : undefined"
    >
      {{ pending ? 'Creating…' : 'Create' }}
    </button>
  </form>
  <Teleport to="body"
    ><div
      v-if="expanded"
      ref="menu"
      class="token-expiry-menu"
      role="listbox"
      aria-label="Token expiry"
      :style="{ left: position.left + 'px', top: position.top + 'px' }"
      @keydown="keys"
    >
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        role="option"
        :aria-selected="expiry === option.value"
        @click="select(option.value)"
      >
        {{ option.label
        }}<Icon v-if="expiry === option.value" class="push-right" name="check" :size="14" />
      </button></div
  ></Teleport>
</template>
<style scoped>
.token-creation-form {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  min-height: 66px;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.token-creation-form input,
.expiry-trigger {
  height: 32px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  padding: 0 10px;
  font-size: 14px;
  min-width: 0;
}
.token-creation-form input {
  flex: 1;
  width: 0;
}
.expiry-trigger {
  width: 96px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  white-space: nowrap;
}
.token-creation-form > .button {
  margin-left: 26px;
  height: 30px;
  padding: 0 10px;
}
.token-expiry-menu {
  position: fixed;
  z-index: 100;
  width: 144px;
  padding: 2px;
  background: var(--fill-menu);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 5px 18px #0001;
}
.token-expiry-menu > button {
  display: flex;
  align-items: center;
  width: 100%;
  height: 28px;
  text-align: left;
  font-size: 14px;
  padding: 0 6px;
  border-radius: 5px;
}
.token-expiry-menu > button:hover,
.token-expiry-menu > button:focus-visible {
  background: var(--selected);
  outline: none;
}
@media (max-width: 850px) {
  .token-creation-form {
    gap: 8px;
    padding: 12px;
  }
  .token-creation-form > .button {
    margin-left: 0;
  }
}
</style>
