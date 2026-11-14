import { expect, it } from 'vitest';
import { decodeTiffLzw } from './tiff-lzw';
function pack9(codes: number[]): Uint8Array {
  const data = new Uint8Array(Math.ceil((codes.length * 9) / 8));
  let bit = 0;
  for (const code of codes)
    for (let j = 8; j >= 0; j--, bit++) data[bit >> 3]! |= ((code >> j) & 1) << (7 - (bit & 7));
  return data;
}
it('decodes TIFF clear and special dictionary codes', () => {
  expect(decodeTiffLzw(pack9([256, 65, 258, 257]), 3)).toEqual(new Uint8Array([65, 65, 65]));
  expect(decodeTiffLzw(pack9([256, 65, 256, 66, 257]), 2)).toEqual(new Uint8Array([65, 66]));
});
it('rejects missing markers, invalid codes, truncation, trailing bytes and size mismatches', () => {
  expect(decodeTiffLzw(pack9([65, 257]), 1)).toBeUndefined();
  expect(decodeTiffLzw(pack9([256, 65, 259, 257]), 3)).toBeUndefined();
  expect(decodeTiffLzw(pack9([256, 258, 257]), 1)).toBeUndefined();
  expect(decodeTiffLzw(pack9([256, 65]), 1)).toBeUndefined();
  const good = pack9([256, 65, 257]);
  expect(decodeTiffLzw(new Uint8Array([...good, 0]), 1)).toBeUndefined();
  expect(decodeTiffLzw(good, 2)).toBeUndefined();
  expect(decodeTiffLzw(good, 0)).toBeUndefined();
  expect(decodeTiffLzw(good, 6 * 1024 * 1024 + 1)).toBeUndefined();
});
