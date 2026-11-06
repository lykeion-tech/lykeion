import { readWorkbookArchive } from './xlsx-preview';
const scope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<{ blob: Blob }>) => void) | null;
  postMessage: (message: unknown) => void;
};
scope.onmessage = async ({ data }) => {
  const parts = await readWorkbookArchive(data.blob);
  scope.postMessage({ parts });
};
