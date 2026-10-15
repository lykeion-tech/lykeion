import { computed, onMounted, onScopeDispose, ref } from 'vue';
import { readText, writeText } from '../storage';

export function clampPane(value: number, min: number, max: number, available = Infinity) {
  return Math.round(
    Math.max(
      Math.min(min, available),
      Math.min(max, available, Number.isFinite(value) ? value : min),
    ),
  );
}

export function usePaneResize(
  key: string,
  initial: number,
  min: number,
  max: number,
  direction = 1,
  available = () => window.innerWidth - 420,
) {
  const stored = Number(readText(key));
  const preferred = ref(
    Number.isFinite(stored) && stored >= min && stored <= max ? stored : initial,
  );
  const viewport = ref(typeof window === 'undefined' ? 1512 : window.innerWidth);
  const width = computed(() => {
    void viewport.value;
    return clampPane(preferred.value, min, max, available());
  });
  const minimum = computed(() => Math.min(min, maximum.value));
  const maximum = computed(() => {
    void viewport.value;
    return Math.min(max, available());
  });
  let cleanup: (() => void) | undefined;
  function save(value: number) {
    preferred.value = clampPane(value, min, max, available());
    writeText(key, String(preferred.value));
  }
  function keydown(event: KeyboardEvent) {
    const values: Record<string, number> = {
      Home: min,
      End: maximum.value,
      ArrowLeft: width.value - direction * (event.shiftKey ? 40 : 10),
      ArrowRight: width.value + direction * (event.shiftKey ? 40 : 10),
    };
    if (event.key in values) {
      event.preventDefault();
      save(values[event.key]!);
    }
  }
  function pointerdown(event: PointerEvent, click?: () => void) {
    if (event.button !== 0 || event.isPrimary === false) return;
    event.preventDefault();
    cleanup?.();
    const start = event.clientX,
      before = width.value;
    const previous = preferred.value;
    let dragged = false;
    const move = (e: PointerEvent) => {
      if (e.pointerId !== event.pointerId) return;
      if (Math.abs(e.clientX - start) > 3) dragged = true;
      if (dragged) save(before + (e.clientX - start) * direction);
    };
    const finish = (e: PointerEvent) => {
      if (e.pointerId !== event.pointerId) return;
      cleanup?.();
      if (e.type === 'pointercancel') {
        preferred.value = previous;
        writeText(key, String(previous));
      }
      if (!dragged && e.type === 'pointerup') click?.();
    };
    const cancel = () => {
      cleanup?.();
      preferred.value = previous;
      writeText(key, String(previous));
    };
    const old = document.body.style.userSelect;
    document.body.style.userSelect = 'none';
    cleanup = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', finish);
      window.removeEventListener('blur', cancel);
      document.body.style.userSelect = old;
      cleanup = undefined;
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', finish);
    window.addEventListener('pointercancel', finish);
    window.addEventListener('blur', cancel);
  }
  const resized = () => {
    viewport.value = window.innerWidth;
  };
  onMounted(() => window.addEventListener('resize', resized));
  onScopeDispose(() => {
    cleanup?.();
    window.removeEventListener('resize', resized);
  });
  return { width, minimum, maximum, keydown, pointerdown };
}
