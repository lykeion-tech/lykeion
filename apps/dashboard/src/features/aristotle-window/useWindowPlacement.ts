import { computed, onMounted, onUnmounted, ref, watch, type Ref } from 'vue';
import { readJSON, writeJSON } from '../../storage';

interface Placement {
  right: number;
  bottom: number;
  height: number;
}
const corner = { right: 20, bottom: 20, height: 0 };
export function useWindowPlacement(key: string, panel: Ref<HTMLElement | undefined>) {
  const stored = readJSON<Placement>(key, corner);
  const placement = ref({ ...corner });
  for (const field of ['right', 'bottom', 'height'] as const)
    if (Number.isFinite(stored?.[field])) placement.value[field] = Math.max(0, stored[field]);
  const width = ref(window.innerWidth),
    height = ref(window.innerHeight);
  const resize = () => {
    width.value = window.innerWidth;
    height.value = window.innerHeight;
  };
  const chromeHeight = ref(104);
  watch(
    panel,
    (element, _, cleanup) => {
      if (!element || typeof ResizeObserver === 'undefined') return;
      const measure = () => {
        const log = element.querySelector<HTMLElement>('.log');
        chromeHeight.value = element.offsetHeight - (log?.offsetHeight ?? 0);
      };
      const observer = new ResizeObserver(measure);
      observer.observe(element);
      measure();
      cleanup(() => observer.disconnect());
    },
    { flush: 'post' },
  );
  const maxHeight = computed(() => Math.max(0, height.value - chromeHeight.value - 16));
  const logHeight = computed(() => Math.min(maxHeight.value, placement.value.height));
  const right = computed(() =>
    Math.max(8, Math.min(placement.value.right, width.value - Math.min(416, width.value - 16) - 8)),
  );
  const bottom = computed(() =>
    Math.max(
      8,
      Math.min(placement.value.bottom, height.value - logHeight.value - chromeHeight.value - 8),
    ),
  );
  const style = computed(() => ({ right: right.value + 'px', bottom: bottom.value + 'px' }));
  const save = () => writeJSON(key, placement.value);
  const setHeight = (h: number) => {
    placement.value.height = Math.max(0, Math.min(maxHeight.value, h));
    save();
  };
  const reset = () => {
    placement.value.right = 20;
    placement.value.bottom = 20;
    save();
  };
  let stop: (() => void) | undefined;
  let dragged = false;
  function start(e: PointerEvent, mode: 'drag' | 'resize') {
    if (e.button !== 0) return;
    stop?.();
    const x = e.clientX,
      y = e.clientY,
      r = right.value,
      b = bottom.value,
      h = logHeight.value;
    const maxLog = Math.max(0, height.value - b - (panel.value?.offsetHeight ?? 100) + h - 8);
    dragged = false;
    const move = (ev: PointerEvent) => {
      if (ev.pointerId !== e.pointerId) return;
      const dx = ev.clientX - x,
        dy = ev.clientY - y;
      if (!dragged && Math.abs(dx) + Math.abs(dy) < 4) return;
      dragged = true;
      if (mode === 'drag') {
        placement.value.right = Math.max(
          8,
          Math.min(r - dx, width.value - (panel.value?.offsetWidth ?? 416) - 8),
        );
        placement.value.bottom = Math.max(
          8,
          Math.min(b - dy, height.value - (panel.value?.offsetHeight ?? 100) - 8),
        );
      } else placement.value.height = Math.max(0, Math.min(h - dy, maxLog, maxHeight.value));
    };
    stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop!);
      window.removeEventListener('pointercancel', stop!);
      save();
      stop = undefined;
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
    window.addEventListener('pointercancel', stop);
  }
  function consumeDrag() {
    const wasDragged = dragged;
    dragged = false;
    return wasDragged;
  }
  onMounted(() => window.addEventListener('resize', resize));
  onUnmounted(() => {
    stop?.();
    window.removeEventListener('resize', resize);
  });
  return { style, logHeight, setHeight, reset, start, consumeDrag };
}
