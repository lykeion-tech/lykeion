import { expect, it } from 'vitest';
import { dictionaryIndexes } from './parquet-dictionary';
it('decodes RLE and packed dictionary indexes', () => {
  expect(dictionaryIndexes(new Uint8Array([2, 4, 2, 3, 0x24, 0xe4]), 10, 4)).toEqual([
    2, 2, 0, 1, 2, 0, 0, 1, 2, 3,
  ]);
  expect(dictionaryIndexes(new Uint8Array([0, 6]), 3, 1)).toEqual([0, 0, 0]);
});
it('rejects truncated, excess, out-of-range and oversized runs', () => {
  for (const [a, n, d] of [
    [[2, 6, 4], 3, 4],
    [[2, 8, 1], 3, 4],
    [[2, 3], 3, 4],
    [[2, 3, 0xff, 0xff], 3, 2],
    [[33, 2, 0, 0, 0, 0, 0], 1, 1],
    [[0, 0], 1, 1],
    [[2, 0x80], 1, 4],
  ] as [number[], number, number][])
    expect(dictionaryIndexes(new Uint8Array(a), n, d)).toBeUndefined();
});
it('rejects nonzero padding in the final packed group', () => {
  expect(dictionaryIndexes(new Uint8Array([2, 3, 0xfc, 0xff]), 1, 4)).toBeUndefined();
});
