import { readOfficeArchive } from './office-preview';
const scope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<{ blob: Blob; format: 'docx' | 'pptx' }>) => void) | null;
  postMessage: (message: unknown) => void;
};
scope.onmessage = async ({ data }) => {
  const parts = await readOfficeArchive(data.blob, data.format);
  scope.postMessage({ parts });
};
