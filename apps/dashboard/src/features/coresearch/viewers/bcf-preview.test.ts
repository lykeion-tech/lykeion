import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { parseBcf } from './bcf-preview';
import { readBgzf } from './bgzf-preview';

function fixture() {
  const header = new TextEncoder().encode(
    '##fileformat=VCFv4.3\n##contig=<ID=chr1,IDX=0>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO\n\0',
  );
  const body = new Uint8Array(24 + 1 + 4 + 2 + 2);
  const b = new DataView(body.buffer);
  b.setInt32(0, 0, true);
  b.setInt32(4, 9, true);
  b.setInt32(8, 1, true);
  b.setUint32(12, 0x7f800001, true);
  b.setUint32(16, 2 << 16, true);
  body.set([0x37, 114, 115, 49, 0x17, 65, 0x17, 67, 0x00], 24);
  const out = new Uint8Array(9 + header.length + 8 + body.length);
  out.set([66, 67, 70, 2, 2]);
  new DataView(out.buffer).setUint32(5, header.length, true);
  out.set(header, 9);
  new DataView(out.buffer).setUint32(9 + header.length, body.length, true);
  out.set(body, 17 + header.length);
  return out;
}
it('reads variant coordinates and missing quality without inventing annotations', () => {
  expect(parseBcf(fixture(), false)?.rows).toEqual([['chr1', '10', 'rs1', 'A', 'C', '.']]);
});
it('rejects invalid contig and truncated records', () => {
  const f = fixture();
  const d = new DataView(f.buffer);
  d.setInt32(f.length - 33, 2, true);
  expect(parseBcf(f, false)).toBeUndefined();
  expect(parseBcf(fixture().slice(0, -1), false)).toBeUndefined();
  const badLength = fixture();
  new DataView(badLength.buffer).setUint32(badLength.length - 41, 0xffffffff, true);
  expect(parseBcf(badLength, true)).toBeUndefined();
});

// HTSlib MIT-licensed test/tabix/vcf_file.bcf, commit 828364c128c17a745fc95681b99f0eaeafd6d929.
it('reads the independently produced HTSlib BCF fixture', async () => {
  const bytes = readFileSync(
    'apps/dashboard/src/features/coresearch/viewers/fixtures/htslib-vcf-file.bcf',
  );
  const decoded = await readBgzf(new Blob([bytes]));
  expect(decoded).toBeDefined();
  expect(parseBcf(decoded!.bytes, decoded!.partial)?.rows.slice(0, 2)).toEqual([
    ['1', '3000150', '.', 'C', 'T', String(Math.fround(59.2))],
    ['1', '3000151', '.', 'C', 'T', String(Math.fround(59.2))],
  ]);
});
