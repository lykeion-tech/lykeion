import { gzipSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { gunzipNifti } from './nifti-gzip';
it('decodes gzip input and rejects invalid compressed data', async () => {
  const result = await gunzipNifti(new Blob([gzipSync(Buffer.from('hello'))]));
  expect(await result?.text()).toBe('hello');
  expect(await gunzipNifti(new Blob(['bad gzip']))).toBeUndefined();
});
it('stops a compressed expansion exceeding the byte ceiling', async () => {
  const compressed = gzipSync(Buffer.alloc(32 * 1024 * 1024 + 1));
  expect(await gunzipNifti(new Blob([compressed]))).toBeUndefined();
});
