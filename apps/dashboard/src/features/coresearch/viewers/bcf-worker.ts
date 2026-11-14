import { parseBcf } from './bcf-preview';
import { readBgzf } from './bgzf-preview';
self.onmessage = async (event: MessageEvent<{ blob: Blob }>) => {
  try {
    const blob = event.data.blob;
    if (!(blob instanceof Blob) || blob.size > 32 * 1024 * 1024) throw Error();
    const magic = new Uint8Array(await blob.slice(0, 3).arrayBuffer());
    const data =
      magic[0] === 66 && magic[1] === 67 && magic[2] === 70
        ? {
            bytes: new Uint8Array(await blob.slice(0, 4 * 1024 * 1024).arrayBuffer()),
            partial: blob.size > 4 * 1024 * 1024,
          }
        : await readBgzf(blob);
    self.postMessage({ preview: data && parseBcf(data.bytes, data.partial) });
  } catch {
    self.postMessage({});
  }
};
