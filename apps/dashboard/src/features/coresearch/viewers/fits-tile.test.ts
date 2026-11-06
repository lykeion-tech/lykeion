import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { inflateFitsTiles } from './fits-tile';
import { readFits } from './fits-preview';
const fixture = (name: string) =>
  new Blob([readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`)]);
it('reconstructs independent Astropy GZIP_1 tiles including partial edge tiles', async () => {
  const result = await inflateFitsTiles(fixture('fits-tiles-GZIP_1.fz'));
  expect(result).toBeDefined();
  expect(await readFits(result!)).toMatchObject({
    shape: [3, 2],
    values: ['1', '2', '3', '4', '5', '6'],
    bitpix: 16,
  });
});
it('rejects unsupported GZIP_2 and truncated tiles', async () => {
  expect(await inflateFitsTiles(fixture('fits-tiles-GZIP_2.fz'))).toBeUndefined();
  const f = fixture('fits-tiles-GZIP_1.fz');
  expect(await inflateFitsTiles(f.slice(0, -1))).toBeUndefined();
});
it('preserves physical scaling and rejects floating-point quantization', async () => {
  const x = await inflateFitsTiles(fixture('fits-tiles-scaled.fz'));
  expect(await readFits(x!)).toMatchObject({
    values: ['10', '12', '14', '16', '18', '20'],
    scale: 2,
    zero: 10,
  });
  expect(await inflateFitsTiles(fixture('fits-tiles-float.fz'))).toBeUndefined();
});
it('preserves integer BLANK pixels instead of inventing values', async () => {
  const b = await inflateFitsTiles(fixture('fits-tiles-blank.fz'));
  expect((await readFits(b!))?.values).toEqual(['1', 'blank']);
});
it('rejects corrupted gzip tiles, overlapping descriptors and invalid offsets', async () => {
  const bytes = new Uint8Array(await fixture('fits-tiles-GZIP_1.fz').arrayBuffer());
  const { listFitsHdus } = await import('./fits-preview');
  const h = (await listFitsHdus(new Blob([bytes])))![1]!;
  const table = h.offset,
    n = Number(h.fields.get('NAXIS1')) * Number(h.fields.get('NAXIS2')),
    dv = new DataView(bytes.buffer),
    len = dv.getInt32(table, false),
    off = dv.getInt32(table + 4, false);
  const corrupt = bytes.slice();
  corrupt[table + n + off + len - 1] ^= 1;
  expect(await inflateFitsTiles(new Blob([corrupt]))).toBeUndefined();
  const overlap = bytes.slice(),
    ov = new DataView(overlap.buffer);
  ov.setInt32(table + 8, len, false);
  ov.setInt32(table + 12, off, false);
  expect(await inflateFitsTiles(new Blob([overlap]))).toBeUndefined();
  const out = bytes.slice();
  new DataView(out.buffer).setInt32(table + 4, 0x7fffffff, false);
  expect(await inflateFitsTiles(new Blob([out]))).toBeUndefined();
});

it.each([
  ['u8', ['0', '255', '1', '255', '0', '255']],
  ['i16', ['-32768', '32767', '-1', '0', '32767', '-32768']],
  ['i32', ['-2147483648', '2147483647', '-1', '0', '2147483647', '-2147483648']],
])('reconstructs Astropy RICE_1 %s pixels with wraparound', async (kind, values) => {
  const b = await inflateFitsTiles(fixture(`fits-rice-${kind}.fz`));
  expect((await readFits(b!))?.values).toEqual(values);
});
it.each(['u8', 'i16', 'i32'])(
  'matches independently decoded high-entropy and constant Rice blocks for %s',
  async (kind) => {
    const expected = JSON.parse(
      readFileSync(
        `apps/dashboard/src/features/coresearch/viewers/fixtures/fits-rice-random-${kind}.json`,
        'utf8',
      ),
    );
    const b = await inflateFitsTiles(fixture(`fits-rice-random-${kind}.fz`));
    expect((await readFits(b!))?.values).toEqual(expected);
  },
);
it('preserves Rice image scaling and BLANK pixels', async () => {
  const b = await inflateFitsTiles(fixture('fits-rice-scaled.fz'));
  expect((await readFits(b!))?.values).toEqual(['10', '12', 'blank']);
});
it('rejects unsupported Rice parameter values without interpreting pixel data', async () => {
  const src = new Uint8Array(await fixture('fits-rice-i16.fz').arrayBuffer());
  const ascii = new TextDecoder().decode(src),
    at = ascii.indexOf('ZVAL1');
  expect(at).toBeGreaterThan(0);
  const changed = src.slice();
  changed.set(new TextEncoder().encode('ZVAL1   =                   16'.padEnd(80, ' ')), at);
  expect(await inflateFitsTiles(new Blob([changed]))).toBeUndefined();
});
