import { expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { parseBcf } from './bcf-preview';
import { readBgzf } from './bgzf-preview';

function fixture(
  filter = [0x00],
  definitions = '',
  infoBytes: number[] = [],
  infoCount = 0,
  sampleNames: string[] = [],
  formatBytes: number[] = [],
  formatCount = 0,
) {
  const header = new TextEncoder().encode(
    `##fileformat=VCFv4.3\n${definitions}##contig=<ID=chr1,IDX=0>\n#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO${sampleNames.length ? '\tFORMAT\t' + sampleNames.join('\t') : ''}\n\0`,
  );
  const body = new Uint8Array(24 + 1 + 4 + 2 + 2 + filter.length - 1 + infoBytes.length);
  const b = new DataView(body.buffer);
  b.setInt32(0, 0, true);
  b.setInt32(4, 9, true);
  b.setInt32(8, 1, true);
  b.setUint32(12, 0x7f800001, true);
  b.setUint32(16, (2 << 16) | infoCount, true);
  b.setUint32(20, (formatCount << 24) | sampleNames.length, true);
  body.set([0x37, 114, 115, 49, 0x17, 65, 0x17, 67, ...filter, ...infoBytes], 24);
  const out = new Uint8Array(9 + header.length + 8 + body.length + formatBytes.length);
  out.set([66, 67, 70, 2, 2]);
  new DataView(out.buffer).setUint32(5, header.length, true);
  out.set(header, 9);
  new DataView(out.buffer).setUint32(9 + header.length, body.length, true);
  new DataView(out.buffer).setUint32(13 + header.length, formatBytes.length, true);
  out.set(body, 17 + header.length);
  out.set(formatBytes, 17 + header.length + body.length);
  return out;
}
it('decodes phased, missing and haploid genotypes while rejecting invalid alleles and padding', () => {
  const defs = '##FORMAT=<ID=GT,Number=1,Type=String,IDX=1>\n';
  const samples = ['one', 'two', 'three'];
  const record = (data: number[]) =>
    fixture([0], defs, [], 0, samples, [0x11, 1, 0x21, ...data], 1);
  expect(parseBcf(record([2, 5, 0, 2, 4, 0x81]), false)?.rows[0]?.slice(8)).toEqual([
    '0|1',
    './0',
    '1',
  ]);
  expect(parseBcf(record([2, 6, 0, 2, 4, 0x81]), false)).toBeUndefined();
  expect(parseBcf(record([0x81, 2, 0, 2, 4, 0x81]), false)).toBeUndefined();
  const newer = record([2, 5, 0, 2, 4, 0x81]);
  const marker = new TextEncoder().encode('VCFv4.3');
  const offset = newer.findIndex((_, i) => marker.every((value, j) => newer[i + j] === value));
  newer[offset + marker.length - 1] = 0x34;
  expect(parseBcf(newer, false)).toBeUndefined();
  const many = Array.from({ length: 17 }, (_, i) => `S${i}`);
  const preview = parseBcf(
    fixture([0], defs, [], 0, many, [0x11, 1, 0x11, ...many.map(() => 2)], 1),
    false,
  );
  expect(preview?.samples.length).toBe(16);
  expect(preview?.samplesTruncated).toBe(true);
  expect(preview?.rows[0]?.slice(8)).toEqual(Array(16).fill('0'));
});
it('reads variant coordinates and missing quality without inventing annotations', () => {
  expect(parseBcf(fixture(), false)?.rows).toEqual([
    ['chr1', '10', 'rs1', 'A', 'C', '.', '.', '.'],
  ]);
});
it('validates INFO types, numeric sentinel values, counts and dictionary keys', () => {
  const defs =
    '##INFO=<ID=DP,Number=1,Type=Integer,IDX=1>\n##INFO=<ID=AF,Number=A,Type=Float,IDX=2>\n##INFO=<ID=FLAG,Number=0,Type=Flag,IDX=3>\n';
  expect(
    parseBcf(
      fixture([0], defs, [0x11, 1, 0x11, 7, 0x11, 2, 0x15, 0, 0, 0, 0x3f, 0x11, 3, 0], 3),
      false,
    )?.rows[0]?.[7],
  ).toBe('DP=7;AF=0.5;FLAG');
  expect(parseBcf(fixture([0], defs, [0x11, 1, 0x11, 0x80], 1), false)?.rows[0]?.[7]).toBe('DP=.');
  expect(
    parseBcf(fixture([0], defs, [0x11, 2, 0x15, 1, 0, 0x80, 0x7f], 1), false)?.rows[0]?.[7],
  ).toBe('AF=.');
  expect(parseBcf(fixture([0], defs, [0x11, 2, 0x15, 0, 0, 0x80, 0x7f], 1), false)).toBeUndefined();
  expect(parseBcf(fixture([0], defs, [0x11, 1, 0x21, 7, 8], 1), false)?.rows[0]?.[7]).toBe(
    'DP=7,8',
  );
  expect(parseBcf(fixture([0], defs, [0x11, 1, 0x15, 0, 0, 0, 0x3f], 1), false)).toBeUndefined();
  expect(parseBcf(fixture([0], defs, [0x11, 4, 0x11, 7], 1), false)).toBeUndefined();
  expect(parseBcf(fixture([0], defs, [0x11, 1, 0x31, 7, 0x81, 8], 1), false)).toBeUndefined();
});
it('distinguishes missing and named filters and rejects invalid or ambiguous dictionary indices', () => {
  const definitions =
    '##FILTER=<ID=PASS,Description="All filters passed",IDX=0>\n##FILTER=<ID=q10,Description="Low quality",IDX=2>\n##FILTER=<ID=other,Description="Other",IDX=3>\n';
  expect(parseBcf(fixture([0x11, 0x80], definitions), false)?.rows[0]?.[6]).toBe('.');
  expect(parseBcf(fixture([0x21, 2, 3], definitions), false)?.rows[0]?.[6]).toBe('q10;other');
  expect(parseBcf(fixture([0x11, 4], definitions), false)).toBeUndefined();
  expect(parseBcf(fixture([0x21, 0, 2], definitions), false)).toBeUndefined();
  expect(parseBcf(fixture([0x21, 2, 2], definitions), false)).toBeUndefined();
  expect(
    parseBcf(fixture([0x11, 2], `${definitions}##INFO=<ID=conflict,IDX=2>\n`), false),
  ).toBeUndefined();
  expect(parseBcf(fixture([0x11, 0]), false)?.rows[0]?.[6]).toBe('PASS');
  const implicit =
    '##INFO=<ID=DP,Number=1,Type=Integer,Description="Depth">\n##FILTER=<ID=q10,Description="Low quality">\n';
  expect(parseBcf(fixture([0x11, 2], implicit), false)?.rows[0]?.[6]).toBe('q10');
  expect(
    parseBcf(fixture([0x11, 2], `${implicit}##FORMAT=<ID=GT,IDX=3>\n`), false),
  ).toBeUndefined();
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
  expect(
    parseBcf(decoded!.bytes, decoded!.partial)
      ?.rows.slice(0, 4)
      .map((row) => row.slice(0, 8)),
  ).toEqual([
    ['1', '3000150', '.', 'C', 'T', String(Math.fround(59.2)), 'PASS', 'AN=4;AC=2'],
    ['1', '3000151', '.', 'C', 'T', String(Math.fround(59.2)), 'PASS', 'AN=4;AC=2'],
    [
      '1',
      '3062915',
      'id3D',
      'GTTT',
      'G',
      String(Math.fround(12.9)),
      'q10',
      'DP4=1,2,3,4;AN=4;AC=2;INDEL;STR=test',
    ],
    [
      '1',
      '3062915',
      'idSNP',
      'G',
      'T,C',
      String(Math.fround(12.6)),
      'test',
      'TEST=5;DP4=1,2,3,4;AN=3;AC=1,1',
    ],
  ]);
  const preview = parseBcf(decoded!.bytes, decoded!.partial);
  expect(preview?.samples).toEqual(['A', 'B']);
  expect(preview?.rows.slice(0, 4).map((row) => row.slice(8))).toEqual([
    ['0/1', '0/1'],
    ['0/1', '0/1'],
    ['0/1', '0/1'],
    ['0/1', '2'],
  ]);
});
