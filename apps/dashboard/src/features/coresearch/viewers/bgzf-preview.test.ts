import { gzipSync } from 'node:zlib';
import { expect, it } from 'vitest';
import { readBgzf } from './bgzf-preview';
function block(data: string) {
  const gzip = gzipSync(Buffer.from(data));
  const out = new Uint8Array(gzip.length + 8);
  out.set(gzip.subarray(0, 10));
  out[3] = 4;
  out.set([6, 0, 66, 67, 2, 0], 10);
  new DataView(out.buffer).setUint16(16, out.length - 1, true);
  out.set(gzip.subarray(10), 18);
  return out;
}
it('reads independent BGZF members and validates checksums', async () => {
  const a = block('hello'),
    b = block(' world');
  expect(new TextDecoder().decode((await readBgzf(new Blob([a, b])))?.bytes)).toBe('hello world');
  const bad = a.slice();
  bad[bad.length - 8] ^= 1;
  expect(await readBgzf(new Blob([bad]))).toBeUndefined();
});
it('rejects malformed member bounds', async () => {
  const f = block('abc');
  f[16] = 0;
  f[17] = 0;
  expect(await readBgzf(new Blob([f]))).toBeUndefined();
  expect(await readBgzf(new Blob([block('x').slice(0, -1)]))).toBeUndefined();
});
