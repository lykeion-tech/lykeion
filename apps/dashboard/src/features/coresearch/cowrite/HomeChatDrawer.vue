<script setup lang="ts">
import { ref, watch, nextTick } from 'vue';
const props = defineProps<{ projectId: string; open: boolean }>();
const emit = defineEmits<{ toggle: []; open: [] }>();
const clamp = (v: number) => Math.min(70, Math.max(25, v));
const stored = () => {
  try {
    const raw = window.localStorage.getItem(`lykeion:home-chat:${props.projectId}`);
    const v = raw === null ? 33 : Number.parseInt(raw, 10);
    return Number.isNaN(v) ? 33 : clamp(v);
  } catch {
    return 33;
  }
};
const share = ref(stored()),
  box = ref<HTMLElement>(),
  body = ref<HTMLElement>();
let drag: { id: number; y: number; share: number; height: number; raw: number } | undefined;
watch(
  () => props.projectId,
  () => {
    share.value = stored();
  },
  { flush: 'sync' },
);
function remember() {
  try {
    window.localStorage.setItem(`lykeion:home-chat:${props.projectId}`, String(share.value));
  } catch {
    // Storage may be unavailable; the drawer then keeps its share for this page only.
  }
}
function key(e: KeyboardEvent) {
  const next =
    e.key === 'ArrowUp'
      ? share.value + 4
      : e.key === 'ArrowDown'
        ? share.value - 4
        : e.key === 'Home'
          ? 25
          : e.key === 'End'
            ? 70
            : undefined;
  if (next !== undefined) {
    e.preventDefault();
    share.value = clamp(next);
    remember();
  }
}
function start(e: PointerEvent) {
  e.preventDefault();
  const el = e.currentTarget as HTMLElement;
  el.focus();
  drag = {
    id: e.pointerId,
    y: e.clientY,
    share: share.value,
    height: box.value?.parentElement?.clientHeight ?? 0,
    raw: share.value,
  };
  el.setPointerCapture(e.pointerId);
}
function move(e: PointerEvent) {
  if (!drag || drag.id !== e.pointerId || !drag.height) return;
  drag.raw = drag.share + ((drag.y - e.clientY) / drag.height) * 100;
  share.value = clamp(drag.raw);
}
function end(e: PointerEvent) {
  if (!drag || drag.id !== e.pointerId) return;
  const raw = drag.raw;
  drag = undefined;
  remember();
  if (raw < 15) emit('toggle');
  const el = e.currentTarget as HTMLElement;
  if (el.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId);
}
function cancel() {
  if (drag) {
    drag = undefined;
    remember();
  }
}
function fromField(e: Event) {
  if (!props.open && e.target instanceof HTMLTextAreaElement) emit('open');
}
function scrollBottom() {
  void nextTick(() => {
    if (body.value) body.value.scrollTop = body.value.scrollHeight;
  });
}
defineExpose({ scrollBottom });
</script>
<template>
  <div
    ref="box"
    class="home-chat"
    :class="{ 'home-chat--shut': !open }"
    data-testid="home-chat-drawer"
    :style="open ? { '--home-chat-share': `${share}%` } : undefined"
  >
    <div
      v-if="open"
      class="home-chat-seam"
      role="separator"
      aria-label="Resize the conversation"
      aria-orientation="horizontal"
      :aria-valuemin="25"
      :aria-valuemax="70"
      :aria-valuenow="share"
      tabindex="0"
      @keydown="key"
      @pointerdown="start"
      @pointermove="move"
      @pointerup="end"
      @pointercancel="cancel"
      @lostpointercapture="cancel"
    />
    <div v-if="open" class="home-chat-bar">
      <button
        type="button"
        class="home-chat-collapse"
        aria-label="Collapse the conversation"
        title="Collapse the conversation"
        @click="emit('toggle')"
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.75"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="m6 9 6 6 6-6" />
        </svg>
      </button>
    </div>
    <div v-if="open" ref="body" class="home-chat-body"><slot /></div>
    <div class="home-chat-composer" @pointerdown.capture="fromField" @focusin.capture="fromField">
      <slot name="composer" />
    </div>
  </div>
</template>
