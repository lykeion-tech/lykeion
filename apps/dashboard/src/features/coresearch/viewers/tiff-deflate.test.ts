import { deflateSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { inflateTiffStrip } from './tiff-deflate';
it('requires exact zlib output and rejects corruption, truncation, and trailing garbage', async () => {
  const compressed = deflateSync(Buffer.from([1, 2, 3]));
  expect(await inflateTiffStrip(compressed, 3)).toEqual(new Uint8Array([1, 2, 3]));
  expect(await inflateTiffStrip(compressed, 2)).toBeUndefined();
  expect(await inflateTiffStrip(compressed, 4)).toBeUndefined();
  expect(await inflateTiffStrip(compressed.subarray(0, -1), 3)).toBeUndefined();
  expect(await inflateTiffStrip(new Uint8Array([...compressed, 1, 2]), 3)).toBeUndefined();
  const corrupt = new Uint8Array(compressed);
  corrupt[corrupt.length - 1] ^= 1;
  expect(await inflateTiffStrip(corrupt, 3)).toBeUndefined();
});
it('bounds compression bombs and declared output', async () => {
  const compressed = deflateSync(Buffer.alloc(8 * 1024 * 1024));
  expect(await inflateTiffStrip(compressed, 2)).toBeUndefined();
  expect(await inflateTiffStrip(compressed, 6 * 1024 * 1024 + 1)).toBeUndefined();
});
