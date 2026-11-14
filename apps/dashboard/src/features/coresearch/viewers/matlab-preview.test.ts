import { readFileSync } from 'node:fs';
import { deflateSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { parseMatlab } from './matlab-preview';
const fixture = (name: string) =>
  new Uint8Array(readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`));
it('samples level 4 numeric matrices in MATLAB column-major order', async () => {
  const r = await parseMatlab(fixture('matrix-v4.mat'));
  expect(r?.variables[0]).toMatchObject({ name: 'A', shape: [2, 2], values: ['1', '3', '2', '4'] });
});
it.each(['v5', 'v7'])('samples independently generated %s matrices with exact int64', async (v) => {
  const r = await parseMatlab(fixture(`matrix-${v}.mat`));
  expect(r?.variables[0]).toMatchObject({ name: 'A', shape: [2, 2], values: ['1', '3', '2', '4'] });
  expect(r?.variables[1]).toMatchObject({
    name: 'large',
    values: ['9007199254740993', '-9007199254740993'],
  });
});
it('rejects truncated files', async () => {
  expect(await parseMatlab(fixture('matrix-v5.mat').slice(0, -1))).toBeUndefined();
});
it('rejects oversized lengths, corrupted compressed data, and unsupported logical arrays', async () => {
  const f = fixture('matrix-v5.mat'),
    oversized = f.slice();
  new DataView(oversized.buffer).setUint32(132, 0x7fffffff, true);
  expect(await parseMatlab(oversized)).toBeUndefined();
  const compressed = fixture('matrix-v7.mat');
  compressed[140] ^= 1;
  expect(await parseMatlab(compressed)).toBeUndefined();
  const logical = f.slice();
  logical[146] |= 2;
  expect(await parseMatlab(logical)).toBeUndefined();
});
it('reads independently verified big-endian v4 matrices', async () => {
  expect((await parseMatlab(fixture('matrix-v4-big.mat')))?.variables[0]).toMatchObject({
    name: 'A',
    shape: [2, 2],
    values: ['1', '3', '2', '4'],
  });
});

it('rejects zlib streams expanding past the bounded page budget', async () => {
  const z = deflateSync(Buffer.alloc(8 * 1024 * 1024 + 1));
  const f = new Uint8Array(136 + z.length);
  f.set(fixture('matrix-v5.mat').subarray(0, 128));
  const view = new DataView(f.buffer);
  view.setUint32(128, 15, true);
  view.setUint32(132, z.length, true);
  f.set(z, 136);
  expect(await parseMatlab(f)).toBeUndefined();
});

it.each(['4', '5', '7', 'v4be'])(
  'samples MATLAB complex numeric arrays correctly in %s files',
  async (version) => {
    const result = await parseMatlab(fixture(`mat-complex-${version}.mat`));
    expect(result?.variables[0]).toMatchObject({
      name: 'z',
      shape: [2, 2],
      type: expect.stringContaining('complex'),
      values: ['(1, 2)', '(5, 6)', '(-3, -4)', '(7, -8)'],
    });
  },
);
it('preserves complex special values and rejects damaged or mismatched imaginary data', async () => {
  const p = await parseMatlab(fixture('mat-complex-special.mat'));
  expect(p?.variables[0]?.values).toEqual(['(NaN, -0)', '(Infinity, -Infinity)']);
  const f = fixture('mat-complex-5.mat');
  expect(await parseMatlab(f.slice(0, -1))).toBeUndefined();
  const mismatch = f.slice();
  new DataView(mismatch.buffer).setUint32(216, 7, true);
  expect(await parseMatlab(mismatch)).toBeUndefined();
  const invalidFlag = fixture('mat-complex-4.mat');
  new DataView(invalidFlag.buffer).setInt32(12, 2, true);
  expect(await parseMatlab(invalidFlag)).toBeUndefined();
});
