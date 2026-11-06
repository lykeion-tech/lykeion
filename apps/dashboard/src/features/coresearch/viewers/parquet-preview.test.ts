import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { parseParquet } from './parquet-preview';
const fixture = (name: string) =>
  new Uint8Array(readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`));
it('samples real required, plain, uncompressed PyArrow pages across row groups', () => {
  const result = parseParquet(fixture('plain-required.parquet'));
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
it('fails closed for unsupported compression and malformed/truncated files', () => {
  expect(parseParquet(fixture('unsupported-snappy.parquet'))).toBeUndefined();
  const valid = fixture('plain-required.parquet');
  expect(parseParquet(valid.slice(0, -1))).toBeUndefined();
  expect(parseParquet(new Uint8Array([80, 65, 82, 49]))).toBeUndefined();
});
it('rejects corrupt data and oversized input', () => {
  const corrupt = fixture('plain-required.parquet');
  corrupt[30] ^= 1;
  expect(parseParquet(corrupt)).toBeUndefined();
  expect(parseParquet(new Uint8Array(32 * 1024 * 1024 + 1))).toBeUndefined();
});

it('samples independently generated nullable primitives across row groups', () => {
  expect(parseParquet(fixture('optional-primitives.parquet'))?.rows).toEqual([
    ['1', null, 'null'],
    [null, 'true', null],
    ['9007199254740993', 'false', 'x'],
    [null, null, ''],
  ]);
});
it('preserves nullable values across pages and row groups when sampling', () => {
  const p = parseParquet(fixture('optional-multipage.parquet'));
  expect(p?.totalRows).toBe('400');
  expect(p?.truncated).toBe(true);
  expect(p?.rows).toEqual(Array.from({ length: 200 }, (_, i) => [i % 3 === 0 ? null : String(i)]));
});

it('reads independently generated nullable dictionary columns across row groups', () => {
  expect(parseParquet(fixture('dictionary-primitives.parquet'))?.rows).toEqual([
    ['1', 'null', null],
    [null, null, '1.5'],
    ['9007199254740993', 'x', '-3'],
    ['1', 'null', '1.5'],
    [null, 'x', null],
  ]);
});
it('handles dictionary pages across sampled pages and row groups including all-null columns', () => {
  const p = parseParquet(fixture('dictionary-multipage.parquet'));
  expect(p?.totalRows).toBe('400');
  expect(p?.rows).toEqual(
    Array.from({ length: 200 }, (_, i) => [
      i % 5 === 0 ? null : String(9007199254740993n + BigInt(i % 13)),
      null,
    ]),
  );
});
it('rejects corrupted dictionary pages before interpreting values', () => {
  const bytes = fixture('dictionary-primitives.parquet');
  bytes[30] ^= 1;
  expect(parseParquet(bytes)).toBeUndefined();
});
