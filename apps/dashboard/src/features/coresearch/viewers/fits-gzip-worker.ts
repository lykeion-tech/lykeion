import { gunzipFits } from './fits-gzip';
self.onmessage = async (event: MessageEvent<{ blob: Blob }>) => {
  try {
    const blob = event.data.blob;
    if (!(blob instanceof Blob) || blob.size > 32 * 1024 * 1024) throw Error();
    self.postMessage({ blob: await gunzipFits(blob) });
  } catch {
    self.postMessage({});
  }
};
