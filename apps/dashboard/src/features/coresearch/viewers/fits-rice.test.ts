import { expect, it } from 'vitest';
import { decodeFitsRice } from './fits-rice';
it('decodes a zero-difference block with an explicit first pixel', () => {
  expect(decodeFitsRice(new Uint8Array([7, 0]), 3, 1, 32)).toEqual(new Uint8Array([7, 7, 7]));
});
it('rejects truncated streams, reserved codes, unterminated unary runs, excess data and unsafe sizes', () => {
  expect(decodeFitsRice(new Uint8Array([7]), 3, 1, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([0, 0, 0, 0, 0xf8]), 1, 4, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([0, 0, 0, 0, 0x08, 0, 0]), 1, 4, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([7, 0, 0]), 3, 1, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([7, 1]), 3, 1, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([0]), 1, 8, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([0]), 8 * 1024 * 1024 + 1, 1, 32)).toBeUndefined();
  expect(decodeFitsRice(new Uint8Array([0]), 1, 1, 0)).toBeUndefined();
});
