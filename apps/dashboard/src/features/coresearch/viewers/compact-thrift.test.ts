import { expect, it } from 'vitest';
import { readCompactStruct } from './compact-thrift';
it('reads compact fields and rejects oversized lists and unterminated varints', () => {
  expect(readCompactStruct(new Uint8Array([0x15, 0x06, 0]), 0, 3)?.fields.get(1)).toBe(3n);
  expect(
    readCompactStruct(new Uint8Array([0x19, 0xf5, 0xff, 0xff, 0xff, 0xff, 0x07, 0]), 0, 8),
  ).toBeUndefined();
  expect(
    readCompactStruct(new Uint8Array([0x15, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80]), 0, 7),
  ).toBeUndefined();
});
