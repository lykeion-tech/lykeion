<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import Icon from '../Icon.vue';
import { Globe } from '@lucide/vue';
const props = withDefaults(
  defineProps<{ modelValue: string; automatic?: boolean; compact?: boolean }>(),
  { automatic: true, compact: false },
);
const emit = defineEmits<{
  'update:modelValue': [value: string];
  'update:automatic': [value: boolean];
}>();
const trigger = ref<HTMLButtonElement>();
const popup = ref<HTMLElement>();
const searchInput = ref<HTMLInputElement>();
const opened = ref(false);
const query = ref('');
const highlighted = ref(0);
const position = ref({ left: '0px', top: '0px' });
const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
const common = [
  'UTC',
  'America/Los_Angeles',
  'America/Denver',
  'America/Chicago',
  'America/New_York',
  'America/Sao_Paulo',
  'Europe/London',
  'Europe/Berlin',
  'Europe/Paris',
  'Europe/Moscow',
  'Africa/Cairo',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Bangkok',
  'Asia/Shanghai',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Australia/Sydney',
  'Pacific/Auckland',
];
const available =
  (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.(
    'timeZone',
  ) || common;
const zones = [
  { id: 'browser', zone: browserZone, label: browserZone + ' (browser)' },
  ...[...new Set([browserZone, ...common, ...available])].map((zone) => ({
    id: zone,
    zone,
    label: zone,
  })),
];
const currentZone = computed(() => (props.automatic ? browserZone : props.modelValue));
const currentLabel = computed(() =>
  props.compact
    ? currentZone.value.split('/').at(-1)!.replaceAll('_', ' ')
    : currentZone.value + (props.automatic ? ' (browser)' : ''),
);
const filtered = computed(() =>
  zones.filter((zone) =>
    zone.label
      .replaceAll('_', ' ')
      .toLowerCase()
      .includes(query.value.replaceAll('_', ' ').toLowerCase()),
  ),
);
const offsetCache = new Map<string, string>();
function offset(zone: string) {
  const cached = offsetCache.get(zone);
  if (cached) return cached;
  try {
    const value =
      new Intl.DateTimeFormat('en', { timeZone: zone, timeZoneName: 'shortOffset' })
        .formatToParts(new Date())
        .find((part) => part.type === 'timeZoneName')?.value || 'GMT';
    const result = value === 'GMT' ? 'UTC+0' : value.replace('GMT', 'UTC');
    offsetCache.set(zone, result);
    return result;
  } catch {
    return 'UTC';
  }
}
function place() {
  if (!trigger.value) return;
  const rect = trigger.value.getBoundingClientRect();
  const width = 320;
  const height = 334;
  const top =
    rect.bottom + height + 4 > window.innerHeight - 8 && rect.top > height
      ? rect.top - height - 4
      : rect.bottom + 4;
  position.value = {
    left: Math.min(Math.max(8, rect.right - width), window.innerWidth - width - 8) + 'px',
    top: Math.max(8, top) + 'px',
  };
}
async function toggle() {
  opened.value = !opened.value;
  if (opened.value) {
    query.value = '';
    highlighted.value = 0;
    place();
    await nextTick();
    searchInput.value?.focus();
  }
}
function close(restore = false) {
  opened.value = false;
  if (restore) trigger.value?.focus();
}
function choose(zone: (typeof zones)[number]) {
  emit('update:automatic', zone.id === 'browser');
  emit('update:modelValue', zone.zone);
  close(true);
}
function key(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    close(true);
    return;
  }
  if (event.key === 'Tab') {
    close();
    return;
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    highlighted.value =
      (highlighted.value + (event.key === 'ArrowDown' ? 1 : -1) + filtered.value.length) %
      Math.max(1, filtered.value.length);
    void nextTick(() =>
      popup.value
        ?.querySelector<HTMLElement>('[data-highlighted="true"]')
        ?.scrollIntoView({ block: 'nearest' }),
    );
  }
  if (event.key === 'Enter') {
    event.preventDefault();
    const zone = filtered.value[highlighted.value];
    if (zone) choose(zone);
  }
}
function outside(event: PointerEvent) {
  const target = event.target as Node;
  if (!popup.value?.contains(target) && !trigger.value?.contains(target)) close();
}
onMounted(() => {
  if (props.automatic && props.modelValue !== browserZone) emit('update:modelValue', browserZone);
  window.addEventListener('pointerdown', outside);
  window.addEventListener('resize', place);
  window.addEventListener('scroll', place, true);
});
onUnmounted(() => {
  window.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', place);
  window.removeEventListener('scroll', place, true);
});
</script>
<template>
  <button
    ref="trigger"
    :class="['timezone-trigger', { compact }]"
    type="button"
    :aria-label="compact ? 'Timezone' : 'Time zone'"
    aria-haspopup="listbox"
    :aria-expanded="opened"
    @click="toggle"
    @keydown.down.prevent="!opened && toggle()"
  >
    <Globe v-if="compact" :size="14" :stroke-width="1.5" /><span>{{ currentLabel }}</span
    ><small>{{ compact ? offset(currentZone).replace('UTC', 'GMT') : offset(currentZone) }}</small
    ><Icon :name="compact ? 'down' : 'chevrons'" :size="14" /></button
  ><Teleport to="body" :disabled="compact"
    ><div v-if="opened" ref="popup" class="timezone-popup" :style="position" @keydown="key">
      <div class="timezone-search">
        <Icon name="search" :size="16" /><input
          ref="searchInput"
          v-model="query"
          placeholder="Search time zones..."
          aria-label="Search time zones..."
          role="combobox"
          aria-controls="timezone-options"
          :aria-activedescendant="'timezone-option-' + highlighted"
          aria-expanded="true"
          @input="highlighted = 0"
        />
      </div>
      <div id="timezone-options" class="timezone-options" role="listbox" aria-label="Time zones">
        <button
          v-for="(zone, index) in filtered"
          :id="'timezone-option-' + index"
          :key="zone.id"
          role="option"
          type="button"
          :aria-selected="props.automatic ? zone.id === 'browser' : zone.id === modelValue"
          :data-highlighted="highlighted === index"
          @pointermove="highlighted = index"
          @click="choose(zone)"
        >
          <span>{{ zone.label }}</span
          ><small>{{ offset(zone.zone) }}</small
          ><Icon
            v-if="props.automatic ? zone.id === 'browser' : zone.id === modelValue"
            name="check"
            :size="14"
          /><i v-else />
        </button>
        <p v-if="!filtered.length">No matching time zones.</p>
      </div>
    </div></Teleport
  >
</template>
<style scoped>
.timezone-trigger {
  height: 28px;
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  padding: 3px 8px;
  min-width: 288px;
  max-width: 100%;
  font-size: 12px;
  line-height: 20px;
}
.timezone-trigger > span {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.timezone-trigger > small {
  margin: 0 0 0 auto !important;
  font-size: 12px;
  line-height: 20px;
  color: var(--muted);
  white-space: nowrap;
}
.timezone-trigger > .icon {
  color: var(--muted);
}
.timezone-popup {
  position: fixed;
  z-index: 100;
  width: 320px;
  max-width: calc(100vw - 16px);
  max-height: 334px;
  padding: 6px;
  background: var(--fill-menu);
  border: 1px solid color-mix(in srgb, var(--surface) 35%, transparent);
  border-radius: 8px;
  box-shadow: 0 6px 20px #0002;
  color: var(--text);
}
.timezone-search {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding: 0 8px;
  color: var(--muted);
}
.timezone-search input {
  width: 100%;
  min-width: 0;
  background: none;
  border: 0;
  outline: none;
  font-family: inherit;
  font-size: 14px;
  color: var(--text);
  padding: 0;
}
.timezone-options {
  max-height: 288px;
  overflow: auto;
  overscroll-behavior: contain;
}
.timezone-options button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 4px 6px;
  height: 28px;
  text-align: left;
  border-radius: 4px;
  background: none;
  border: 0;
  cursor: pointer;
  color: var(--text);
}
.timezone-options button[data-highlighted='true'] {
  background: color-mix(in srgb, var(--surface) 25%, transparent);
}
.timezone-options button > span {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.timezone-options button > small {
  font-size: 12px;
  color: var(--muted);
  margin-left: auto;
  white-space: nowrap;
}
.timezone-options button > i {
  width: 14px;
  height: 14px;
  flex: none;
}
.timezone-options > p {
  font-size: 13px;
  text-align: center;
  padding: 20px;
  color: var(--muted);
}
.timezone-trigger.compact {
  height: 32px;
  min-width: 0;
  width: 100%;
  font-size: 14px;
  border-color: #ffffff38;
  padding: 5px 10px;
  gap: 7px;
}
.timezone-trigger.compact > span {
  font-family: inherit;
  font-size: 14px;
}
.timezone-trigger.compact > svg:first-child {
  color: var(--muted);
  flex: none;
}
</style>
