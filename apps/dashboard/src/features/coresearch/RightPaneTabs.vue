<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { RightPaneTab } from './pane-tabs';
const props = defineProps<{
  tabs: RightPaneTab[];
  activeId: string;
}>();
const emit = defineEmits<{
  select: [id: string];
  closeTab: [id: string];
  closePane: [];
}>();
const band = ref<HTMLElement>();
const overflow = ref('none');
let observer: ResizeObserver | undefined;
function measure() {
  const el = band.value;
  if (!el) return;
  const before = el.scrollLeft > 1,
    after = el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
  overflow.value = before ? (after ? 'both' : 'start') : after ? 'end' : 'none';
}
async function reveal() {
  await nextTick();
  const el = band.value;
  if (!el?.children) return;
  const tab = Array.from(el.children).find(
    (child) => (child as HTMLElement).dataset?.tabId === props.activeId,
  ) as HTMLElement | undefined;
  if (tab) {
    if (tab.offsetLeft < el.scrollLeft) el.scrollLeft = tab.offsetLeft;
    else if (tab.offsetLeft + tab.offsetWidth > el.scrollLeft + el.clientWidth)
      el.scrollLeft = tab.offsetLeft + tab.offsetWidth - el.clientWidth;
  }
  measure();
}
function keys(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const at = props.tabs.findIndex((t) => t.id === props.activeId);
  const index =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? props.tabs.length - 1
        : (at + (event.key === 'ArrowRight' ? 1 : props.tabs.length - 1)) % props.tabs.length;
  const tab = props.tabs[index];
  if (tab) {
    emit('select', tab.id);
    void nextTick(() =>
      band.value?.querySelector<HTMLElement>(`[data-tab-id="${tab.id}"] [role="tab"]`)?.focus(),
    );
  }
}
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && band.value) {
    observer = new ResizeObserver(measure);
    observer.observe(band.value);
  }
  void reveal();
});
onBeforeUnmount(() => observer?.disconnect());
watch(() => [props.activeId, props.tabs], reveal, { deep: true });
</script>
<template>
  <div class="rightpane-header">
    <div
      ref="band"
      class="tab-band rightpane-tabs"
      :data-overflow="overflow"
      data-testid="rightpane-tab-band"
      role="tablist"
      aria-label="Right pane"
      @scroll="measure"
      @keydown="keys"
    >
      <span
        v-for="tab in tabs"
        :key="tab.id"
        role="presentation"
        :data-tab-id="tab.id"
        class="rightpane-tabwrap"
      >
        <button
          type="button"
          role="tab"
          :aria-selected="activeId === tab.id"
          :tabindex="activeId === tab.id ? 0 : -1"
          class="rightpane-tab"
          :class="{ 'is-active': activeId === tab.id }"
          :title="tab.title"
          @click="emit('select', tab.id)"
        >
          {{ tab.label }}
        </button>
        <button
          v-if="tab.closable"
          type="button"
          class="rightpane-tabclose"
          :aria-label="tab.closeLabel ?? `Close ${tab.label}`"
          @click="emit('closeTab', tab.id)"
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
            <path
              d="M2.5 2.5l5 5M7.5 2.5l-5 5"
              stroke="currentColor"
              stroke-width="1.3"
              stroke-linecap="round"
            />
          </svg>
        </button>
      </span>
    </div>
    <div class="rightpane-actions">
      <button
        type="button"
        class="art-icon-btn rightpane-close"
        aria-label="Close panel"
        @click="emit('closePane')"
      >
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
          <path
            d="m4 4 8 8m0-8-8 8"
            stroke="currentColor"
            stroke-width="1.2"
            stroke-linecap="round"
          />
        </svg>
      </button>
    </div>
  </div>
</template>
