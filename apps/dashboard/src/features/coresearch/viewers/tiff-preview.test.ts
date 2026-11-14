import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { parseTiff } from './tiff-preview';
const fixture = (name: string) =>
  new Uint8Array(readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`));
it('renders uncompressed grayscale, RGB and 16-bit grayscale', async () => {
  const gray = (await parseTiff(fixture('tiff-gray.tiff'), 0))!;
  expect(gray).toMatchObject({ width: 2, height: 2, pages: 1 });
  expect([...gray.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 64, 128, 255]);
  expect([...(await parseTiff(fixture('tiff-rgb.tif'), 0))!.rgba]).toEqual([
    255, 0, 0, 255, 0, 255, 0, 255,
  ]);
  expect((await parseTiff(fixture('tiff-gray16.tiff'), 0))?.displayReducedPrecision).toBe(true);
  expect(gray.displayReducedPrecision).toBe(false);
  expect([...(await parseTiff(fixture('tiff-gray16.tiff'), 0))!.rgba]).toEqual([
    0, 0, 0, 255, 255, 255, 255, 255,
  ]);
  expect([...(await parseTiff(fixture('tiff-big16.tiff'), 0))!.rgba]).toEqual([
    0, 0, 0, 255, 255, 255, 255, 255,
  ]);
});
it('rejects compressed, rotated, overlapping and circular directories', async () => {
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
  expect(await parseTiff(compressed, 0)).toBeUndefined();
  const rotated = fixture('tiff-gray.tiff');
  {
    const v = new DataView(rotated.buffer),
      at = entry(rotated, 284);
    v.setUint16(at, 274, true);
    v.setUint16(at + 8, 2, true);
  }
  expect(await parseTiff(rotated, 0)).toBeUndefined();
  const overlap = fixture('tiff-gray.tiff');
  new DataView(overlap.buffer).setUint32(entry(overlap, 273) + 8, 8, true);
  expect(await parseTiff(overlap, 0)).toBeUndefined();
  const circular = fixture('tiff-gray.tiff');
  {
    const v = new DataView(circular.buffer),
      ifd = v.getUint32(4, true),
      count = v.getUint16(ifd, true);
    v.setUint32(ifd + 2 + count * 12, ifd, true);
  }
  expect(await parseTiff(circular, 0)).toBeUndefined();
});
it('selects pages and keeps identifying description tags private', async () => {
  const result = (await parseTiff(fixture('tiff-multipage.tiff'), 1))!;
  expect(result.pages).toBe(2);
  expect([...result.rgba].filter((_, i) => i % 4 === 0)).toEqual([255, 128, 64, 0]);
  expect(JSON.stringify(await parseTiff(fixture('tiff-private.tif'), 0))).not.toContain(
    'PRIVATE PATIENT SENTINEL',
  );
  expect(await parseTiff(fixture('tiff-spacing.tif'), 0)).toMatchObject({
    aspect: 0.5,
    spacingKnown: true,
  });
});
it('rejects missing data and invalid page selections', async () => {
  const file = fixture('tiff-gray.tiff');
  expect(await parseTiff(file.slice(0, -1), 0)).toBeUndefined();
  expect(await parseTiff(file, 1)).toBeUndefined();
  expect(await parseTiff(new Uint8Array([0, 1, 2]), 0)).toBeUndefined();
});
it('renders independent deflate-compressed multi-strip gray, big-endian 16-bit and RGB TIFFs', async () => {
  const gray = await parseTiff(fixture('tiff-deflate-gray.tif'), 0);
  expect([...gray!.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 64, 128, 255]);
  const be = await parseTiff(fixture('tiff-deflate-big16.tif'), 0);
  expect([...be!.rgba]).toEqual([0, 0, 0, 255, 255, 255, 255, 255]);
  const rgb = await parseTiff(fixture('tiff-deflate-rgb.tif'), 0);
  expect([...rgb!.rgba]).toEqual([255, 0, 0, 255, 0, 255, 0, 255]);
});
it('accepts TIFF Deflate tag 8 and rejects corrupted, truncated or predicted strips', async () => {
  const f = fixture('tiff-deflate-gray.tif');
  const view = new DataView(f.buffer);
  const little = f[0] === 0x49;
  const ifd = view.getUint32(4, little),
    n = view.getUint16(ifd, little);
  let compression = -1;
  for (let i = 0; i < n; i++) {
    const at = ifd + 2 + i * 12;
    if (view.getUint16(at, little) === 259) compression = at;
  }
  expect(compression).toBeGreaterThan(0);
  const tag8 = f.slice();
  new DataView(tag8.buffer).setUint16(compression + 8, 8, little);
  expect((await parseTiff(tag8, 0))?.width).toBe(2);
  const corrupt = f.slice();
  corrupt[217] ^= 1;
  expect(await parseTiff(corrupt, 0)).toBeUndefined();
  expect(await parseTiff(f.slice(0, -1), 0)).toBeUndefined();
});

it('reconstructs independent TIFF Deflate horizontal prediction in 8/16-bit gray and RGB', async () => {
  const gray = await parseTiff(fixture('tiff-predict-gray.tif'), 0);
  expect([...gray!.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 64, 128, 255]);
  const big = await parseTiff(fixture('tiff-predict-big16.tif'), 0);
  expect([...big!.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 255, 0]);
  const rgb = await parseTiff(fixture('tiff-predict-rgb.tif'), 0);
  expect([...rgb!.rgba]).toEqual([255, 0, 0, 255, 0, 255, 0, 255, 1, 2, 3, 255]);
});
it('resets prediction each row and strip and handles unsigned wraparound in both byte orders', async () => {
  const x = await parseTiff(fixture('tiff-predict-rows.tif'), 0);
  expect([...x!.rgba].filter((_, i) => i % 4 === 0)).toEqual([
    250, 10, 249, 3, 255, 1, 199, 0, 200,
  ]);
  const y = await parseTiff(fixture('tiff-predict-little16.tif'), 0);
  expect([...y!.rgba].filter((_, i) => i % 4 === 0)).toEqual([255, 0, 128]);
});
it('rejects unsupported predictors and predictor tags on uncompressed input', async () => {
  const file = fixture('tiff-predict-gray.tif'),
    view = new DataView(file.buffer),
    le = file[0] === 0x49,
    offset = view.getUint32(4, le),
    n = view.getUint16(offset, le);
  let predict = -1,
    compression = -1;
  for (let i = 0; i < n; i++) {
    const at = offset + 2 + i * 12,
      id = view.getUint16(at, le);
    if (id === 317) predict = at;
    if (id === 259) compression = at;
  }
  expect(predict).toBeGreaterThan(0);
  const unknown = file.slice();
  new DataView(unknown.buffer).setUint16(predict + 8, 3, le);
  expect(await parseTiff(unknown, 0)).toBeUndefined();
  const uncompressed = file.slice();
  new DataView(uncompressed.buffer).setUint16(compression + 8, 1, le);
  expect(await parseTiff(uncompressed, 0)).toBeUndefined();
});
it('renders real LZW grayscale, RGB horizontal prediction, and 16-bit TIFFs', async () => {
  const gray = await parseTiff(fixture('tiff-lzw-gray.tif'), 0);
  expect([...gray!.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 64, 128, 255]);
  const rgb = await parseTiff(fixture('tiff-lzw-rgb-predict.tif'), 0);
  expect([...rgb!.rgba].slice(0, 12)).toEqual([255, 0, 0, 255, 0, 255, 0, 255, 1, 2, 3, 255]);
  const bits = await parseTiff(fixture('tiff-lzw-gray16.tif'), 0);
  expect([...bits!.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 255, 128]);
});
it('handles full LZW dictionary growth and reset without changing pixels', async () => {
  const p = await parseTiff(fixture('tiff-lzw-dictionary.tif'), 0);
  expect(new Uint8Array([...p!.rgba].filter((_, i) => i % 4 === 0))).toEqual(
    fixture('tiff-lzw-dictionary.raw'),
  );
});
it('decodes independently generated LZW horizontal-prediction strips separately', async () => {
  const p = await parseTiff(fixture('tiff-lzw-multistrip.tif'), 0);
  expect([...p!.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 64, 128, 255]);
});
it.each(['none', 'deflate', 'lzw'])(
  'renders edge-clipped tiled %s TIFF with correct row ordering',
  async (compression) => {
    const p = await parseTiff(fixture(`tiff-tiles-${compression}.tif`), 0);
    expect(p).toMatchObject({ width: 18, height: 17 });
    expect(new Uint8Array([...p!.rgba].filter((_, i) => i % 4 === 0))).toEqual(
      fixture('tiff-tiles-gray.raw'),
    );
  },
);
it('renders big-endian 16-bit tiled Deflate predictor', async () => {
  const p = await parseTiff(fixture('tiff-tiles-big16.tif'), 0);
  expect(new Uint8Array([...p!.rgba].filter((_, i) => i % 4 === 0))).toEqual(
    fixture('tiff-tiles-gray16.raw'),
  );
});
it('renders tiled RGB predictors without mixing channels or padded edges', async () => {
  const p = await parseTiff(fixture('tiff-tiles-rgb.tif'), 0);
  const rgb = Uint8Array.from([...p!.rgba].filter((_, i) => i % 4 !== 3));
  expect(rgb).toEqual(fixture('tiff-tiles-rgb.raw'));
});
it('rejects oversized, mixed-layout, overlapping and truncated tiles', async () => {
  const file = fixture('tiff-tiles-deflate.tif'),
    v = new DataView(file.buffer),
    le = file[0] === 0x49,
    ifd = v.getUint32(4, le),
    count = v.getUint16(ifd, le);
  const tags = new Map<number, number>();
  for (let i = 0; i < count; i++) {
    const at = ifd + 2 + i * 12;
    tags.set(v.getUint16(at, le), at);
  }
  const huge = file.slice();
  new DataView(huge.buffer).setUint32(tags.get(322)! + 8, 65536, le);
  expect(await parseTiff(huge, 0)).toBeUndefined();
  const overlap = file.slice(),
    ov = new DataView(overlap.buffer),
    at = ov.getUint32(tags.get(324)! + 8, le);
  ov.setUint32(at + 4, ov.getUint32(at, le), le);
  expect(await parseTiff(overlap, 0)).toBeUndefined();
  const mixed = file.slice();
  new DataView(mixed.buffer).setUint16(tags.get(325)!, 279, le);
  expect(await parseTiff(mixed, 0)).toBeUndefined();
  expect(await parseTiff(file.slice(0, -1), 0)).toBeUndefined();
});
