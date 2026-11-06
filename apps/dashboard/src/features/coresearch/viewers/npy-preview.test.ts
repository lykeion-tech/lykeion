import { describe, it, expect } from 'vitest';
import { readNpy } from './npy-preview';
function npy(descr: string, shape: string, data: number[], fortran = false) {
  const header = `{'descr': '${descr}', 'fortran_order': ${fortran ? 'True' : 'False'}, 'shape': (${shape}), }\n`;
  const bytes = new TextEncoder().encode(header);
  const result = new Uint8Array(10 + bytes.length + data.length);
  result.set([0x93, 0x4e, 0x55, 0x4d, 0x50, 0x59, 1, 0, bytes.length & 255, bytes.length >> 8]);
  result.set(bytes, 10);
  result.set(data, 10 + bytes.length);
  return new Blob([result]);
}
describe('NumPy preview', () => {
  it('samples a valid array larger than 32 MiB without treating the whole file as table data', async () => {
    const count = 33 * 1024 * 1024;
    const header = new TextEncoder().encode(
      `{'descr': '|u1', 'fortran_order': False, 'shape': (${count},), }\n`,
    );
    const prefix = new Uint8Array([
      0x93,
      0x4e,
      0x55,
      0x4d,
      0x50,
      0x59,
      1,
      0,
      header.length & 255,
      header.length >> 8,
    ]);
    const result = await readNpy(new Blob([prefix, header, new Uint8Array(count)]));
    expect(result?.shape).toEqual([count]);
    expect(result?.values.length).toBeLessThanOrEqual(100);
    expect(result?.values.every((value) => value === '0')).toBe(true);
  });
  it('reads big-endian ints with shape and C-order values', async () => {
    expect(await readNpy(npy('>i2', '2, 2', [0, 1, 0, 2, 0, 3, 0, 4]))).toMatchObject({
      shape: [2, 2],
      values: ['1', '2', '3', '4'],
    });
  });
  it('displays Fortran-order data in logical row-major order', async () => {
    expect((await readNpy(npy('|u1', '2, 2', [1, 3, 2, 4], true)))?.values).toEqual([
      '1',
      '2',
      '3',
      '4',
    ]);
  });
  it('reads selected multidimensional slices in C and Fortran order', async () => {
    expect((await readNpy(npy('|u1', '2, 2, 2', [0, 1, 2, 3, 4, 5, 6, 7]), [1]))?.values).toEqual([
      '4',
      '5',
      '6',
      '7',
    ]);
    expect(
      (await readNpy(npy('|u1', '2, 2, 2', [0, 4, 2, 6, 1, 5, 3, 7], true), [1]))?.values,
    ).toEqual(['4', '5', '6', '7']);
    expect(await readNpy(npy('|u1', '2, 2, 2', [0, 1, 2, 3, 4, 5, 6, 7]), [2])).toBeUndefined();
  });
  it('bounds matrix samples and samples across both axes', async () => {
    const preview = await readNpy(
      npy(
        '|u1',
        '20, 20',
        Array.from({ length: 400 }, (_, i) => i % 256),
      ),
    );
    expect(preview?.values).toHaveLength(100);
    expect(preview?.sampleShape).toEqual([10, 10]);
    expect(preview?.sampleIndices).toEqual([
      [0, 2, 4, 6, 8, 10, 12, 14, 16, 19],
      [0, 2, 4, 6, 8, 10, 12, 14, 16, 19],
    ]);
    expect(preview?.values.at(-1)).toBe('143');
  });
  it('preserves exact 64-bit integer values', async () => {
    expect((await readNpy(npy('<u8', '1,', [1, 0, 0, 0, 0, 0, 0, 128])))?.values).toEqual([
      '9223372036854775809',
    ]);
  });
  it('rejects object, structured, truncated and unsafe shapes', async () => {
    expect(await readNpy(npy('|O8', '1,', [0, 0, 0, 0, 0, 0, 0, 0]))).toBeUndefined();
    expect(await readNpy(npy('<i4', '2,', [1]))).toBeUndefined();
    expect(await readNpy(npy('<i4', '9007199254740992,', []))).toBeUndefined();
    expect(await readNpy(new Blob(['not numpy']))).toBeUndefined();
  });
});
