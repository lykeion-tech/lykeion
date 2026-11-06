import init from './vendor/heic/safe-heic.mjs';
import asset from './vendor/heic/safe-heic.wasm?url&inline';
const LIMIT = 8 * 1024 * 1024;
const HASH = 'dbe9d6bce828b0b3c338f8fdc646af33f31d3d7a22ccf0c69bd129817264b9b9';
const prefix = 'data:application/wasm;base64,';
self.onmessage = async (event: MessageEvent<{ blob: Blob }>) => {
  try {
    const blob = event.data.blob;
    if (
      !(blob instanceof Blob) ||
      blob.size < 12 ||
      blob.size > LIMIT ||
      !asset.startsWith(prefix) ||
      asset.length > 2 * 1024 * 1024
    )
      throw Error();
    const raw = atob(asset.slice(prefix.length));
    const wasm = Uint8Array.from(raw, (c) => c.charCodeAt(0));
    const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', wasm)), (b) =>
      b.toString(16).padStart(2, '0'),
    ).join('');
    if (hash !== HASH) throw Error();
    const kit = await init({ wasmBinary: wasm, print: () => {}, printErr: () => {} });
    const data = new Uint8Array(await blob.arrayBuffer());
    const ptr = kit._malloc(data.length);
    if (!ptr) throw Error();
    try {
      kit.HEAPU8.set(data, ptr);
      if (!kit._preview_decode(ptr, data.length)) throw Error();
      const width = kit._preview_width(),
        height = kit._preview_height(),
        start = kit._preview_pixels();
      const size = width * height * 4;
      if (
        !Number.isInteger(width) ||
        !Number.isInteger(height) ||
        width < 1 ||
        height < 1 ||
        width > 4096 ||
        height > 4096 ||
        size > 16777216 ||
        !start ||
        start + size > kit.HEAPU8.length
      )
        throw Error();
      const pixels = kit.HEAPU8.slice(start, start + size);
      self.postMessage({ width, height, pixels }, { transfer: [pixels.buffer] });
    } finally {
      kit._preview_release();
      kit._free(ptr);
    }
  } catch {
    self.postMessage(null);
  }
};
