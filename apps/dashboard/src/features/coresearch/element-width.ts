import { ref, watch, type Ref } from 'vue';

export function useElementWidth(element: Ref<HTMLElement | undefined>) {
  const width = ref(0);
  watch(
    element,
    (el, _previous, onCleanup) => {
      width.value = 0;
      if (!el || typeof ResizeObserver === 'undefined') return;
      const observer = new ResizeObserver((entries) => {
        width.value = entries[0]?.contentRect.width ?? 0;
      });
      observer.observe(el);
      onCleanup(() => observer.disconnect());
    },
    { flush: 'post' },
  );
  return width;
}
