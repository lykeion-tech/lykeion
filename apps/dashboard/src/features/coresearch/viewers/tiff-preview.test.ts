import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { parseTiff } from './tiff-preview';
const fixture = (name: string) =>
  new Uint8Array(readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`));
it('renders uncompressed grayscale, RGB and 16-bit grayscale', () => {
  const gray = parseTiff(fixture('tiff-gray.tiff'), 0)!;
  expect(gray).toMatchObject({ width: 2, height: 2, pages: 1 });
  expect([...gray.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 64, 128, 255]);
  expect([...parseTiff(fixture('tiff-rgb.tif'), 0)!.rgba]).toEqual([
    255, 0, 0, 255, 0, 255, 0, 255,
  ]);
  expect(parseTiff(fixture('tiff-gray16.tiff'), 0)?.displayReducedPrecision).toBe(true);
  expect(gray.displayReducedPrecision).toBe(false);
  expect([...parseTiff(fixture('tiff-gray16.tiff'), 0)!.rgba]).toEqual([
    0, 0, 0, 255, 255, 255, 255, 255,
  ]);
  expect([...parseTiff(fixture('tiff-big16.tiff'), 0)!.rgba]).toEqual([
    0, 0, 0, 255, 255, 255, 255, 255,
  ]);
});
it('rejects compressed, rotated, overlapping and circular directories', () => {
  const entry = (data: Uint8Array, id: number) => {
    const view = new DataView(data.buffer, data.byteOffset, data.byteLength),
      ifd = view.getUint32(4, true),
      count = view.getUint16(ifd, true);
    for (let i = 0; i < count; i++) {
      const at = ifd + 2 + i * 12;
      if (view.getUint16(at, true) === id) return at;
    }
    throw Error('missing fixture tag');
  };
  const compressed = fixture('tiff-gray.tiff');
  new DataView(compressed.buffer).setUint16(entry(compressed, 259) + 8, 5, true);
  expect(parseTiff(compressed, 0)).toBeUndefined();
  const rotated = fixture('tiff-gray.tiff');
  {
    const v = new DataView(rotated.buffer),
      at = entry(rotated, 284);
    v.setUint16(at, 274, true);
    v.setUint16(at + 8, 2, true);
  }
  expect(parseTiff(rotated, 0)).toBeUndefined();
  const overlap = fixture('tiff-gray.tiff');
  new DataView(overlap.buffer).setUint32(entry(overlap, 273) + 8, 8, true);
  expect(parseTiff(overlap, 0)).toBeUndefined();
  const circular = fixture('tiff-gray.tiff');
  {
    const v = new DataView(circular.buffer),
      ifd = v.getUint32(4, true),
      count = v.getUint16(ifd, true);
    v.setUint32(ifd + 2 + count * 12, ifd, true);
  }
  expect(parseTiff(circular, 0)).toBeUndefined();
});
it('selects pages and keeps identifying description tags private', () => {
  const result = parseTiff(fixture('tiff-multipage.tiff'), 1)!;
  expect(result.pages).toBe(2);
  expect([...result.rgba].filter((_, i) => i % 4 === 0)).toEqual([255, 128, 64, 0]);
  expect(JSON.stringify(parseTiff(fixture('tiff-private.tif'), 0))).not.toContain(
    'PRIVATE PATIENT SENTINEL',
  );
  expect(parseTiff(fixture('tiff-spacing.tif'), 0)).toMatchObject({
    aspect: 0.5,
    spacingKnown: true,
  });
});
it('rejects missing data and invalid page selections', () => {
  const file = fixture('tiff-gray.tiff');
  expect(parseTiff(file.slice(0, -1), 0)).toBeUndefined();
  expect(parseTiff(file, 1)).toBeUndefined();
  expect(parseTiff(new Uint8Array([0, 1, 2]), 0)).toBeUndefined();
});
