// @vitest-environment node
import { readFile } from 'node:fs/promises';
import { expect, it, vi } from 'vitest';
import init from './vendor/heic/safe-heic.mjs';

it('decodes a synthetic HEIC and fails closed on damaged files without retaining pixels', async () => {
  vi.stubGlobal('self', { location: { href: 'file:///synthetic/' } });
  try {
    const wasm = await readFile(
      'apps/dashboard/src/features/coresearch/viewers/vendor/heic/safe-heic.wasm',
    );
    const fixture = await readFile(
      'apps/dashboard/src/features/coresearch/viewers/fixtures/heic/red-8x8.heic',
    );
    const decoder = await init({ wasmBinary: wasm, print: () => {}, printErr: () => {} });
    const decode = (bytes: Uint8Array) => {
      const ptr = decoder._malloc(bytes.length);
      decoder.HEAPU8.set(bytes, ptr);
      try {
        return decoder._preview_decode(ptr, bytes.length);
      } finally {
        decoder._free(ptr);
      }
    };
    expect(decode(fixture)).toBe(1);
    expect([decoder._preview_width(), decoder._preview_height()]).toEqual([8, 8]);
    expect([
      ...decoder.HEAPU8.slice(decoder._preview_pixels(), decoder._preview_pixels() + 4),
    ]).toEqual([254, 0, 0, 255]);
    expect(decode(fixture.subarray(0, 50))).toBe(0);
    expect([
      decoder._preview_width(),
      decoder._preview_height(),
      decoder._preview_pixels(),
    ]).toEqual([0, 0, 0]);
    expect(decode(new Uint8Array(1024))).toBe(0);
    expect(decode(new Uint8Array(8 * 1024 * 1024 + 1))).toBe(0);
    for (const name of ['multi', 'seq', 'wide']) {
      const bytes = await readFile(
        `apps/dashboard/src/features/coresearch/viewers/fixtures/heic/lykeion-heic-${name}.heic`,
      );
      expect(decode(bytes)).toBe(0);
      expect([
        decoder._preview_width(),
        decoder._preview_height(),
        decoder._preview_pixels(),
      ]).toEqual([0, 0, 0]);
    }
    decoder._preview_release();
  } finally {
    vi.unstubAllGlobals();
  }
});
