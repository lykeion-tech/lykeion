import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { readSqliteSnapshot } from './sqlite-snapshot';
const fixture = () =>
  new Uint8Array(
    readFileSync('apps/dashboard/src/features/coresearch/viewers/fixtures/sqlite-snapshot.db'),
  );
describe('SQLite physical snapshot', () => {
  it('reads ordinary table records exactly without executing schema', () => {
    const snapshot = readSqliteSnapshot(fixture());
    const table = snapshot?.tables.find((t) => t.name === 'samples');
    expect(table?.rows.map((r) => r.fields)).toEqual([
      ['alpha', '9223372036854775807'],
      ['beta', '-9223372036854775808'],
    ]);
    expect(snapshot?.tables.find((t) => t.name === 'no_rowid')?.unavailable).toBeTruthy();
    expect(snapshot?.tables.some((t) => t.name === 'v')).toBe(false);
  });
  it('rejects invalid headers and truncated pages', () => {
    const bad = fixture();
    bad[0] = 0;
    expect(readSqliteSnapshot(bad)).toBeUndefined();
    expect(readSqliteSnapshot(fixture().slice(0, 100))).toBeUndefined();
  });
});
it('bounds multilevel trees and marks overflow as unsupported', () => {
  const bytes = new Uint8Array(
    readFileSync('apps/dashboard/src/features/coresearch/viewers/fixtures/sqlite-edge.db'),
  );
  const parsed = readSqliteSnapshot(bytes);
  const many = parsed?.tables.find((t) => t.name === 'many');
  expect(many?.rows).toHaveLength(25);
  expect(many?.truncated).toBe(true);
  expect(parsed?.tables.find((t) => t.name === 'oversized')?.unavailable).toBeTruthy();
});
it('rejects schema page pointers outside the database', () => {
  const bytes = fixture();
  bytes[103] = 255;
  bytes[104] = 255;
  expect(readSqliteSnapshot(bytes)).toBeUndefined();
});
it('rejects cyclic child pages instead of traversing forever', () => {
  const bytes = new Uint8Array(
    readFileSync('apps/dashboard/src/features/coresearch/viewers/fixtures/sqlite-edge.db'),
  );
  const view = new DataView(bytes.buffer);
  const base = 512;
  expect(bytes[base]).toBe(5);
  const cell = base + view.getUint16(base + 12, false);
  view.setUint32(cell, 2, false);
  expect(
    readSqliteSnapshot(bytes)?.tables.find((t) => t.name === 'many')?.unavailable,
  ).toBeTruthy();
});
it('rejects overlapping cell extents and unsupported freeblocks', () => {
  const bytes = fixture(),
    page = 4096,
    view = new DataView(bytes.buffer);
  const start = page + 4080;
  bytes.set([7, 1, 2, 22, 3, 2, 2, 15, 88], start);
  view.setUint16(page + 8, 4080, false);
  view.setUint16(page + 10, 4084, false);
  expect(
    readSqliteSnapshot(bytes)?.tables.find((t) => t.name === 'samples')?.unavailable,
  ).toBeTruthy();
  const free = fixture();
  new DataView(free.buffer).setUint16(page + 1, 4090, false);
  expect(
    readSqliteSnapshot(free)?.tables.find((t) => t.name === 'samples')?.unavailable,
  ).toBeTruthy();
});
it('rejects repeated table roots and caps decoded output', async () => {
  const duplicate = new Uint8Array(
    readFileSync('apps/dashboard/src/features/coresearch/viewers/fixtures/sqlite-duplicate.db'),
  );
  expect(readSqliteSnapshot(duplicate)).toBeUndefined();
  const { gunzipSync } = await import('node:zlib');
  const bytes = new Uint8Array(
    gunzipSync(
      readFileSync('apps/dashboard/src/features/coresearch/viewers/fixtures/sqlite-budget.db.gz'),
    ),
  );
  const table = readSqliteSnapshot(bytes)?.tables.find((t) => t.name === 'large');
  expect(table?.unavailable).toContain('output limit');
  expect((table?.rows ?? []).reduce((n, r) => n + r.fields.join('').length, 0)).toBeLessThanOrEqual(
    262144,
  );
});
it('rejects pointers before the cell-content area and invalid zero sentinel', () => {
  const bytes = fixture(),
    page = 4096;
  new DataView(bytes.buffer).setUint16(page + 5, 4090, false);
  expect(
    readSqliteSnapshot(bytes)?.tables.find((t) => t.name === 'samples')?.unavailable,
  ).toBeTruthy();
  const zero = fixture();
  new DataView(zero.buffer).setUint16(page + 5, 0, false);
  expect(
    readSqliteSnapshot(zero)?.tables.find((t) => t.name === 'samples')?.unavailable,
  ).toBeTruthy();
});
it('accepts the zero content-area sentinel for an empty 65536-byte page', async () => {
  const { gunzipSync } = await import('node:zlib');
  const bytes = new Uint8Array(
    gunzipSync(
      readFileSync(
        'apps/dashboard/src/features/coresearch/viewers/fixtures/sqlite-page65536.db.gz',
      ),
    ),
  );
  expect(readSqliteSnapshot(bytes)?.tables.find((t) => t.name === 'empty')?.rows).toEqual([]);
});
