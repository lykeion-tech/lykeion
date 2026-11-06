import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { gunzipFits } from './fits-gzip';
import { readFits } from './fits-preview';
it('decompresses an independently generated Astropy FITS image', async () => {
  const bytes = readFileSync(
    'apps/dashboard/src/features/coresearch/viewers/fixtures/fits-astropy-image.fits.gz',
  );
  const decompressed = await gunzipFits(new Blob([bytes]));
  expect(decompressed).toBeDefined();
  expect(await readFits(decompressed!)).toMatchObject({
    shape: [2, 2],
    values: ['1', '2', '3', '4'],
  });
});
it('rejects corrupt, truncated, trailing, and excessive gzip output', async () => {
  const data = gzipSync(Buffer.from('hello'));
  expect(await gunzipFits(new Blob([data.subarray(0, -1)]))).toBeUndefined();
  expect(await gunzipFits(new Blob([data, new Uint8Array([1])]))).toBeUndefined();
  const corrupt = new Uint8Array(data);
  corrupt[corrupt.length - 1] ^= 1;
  expect(await gunzipFits(new Blob([corrupt]))).toBeUndefined();
  expect(
    await gunzipFits(new Blob([gzipSync(Buffer.alloc(32 * 1024 * 1024 + 1))])),
  ).toBeUndefined();
});
