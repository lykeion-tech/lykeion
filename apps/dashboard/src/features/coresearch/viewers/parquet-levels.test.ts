import { expect, it } from 'vitest';
import { optionalLevels } from './parquet-levels';
it('decodes bounded RLE and bit-packed definition runs', () => {
  expect(optionalLevels(new Uint8Array([4, 0, 0, 0, 4, 1, 4, 0]), 4)).toEqual({
    levels: [true, true, false, false],
    bytes: 8,
  });
  expect(optionalLevels(new Uint8Array([2, 0, 0, 0, 3, 5]), 3)).toEqual({
    levels: [true, false, true],
    bytes: 6,
  });
});
it('rejects invalid or excessive runs, truncated varints and trailing bytes', () => {
  for (const a of [
    [1, 0, 0, 0, 0],
    [2, 0, 0, 0, 10, 1],
    [1, 0, 0, 0, 128],
    [3, 0, 0, 0, 2, 1, 0],
    [2, 0, 0, 0, 2, 2],
    [2, 0, 0, 0, 5, 1],
  ])
    expect(optionalLevels(new Uint8Array(a), 3)).toBeUndefined();
});
