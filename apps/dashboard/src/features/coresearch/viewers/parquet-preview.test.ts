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
it('rejects nullable columns, dictionary pages, corrupt data and oversized input', () => {
  expect(parseParquet(fixture('unsupported-optional.parquet'))).toBeUndefined();
  expect(parseParquet(fixture('unsupported-dictionary.parquet'))).toBeUndefined();
  const corrupt = fixture('plain-required.parquet');
  corrupt[30] ^= 1;
  expect(parseParquet(corrupt)).toBeUndefined();
  expect(parseParquet(new Uint8Array(32 * 1024 * 1024 + 1))).toBeUndefined();
});
