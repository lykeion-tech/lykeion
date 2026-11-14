import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { parseParquet } from './parquet-preview';
import { readCompactStruct } from './compact-thrift';
const fixture = (name: string) =>
  new Uint8Array(readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`));
it('samples real required, plain, uncompressed PyArrow pages across row groups', async () => {
  const result = await parseParquet(fixture('plain-required.parquet'));
  expect(result?.columns.map((c) => c.name)).toEqual([
    'flag',
    'count',
    'big',
    'value',
    'label',
    'binary',
  ]);
  expect(result?.rows).toEqual([
    ['true', '123', '9007199254740993', '1.5', 'hello', '[2 bytes]'],
    ['false', '-7', '-9007199254740993', '-2.25', 'world', '[3 bytes]'],
    ['true', '42', '1', '3', 'third', '[3 bytes]'],
  ]);
  expect(result?.totalRows).toBe('3');
});
it('fails closed for unsupported compression and malformed/truncated files', async () => {
  expect(await parseParquet(fixture('unsupported-zstd.parquet'))).toBeUndefined();
  const valid = fixture('plain-required.parquet');
  expect(await parseParquet(valid.slice(0, -1))).toBeUndefined();
  expect(await parseParquet(new Uint8Array([80, 65, 82, 49]))).toBeUndefined();
});
it('rejects corrupt data and oversized input', async () => {
  const corrupt = fixture('plain-required.parquet');
  corrupt[30] ^= 1;
  expect(await parseParquet(corrupt)).toBeUndefined();
  expect(await parseParquet(new Uint8Array(32 * 1024 * 1024 + 1))).toBeUndefined();
});

it('samples independently generated nullable primitives across row groups', async () => {
  expect((await parseParquet(fixture('optional-primitives.parquet')))?.rows).toEqual([
    ['1', null, 'null'],
    [null, 'true', null],
    ['9007199254740993', 'false', 'x'],
    [null, null, ''],
  ]);
});
it('preserves nullable values across pages and row groups when sampling', async () => {
  const p = await parseParquet(fixture('optional-multipage.parquet'));
  expect(p?.totalRows).toBe('400');
  expect(p?.truncated).toBe(true);
  expect(p?.rows).toEqual(Array.from({ length: 200 }, (_, i) => [i % 3 === 0 ? null : String(i)]));
});

it('reads independently generated nullable dictionary columns across row groups', async () => {
  expect((await parseParquet(fixture('dictionary-primitives.parquet')))?.rows).toEqual([
    ['1', 'null', null],
    [null, null, '1.5'],
    ['9007199254740993', 'x', '-3'],
    ['1', 'null', '1.5'],
    [null, 'x', null],
  ]);
});
it('handles dictionary pages across sampled pages and row groups including all-null columns', async () => {
  const p = await parseParquet(fixture('dictionary-multipage.parquet'));
  expect(p?.totalRows).toBe('400');
  expect(p?.rows).toEqual(
    Array.from({ length: 200 }, (_, i) => [
      i % 5 === 0 ? null : String(9007199254740993n + BigInt(i % 13)),
      null,
    ]),
  );
});
it('rejects corrupted dictionary pages before interpreting values', async () => {
  const bytes = fixture('dictionary-primitives.parquet');
  bytes[30] ^= 1;
  expect(await parseParquet(bytes)).toBeUndefined();
});

it('samples Snappy compressed pages with plain and dictionary values, including nulls', async () => {
  for (const [compressed, reference] of [
    ['unsupported-snappy.parquet', 'plain-required.parquet'],
    ['optional-primitives-snappy.parquet', 'optional-primitives.parquet'],
    ['dictionary-primitives-snappy.parquet', 'dictionary-primitives.parquet'],
    ['dictionary-multipage-snappy.parquet', 'dictionary-multipage.parquet'],
  ]) {
    expect((await parseParquet(fixture(compressed)))?.rows).toEqual(
      (await parseParquet(fixture(reference)))?.rows,
    );
  }
});

it('rejects corrupted compressed data with CRC and invalid Snappy sizes without CRC', async () => {
  const crc = fixture('dictionary-primitives-snappy.parquet');
  const dict = readCompactStruct(crc, 4, crc.length)!;
  crc[dict.next] ^= 1;
  expect(await parseParquet(crc)).toBeUndefined();
  const noCrc = fixture('snappy-no-crc.parquet');
  expect((await parseParquet(noCrc))?.rows).toHaveLength(100);
  const header = readCompactStruct(noCrc, 4, noCrc.length)!;
  noCrc[header.next] ^= 1;
  expect(await parseParquet(noCrc)).toBeUndefined();
});
it('samples PyArrow Data Page V2 with plain and dictionary values and both codecs', async () => {
  for (const name of [
    'plain-required',
    'optional-primitives',
    'dictionary-primitives',
    'dictionary-multipage',
  ])
    for (const codec of ['none', 'snappy']) {
      expect((await parseParquet(fixture(`${name}-v2-${codec}.parquet`)))?.rows).toEqual(
        (await parseParquet(fixture(`${name}.parquet`)))?.rows,
      );
    }
});
it('rejects V2 inconsistent rows, null counts and level lengths without relying on CRC', async () => {
  const f = fixture('optional-v2-no-crc.parquet');
  expect(await parseParquet(f)).toBeDefined();
  const signature = [0x5c, 0x15, 8, 0x15, 4, 0x15, 8, 0x15, 0, 0x15, 4, 0x15, 0];
  const at = f.findIndex((_, i) => signature.every((b, j) => f[i + j] === b));
  expect(at).toBeGreaterThan(0);
  for (const [offset, value] of [
    [4, 0],
    [6, 6],
    [10, 126],
  ]) {
    const corrupt = f.slice();
    corrupt[at + offset] = value;
    expect(await parseParquet(corrupt)).toBeUndefined();
  }
});

it('samples independently generated GZIP V1 and V2 pages', async () => {
  for (const name of [
    'plain-required',
    'optional-primitives',
    'dictionary-primitives',
    'dictionary-multipage',
  ])
    for (const version of ['1', '2']) {
      expect((await parseParquet(fixture(`${name}-v${version}-gzip.parquet`)))?.rows).toEqual(
        (await parseParquet(fixture(`${name}.parquet`)))?.rows,
      );
    }
});
it('rejects corruption of gzip pages without a Parquet checksum', async () => {
  const f = fixture('gzip-no-crc.parquet');
  expect(await parseParquet(f)).toBeDefined();
  const header = readCompactStruct(f, 4, f.length)!;
  const v2 = header.fields.get(8) as Map<number, bigint>;
  const definitionLength = Number(v2.get(5));
  const corrupt = f.slice();
  corrupt[header.next + definitionLength] ^= 1;
  expect(await parseParquet(corrupt)).toBeUndefined();
});
it('preserves logical date, time, timestamp and fixed-point decimal values exactly', async () => {
  for (const codec of ['none', 'gzip']) {
    const p = await parseParquet(fixture(`logical-primitives-${codec}.parquet`));
    expect(p?.columns.map((c) => c.type)).toEqual([
      'DATE · days since 1970-01-01',
      'TIMESTAMP · ns since 1970-01-01 · UTC-adjusted',
      'TIMESTAMP · µs since 1970-01-01 · local',
      'TIME · ns since midnight · local',
      'DECIMAL(18,4)',
    ]);
    expect(p?.rows).toEqual([
      ['-1', '-9223372036854775807', '-1', '0', '-123.4500'],
      ['1', '9223372036854775807', '0', '86399999999999', '99999999999999.9999'],
      [null, '1', null, null, null],
    ]);
  }
});
