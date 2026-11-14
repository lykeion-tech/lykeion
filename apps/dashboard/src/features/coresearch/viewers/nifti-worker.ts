import { gunzipNifti } from './nifti-gzip';
import { parseNifti } from './nifti-preview';
const scope = globalThis as unknown as {
  onmessage:
    | ((
        event: MessageEvent<{ blob: Blob; axis: number; index: number; compressed: boolean }>,
      ) => void)
    | null;
  postMessage: (message: unknown, transfer?: Transferable[]) => void;
};
scope.onmessage = async ({ data }) => {
  try {
    if (data.blob.size > 32 * 1024 * 1024) {
      scope.postMessage({});
      return;
    }
    const blob = data.compressed ? await gunzipNifti(data.blob) : data.blob;
    if (!blob) {
      scope.postMessage({});
      return;
    }
    const result = parseNifti(new Uint8Array(await blob.arrayBuffer()), data.axis, data.index);
    if (!result) {
      scope.postMessage({});
      return;
    }
    scope.postMessage({ result }, [result.rgba.buffer]);
  } catch {
    scope.postMessage({});
  }
};
