<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { Pipette, Dices } from '@lucide/vue';
const props = withDefaults(
  defineProps<{ modelValue: string; label?: string; swatchOnly?: boolean }>(),
  { label: 'Color', swatchOnly: false },
);
const emit = defineEmits<{ 'update:modelValue': [value: string] }>();
const presets = [
  '#6b7280',
  '#ef4444',
  '#f97316',
  '#eab308',
  '#22c55e',
  '#14b8a6',
  '#3b82f6',
  '#6366f1',
  '#a855f7',
  '#ec4899',
];
const root = ref<HTMLElement>();
const trigger = ref<HTMLButtonElement>();
const sv = ref<HTMLElement>();
const opened = ref(false);
const above = ref(false);
const hue = ref(217);
const saturation = ref(76);
const brightness = ref(96);
const hex = ref(props.modelValue.toUpperCase());
const hexInvalid = ref(false);
const pickerError = ref('');
let emitted = '';
function rgbFromHex(value: string) {
  return [
    parseInt(value.slice(1, 3), 16),
    parseInt(value.slice(3, 5), 16),
    parseInt(value.slice(5, 7), 16),
  ].map((c) => c / 255);
}
function sync(value: string) {
  if (!/^#[0-9a-f]{6}$/i.test(value)) return;
  hex.value = value.toUpperCase();
  hexInvalid.value = false;
  if (value.toLowerCase() === emitted) return;
  const [r, g, b] = rgbFromHex(value) as [number, number, number];
  const max = Math.max(r, g, b),
    min = Math.min(r, g, b),
    delta = max - min;
  brightness.value = max * 100;
  saturation.value = max ? (delta / max) * 100 : 0;
  if (delta) {
    hue.value =
      ((max === r ? (g - b) / delta : max === g ? (b - r) / delta + 2 : (r - g) / delta + 4) * 60 +
        360) %
      360;
  }
}
watch(() => props.modelValue, sync, { immediate: true });
function currentHex() {
  const h = hue.value / 60,
    c = ((brightness.value / 100) * saturation.value) / 100,
    x = c * (1 - Math.abs((h % 2) - 1)),
    m = brightness.value / 100 - c;
  const rgb =
    h < 1
      ? [c, x, 0]
      : h < 2
        ? [x, c, 0]
        : h < 3
          ? [0, c, x]
          : h < 4
            ? [0, x, c]
            : h < 5
              ? [x, 0, c]
              : [c, 0, x];
  return (
    '#' +
    rgb
      .map((v) =>
        Math.round((v + m) * 255)
          .toString(16)
          .padStart(2, '0'),
      )
      .join('')
  );
}
function publish() {
  emitted = currentHex();
  hex.value = emitted.toUpperCase();
  hexInvalid.value = false;
  emit('update:modelValue', emitted);
}
function choose(value: string) {
  emitted = '';
  sync(value);
  emit('update:modelValue', value.toLowerCase());
}
function hexChange() {
  if (/^#[0-9a-f]{6}$/i.test(hex.value)) {
    choose(hex.value);
    hexInvalid.value = false;
  } else hexInvalid.value = true;
}
function pickPoint(event: PointerEvent) {
  const element = sv.value;
  if (!element) return;
  const bounds = element.getBoundingClientRect();
  saturation.value = Math.min(
    100,
    Math.max(0, ((event.clientX - bounds.left) / bounds.width) * 100),
  );
  brightness.value =
    100 - Math.min(100, Math.max(0, ((event.clientY - bounds.top) / bounds.height) * 100));
  publish();
}
function drag(event: PointerEvent) {
  event.preventDefault();
  sv.value?.focus();
  const element = event.currentTarget as HTMLElement;
  element.setPointerCapture(event.pointerId);
  pickPoint(event);
  const move = (e: PointerEvent) => pickPoint(e);
  const stop = () => {
    element.removeEventListener('pointermove', move);
    element.removeEventListener('pointerup', stop);
    element.removeEventListener('pointercancel', stop);
  };
  element.addEventListener('pointermove', move);
  element.addEventListener('pointerup', stop, { once: true });
  element.addEventListener('pointercancel', stop, { once: true });
}
function colorKey(event: KeyboardEvent) {
  const step = event.shiftKey ? 10 : 1;
  if (event.key === 'ArrowLeft') saturation.value = Math.max(0, saturation.value - step);
  else if (event.key === 'ArrowRight') saturation.value = Math.min(100, saturation.value + step);
  else if (event.key === 'ArrowDown') brightness.value = Math.max(0, brightness.value - step);
  else if (event.key === 'ArrowUp') brightness.value = Math.min(100, brightness.value + step);
  else if (event.key === 'Home') saturation.value = 0;
  else if (event.key === 'End') saturation.value = 100;
  else return;
  event.preventDefault();
  publish();
}
async function toggle() {
  opened.value = !opened.value;
  pickerError.value = '';
  if (opened.value) {
    const bounds = trigger.value!.getBoundingClientRect();
    above.value = bounds.bottom + 310 > window.innerHeight - 8 && bounds.top > 310;
    await nextTick();
    sv.value?.focus();
  }
}
function close(restore = false) {
  opened.value = false;
  if (restore) trigger.value?.focus();
}
function outside(event: PointerEvent) {
  if (!root.value?.contains(event.target as Node)) close();
}
function key(event: KeyboardEvent) {
  if (event.key === 'Escape' && opened.value) {
    event.preventDefault();
    event.stopPropagation();
    close(true);
  }
}
const marker = computed(() => ({
  left: saturation.value + '%',
  top: 100 - brightness.value + '%',
}));
type EyeDropperConstructor = new () => { open(): Promise<{ sRGBHex: string }> };
const eyedropper = (window as unknown as { EyeDropper?: EyeDropperConstructor }).EyeDropper;
async function pickScreen() {
  if (!eyedropper) return;
  try {
    const result = await new eyedropper().open();
    choose(result.sRGBHex);
  } catch (error) {
    if ((error as Error).name !== 'AbortError')
      pickerError.value = 'Screen color picking is unavailable.';
  }
}
function random() {
  const bytes = crypto.getRandomValues(new Uint8Array(3));
  choose('#' + [...bytes].map((n) => n.toString(16).padStart(2, '0')).join(''));
}
onMounted(() => window.addEventListener('pointerdown', outside));
onUnmounted(() => window.removeEventListener('pointerdown', outside));
</script>
<template>
  <div ref="root" class="settings-color" @keydown="key">
    <button
      ref="trigger"
      class="color-trigger"
      :class="{ 'swatch-only': swatchOnly }"
      type="button"
      :aria-label="label"
      aria-haspopup="dialog"
      :aria-expanded="opened"
      @click="toggle"
    >
      <i :style="{ background: modelValue }" /><code v-if="!swatchOnly">{{
        modelValue.toUpperCase()
      }}</code>
    </button>
    <div
      v-if="opened"
      class="color-surface"
      :class="{ above }"
      role="dialog"
      :aria-label="label + ' picker'"
    >
      <div
        ref="sv"
        class="saturation-value"
        tabindex="0"
        role="slider"
        aria-label="Saturation and brightness"
        :aria-valuenow="Math.round(brightness)"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuetext="`${Math.round(saturation)}% saturation, ${Math.round(brightness)}% brightness`"
        :style="{ backgroundColor: `hsl(${hue} 100% 50%)` }"
        @pointerdown="drag"
        @keydown="colorKey"
      >
        <i :style="marker" />
      </div>
      <div class="hue-row">
        <button
          type="button"
          class="eyedropper"
          aria-label="Pick a color from the screen"
          :disabled="!eyedropper"
          :title="
            eyedropper
              ? 'Pick a color from the screen'
              : 'Screen color picking is unavailable in this environment'
          "
          @click="pickScreen"
        >
          <Pipette :size="15" :stroke-width="1.7" /></button
        ><input
          v-model.number="hue"
          class="hue-slider"
          aria-label="Hue"
          type="range"
          min="0"
          max="359"
          step="1"
          @input="publish"
        />
      </div>
      <div class="hex-row">
        <i :style="{ background: modelValue }" /><input
          v-model="hex"
          aria-label="Hex color"
          maxlength="7"
          :aria-invalid="hexInvalid"
          spellcheck="false"
          @change="hexChange"
          @keydown.enter.prevent="hexChange"
        />
      </div>
      <p v-if="hexInvalid" class="color-error" role="alert">Enter a hex color such as #3B82F6.</p>
      <p v-if="pickerError" class="color-error" role="alert">{{ pickerError }}</p>
      <header>
        <span>Presets</span
        ><button type="button" @click="random">
          <Dices :size="14" :stroke-width="1.7" />Random
        </button>
      </header>
      <div class="color-preset-row">
        <button
          v-for="color in presets"
          :key="color"
          type="button"
          :aria-label="color"
          :aria-pressed="modelValue.toLowerCase() === color"
          :style="{ background: color }"
          @click="choose(color)"
        />
      </div>
    </div>
  </div>
</template>
<style scoped>
.settings-color {
  position: relative;
  width: max-content;
}
.color-trigger {
  display: flex;
  align-items: center;
  gap: 10px;
  width: max-content;
  border: 1px solid color-mix(in srgb, var(--surface) 40%, transparent);
  border-radius: 6px;
  padding: 6px 10px;
  cursor: pointer;
  height: 36px;
}
.color-trigger > i {
  height: 20px;
  width: 20px;
  border-radius: 50%;
  box-shadow: inset 0 0 0 1px #00000009;
}
.color-trigger > code {
  font-size: 12px;
  color: var(--muted);
}
.color-trigger.swatch-only {
  padding: 0;
  border: 0;
  width: 24px;
  height: 28px;
  justify-content: center;
}
.color-trigger.swatch-only > i {
  height: 22px;
  width: 22px;
}
.color-surface {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  z-index: 12;
  width: 290px;
  background: var(--modal);
  border: 1px solid color-mix(in srgb, var(--surface) 40%, transparent);
  padding: 12px;
  border-radius: 10px;
  box-shadow: 0 6px 16px #0002;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.color-surface.above {
  top: auto;
  bottom: calc(100% + 4px);
}
.saturation-value {
  height: 144px;
  position: relative;
  touch-action: none;
  border-radius: 6px;
  background-image:
    linear-gradient(to top, #000, transparent), linear-gradient(to right, #fff, transparent);
  cursor: crosshair;
  overflow: hidden;
}
.saturation-value > i {
  position: absolute;
  display: block;
  width: 14px;
  height: 14px;
  border: 2px solid white;
  box-shadow: 0 0 1px #0008;
  border-radius: 50%;
  transform: translate(-50%, -50%);
  pointer-events: none;
}
.hue-row {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 28px;
}
.eyedropper {
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  background: var(--surface);
  border-radius: 6px;
  border: 1px solid var(--border);
  padding: 0;
  flex: none;
  color: var(--text);
}
.hue-slider {
  appearance: none;
  height: 12px !important;
  border: 0 !important;
  border-radius: 10px !important;
  padding: 0 !important;
  flex: 1;
  min-width: 0;
  width: 100%;
  background: linear-gradient(to right, #f00, #ff0, #0f0, #0ff, #00f, #f0f, #f00) !important;
  cursor: pointer;
  box-shadow: inset 0 0 0 1px #00000007;
  margin: 0;
}
.hue-slider::-webkit-slider-thumb {
  appearance: none;
  width: 14px;
  height: 14px;
  border: 2px solid #fff;
  border-radius: 50%;
  background: transparent;
  box-shadow: 0 0 2px #0008;
}
.hue-slider::-moz-range-thumb {
  width: 10px;
  height: 10px;
  border: 2px solid #fff;
  border-radius: 50%;
  background: transparent;
  box-shadow: 0 0 2px #0008;
}
.hex-row {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 32px;
}
.hex-row > i {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  border: 1px solid #0000000c;
  flex: none;
}
.hex-row > input {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace !important;
  border: 1px solid color-mix(in srgb, var(--surface) 40%, transparent) !important;
  border-radius: 6px !important;
  min-width: 0;
  flex: 1;
  width: 100%;
  height: 30px !important;
  padding: 4px 10px !important;
  font-size: 13px !important;
  background: transparent !important;
}
.color-surface > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  color: var(--muted);
  height: 20px;
}
.color-surface > header > button {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  padding: 0;
}
.color-preset-row {
  display: flex;
  justify-content: space-between;
  gap: 6px;
}
.color-preset-row button {
  width: 20px;
  height: 20px;
  padding: 0;
  border-radius: 50%;
  flex: none;
}
.color-preset-row button[aria-pressed='true'] {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
.color-error {
  color: #bd4444;
  font-size: 11px;
  line-height: 16px;
  margin: 0;
}
</style>
