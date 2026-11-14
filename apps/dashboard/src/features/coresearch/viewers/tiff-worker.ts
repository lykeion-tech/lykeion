import { parseTiff } from './tiff-preview';
const scope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<{ blob: Blob; page: number }>) => void) | null;
  postMessage: (message: unknown, transfer?: Transferable[]) => void;
};
scope.onmessage = async ({ data }) => {
  try {
    if (data.blob.size > 32 * 1024 * 1024) {
      scope.postMessage({});
      return;
    }
    const result = await parseTiff(new Uint8Array(await data.blob.arrayBuffer()), data.page);
    if (!result) {
      scope.postMessage({});
      return;
    }
    scope.postMessage({ result }, [result.rgba.buffer]);
  } catch {
    scope.postMessage({});
  }
};
