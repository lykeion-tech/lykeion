import { describe, expect, it } from 'vitest';
import { listNpz, extractNpz } from './npz-preview';
import { readNpy } from './npy-preview';

const compressed =
  'UEsDBC0AAAAIAAAAIQCdMoU6//////////8FABQAeC5ucHkBABAAhAAAAAAAAABIAAAAAAAAAJvsF+obEMnIUMZQrZ6SWpxcpG6loG6TaaSuo6Cell9UUpSYF59flJIKEndLzClOBYoXZyQWpAL5GkY6mjoKtQoUAC5GBiYGAFBLAQItAy0AAAAIAAAAIQCdMoU6SAAAAIQAAAAFAAAAAAAAAAAAAACAAQAAAAB4Lm5weVBLBQYAAAAAAQABADMAAAB/AAAAAAA=';
const stored =
  'UEsDBC0AAAAAAAAAIQCdMoU6//////////8FABQAeC5ucHkBABAAhAAAAAAAAACEAAAAAAAAAJNOVU1QWQEAdgB7J2Rlc2NyJzogJzxpMicsICdmb3J0cmFuX29yZGVyJzogRmFsc2UsICdzaGFwZSc6ICgyLCksIH0gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAKAQACAFBLAQItAy0AAAAAAAAAIQCdMoU6hAAAAIQAAAAFAAAAAAAAAAAAAACAAQAAAAB4Lm5weVBLBQYAAAAAAQABADMAAAC7AAAAAAA=';
const bytes = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
describe('NPZ archives', () => {
  it.each([compressed, stored])(
    'opens a real NumPy archive with local ZIP64 sizes',
    async (fixture) => {
      const blob = new Blob([bytes(fixture)]);
      const entries = await listNpz(blob);
      expect(entries?.map((entry) => entry.name)).toEqual(['x.npy']);
      const extracted = await extractNpz(blob, entries![0]!);
      expect((await readNpy(extracted!))?.values).toEqual(['1', '2']);
    },
  );
  it('rejects altered local headers and unsupported compression', async () => {
    const a = bytes(compressed);
    a[0] = 0;
    expect(await listNpz(new Blob([a]))).toBeUndefined();
    const b = bytes(compressed);
    b[8] = 99;
    expect(await listNpz(new Blob([b]))).toBeUndefined();
  });
  it('rejects corrupted content via ZIP CRC and size checking', async () => {
    const a = bytes(stored);
    a[60] ^= 1;
    const blob = new Blob([a]);
    const entries = await listNpz(blob);
    expect(entries).toBeDefined();
    expect(await extractNpz(blob, entries![0]!)).toBeUndefined();
  });
  it('rejects malformed/truncated archives', async () => {
    expect(await listNpz(new Blob([bytes(compressed).slice(0, 40)]))).toBeUndefined();
  });
  it('rejects encrypted, out-of-range and unsupported ZIP64 directory entries', async () => {
    for (const mutate of [
      (v: DataView, central: number) => v.setUint16(central + 8, 1, true),
      (v: DataView, central: number) => v.setUint32(central + 42, 0xfffffff0, true),
      (v: DataView, central: number) => v.setUint32(central + 24, 0xffffffff, true),
    ]) {
      const a = bytes(compressed);
      const v = new DataView(a.buffer);
      mutate(v, v.getUint32(a.length - 22 + 16, true));
      expect(await listNpz(new Blob([a]))).toBeUndefined();
    }
  });
  it('stops decompression when the actual output exceeds the declared size', async () => {
    const a = bytes(compressed);
    const data = new DataView(a.buffer);
    const central = data.getUint32(a.length - 22 + 16, true);
    data.setUint32(central + 24, 2, true);
    data.setBigUint64(39, 2n, true);
    const blob = new Blob([a]);
    const entries = await listNpz(blob);
    expect(entries).toBeDefined();
    expect(await extractNpz(blob, entries![0]!)).toBeUndefined();
  });
});
