import { readTargetedHdf } from './targeted-reader';
self.onmessage = async (e: MessageEvent<{ blob: Blob; mode: 'netcdf' | 'matlab' }>) => {
  try {
    const { blob, mode } = e.data;
    if (
      !(blob instanceof Blob) ||
      blob.size > 32 * 1024 * 1024 ||
      !['netcdf', 'matlab'].includes(mode)
    )
      throw Error();
    self.postMessage({
      preview: await readTargetedHdf(new Uint8Array(await blob.arrayBuffer()), mode),
    });
  } catch {
    self.postMessage({});
  }
};
