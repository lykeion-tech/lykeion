const MAX_ACTIVE = 4;
let active = 0;
const waiting: Array<() => void> = [];
function drain() {
  while (active < MAX_ACTIVE && waiting.length) waiting.shift()?.();
}
export function queuePreview<T>(signal: AbortSignal, work: () => Promise<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    let started = false;
    const cancelled = () => {
      if (started) return;
      const index = waiting.indexOf(start);
      if (index >= 0) waiting.splice(index, 1);
      reject(new DOMException('Preview cancelled', 'AbortError'));
    };
    const start = () => {
      if (signal.aborted) {
        cancelled();
        return;
      }
      started = true;
      active++;
      void Promise.resolve()
        .then(work)
        .then(
          (value) =>
            signal.aborted
              ? reject(new DOMException('Preview cancelled', 'AbortError'))
              : resolve(value),
          reject,
        )
        .finally(() => {
          signal.removeEventListener('abort', cancelled);
          active--;
          drain();
        });
    };
    if (signal.aborted) {
      cancelled();
      return;
    }
    signal.addEventListener('abort', cancelled, { once: true });
    waiting.push(start);
    drain();
  });
}
