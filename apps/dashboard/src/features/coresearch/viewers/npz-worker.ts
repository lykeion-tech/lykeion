import { listNpz, extractNpz } from './npz-preview';
const scope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<{ blob: Blob; index: number }>) => void) | null;
  postMessage: (message: unknown) => void;
};
scope.onmessage = async ({ data }) => {
  const entries = await listNpz(data.blob);
  if (!entries || !Number.isInteger(data.index) || data.index < 0 || data.index >= entries.length) {
    scope.postMessage({ error: true });
    return;
  }
  const blob = await extractNpz(data.blob, entries[data.index]!);
  scope.postMessage({ names: entries.map((entry) => entry.name), blob });
};
