type Task = { run: () => Promise<void>; signal: AbortSignal; done: () => void };
const queue: Task[] = [];
let running = 0;
function pump() {
  while (running < 4 && queue.length) {
    const task = queue.shift()!;
    if (task.signal.aborted) {
      task.done();
      continue;
    }
    running++;
    void task
      .run()
      .catch(() => {})
      .finally(() => {
        running--;
        task.done();
        pump();
      });
  }
}
export function queueThumbnail(run: () => Promise<void>, signal: AbortSignal) {
  return new Promise<void>((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }
    const task: Task = { run, signal, done: resolve };
    queue.push(task);
    signal.addEventListener(
      'abort',
      () => {
        const index = queue.indexOf(task);
        if (index !== -1) {
          queue.splice(index, 1);
          resolve();
        }
      },
      { once: true },
    );
    pump();
  });
}
