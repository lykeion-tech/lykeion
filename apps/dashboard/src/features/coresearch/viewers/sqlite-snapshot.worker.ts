import { readSqliteSnapshot } from './sqlite-snapshot';
self.onmessage = async (event: MessageEvent<Blob>) => {
  try {
    const blob = event.data;
    if (!(blob instanceof Blob) || blob.size > 4 * 1024 * 1024) {
      self.postMessage(null);
      return;
    }
    self.postMessage(readSqliteSnapshot(new Uint8Array(await blob.arrayBuffer())) ?? null);
  } catch {
    self.postMessage(null);
  }
};
