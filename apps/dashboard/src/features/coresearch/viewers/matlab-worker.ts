import { parseMatlab } from './matlab-preview';
self.onmessage = async (event: MessageEvent<{ blob: Blob }>) => {
  try {
    const blob = event.data.blob;
    if (!(blob instanceof Blob) || blob.size > 32 * 1024 * 1024) throw Error();
    self.postMessage({ preview: await parseMatlab(new Uint8Array(await blob.arrayBuffer())) });
  } catch {
    self.postMessage({});
  }
};
