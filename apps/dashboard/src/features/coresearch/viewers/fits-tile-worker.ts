import { inflateFitsTiles } from './fits-tile';
self.onmessage = async (e: MessageEvent<{ blob: Blob }>) => {
  try {
    const blob = e.data.blob;
    if (!(blob instanceof Blob) || blob.size > 32 * 1024 * 1024) throw Error();
    self.postMessage({ blob: await inflateFitsTiles(blob) });
  } catch {
    self.postMessage({});
  }
};
