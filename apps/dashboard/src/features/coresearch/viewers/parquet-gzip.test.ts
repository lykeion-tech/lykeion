import { gzipSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { gunzipPage } from './parquet-gzip';
it('decodes gzip and rejects malformed, trailing or mismatched output', async () => {
  const zipped = gzipSync(Buffer.from('hello'));
  expect(await gunzipPage(zipped, 5)).toEqual(new Uint8Array(Buffer.from('hello')));
  expect(await gunzipPage(zipped, 4)).toBeUndefined();
  expect(await gunzipPage(zipped, 6)).toBeUndefined();
  expect(await gunzipPage(new Uint8Array([...zipped, 1, 2]), 5)).toBeUndefined();
  expect(await gunzipPage(zipped.subarray(0, -1), 5)).toBeUndefined();
});
it('rejects decompression beyond the declared ceiling', async () => {
  const zipped = gzipSync(Buffer.alloc(2 * 1024 * 1024));
  expect(await gunzipPage(zipped, 1)).toBeUndefined();
  expect(await gunzipPage(zipped, 8 * 1024 * 1024 + 1)).toBeUndefined();
});
