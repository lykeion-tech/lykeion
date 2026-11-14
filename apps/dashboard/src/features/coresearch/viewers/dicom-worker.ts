import { parseDicom } from './dicom-preview';
const scope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<{ blob: Blob; frame: number }>) => void) | null;
  postMessage: (message: unknown, transfer?: Transferable[]) => void;
};
scope.onmessage = async ({ data }) => {
  try {
    if (data.blob.size > 32 * 1024 * 1024) {
      scope.postMessage({});
      return;
    }
    const result = parseDicom(new Uint8Array(await data.blob.arrayBuffer()), data.frame);
    if (!result) {
      scope.postMessage({});
      return;
    }
    scope.postMessage({ result }, [result.rgba.buffer]);
  } catch {
    scope.postMessage({});
  }
};
