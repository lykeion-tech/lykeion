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
