import { inflateTiffStrip } from './tiff-deflate';
export interface TiffPreview {
  width: number;
  height: number;
  pages: number;
  aspect: number;
  spacingKnown: boolean;
  displayReducedPrecision: boolean;
  rgba: Uint8ClampedArray<ArrayBuffer>;
}
type Field = { type: number; count: number; at: number };
const widths: Record<number, number> = {
  1: 1,
  2: 1,
  3: 2,
  4: 4,
  5: 8,
  6: 1,
  7: 1,
  8: 2,
  9: 4,
  10: 8,
  11: 4,
  12: 8,
};
export async function parseTiff(bytes: Uint8Array, page: number): Promise<TiffPreview | undefined> {
  try {
    if (
      bytes.length < 8 ||
      bytes.length > 32 * 1024 * 1024 ||
      !Number.isSafeInteger(page) ||
      page < 0
    )
      return;
    const little =
      bytes[0] === 0x49 && bytes[1] === 0x49
        ? true
        : bytes[0] === 0x4d && bytes[1] === 0x4d
          ? false
          : undefined;
    if (little === undefined) return;
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    if (view.getUint16(2, little) !== 42) return;
    const ranges: [number, number][] = [[0, 8]],
      directories: Map<number, Field>[] = [],
      visited = new Set<number>();
    let offset = view.getUint32(4, little);
    while (offset) {
      if (visited.has(offset) || directories.length >= 64 || offset + 2 > bytes.length) return;
      visited.add(offset);
      const count = view.getUint16(offset, little),
        end = offset + 2 + count * 12 + 4;
      if (count > 256 || end > bytes.length) return;
      ranges.push([offset, end]);
      const fields = new Map<number, Field>();
      for (let i = 0; i < count; i++) {
        const at = offset + 2 + i * 12,
          id = view.getUint16(at, little),
          type = view.getUint16(at + 2, little),
          n = view.getUint32(at + 4, little),
          unit = widths[type];
        if (!unit || fields.has(id)) return;
        const length = n * unit;
        if (!Number.isSafeInteger(length)) return;
        const valueAt = length <= 4 ? at + 8 : view.getUint32(at + 8, little);
        if (valueAt + length > bytes.length) return;
        if (length > 4) ranges.push([valueAt, valueAt + length]);
        fields.set(id, { type, count: n, at: valueAt });
      }
      directories.push(fields);
      offset = view.getUint32(end - 4, little);
    }
    if (page >= directories.length) return;
    const fields = directories[page]!;
    function values(id: number, fallback?: number[]): number[] | undefined {
      const f = fields.get(id);
      if (!f) return fallback;
      if (![3, 4].includes(f.type) || f.count < 1 || f.count > 4096) return;
      return Array.from({ length: f.count }, (_, i) =>
        f.type === 3 ? view.getUint16(f.at + i * 2, little) : view.getUint32(f.at + i * 4, little),
      );
    }
    function one(id: number, fallback?: number): number | undefined {
      const v = values(id, fallback === undefined ? undefined : [fallback]);
      return v?.length === 1 ? v[0] : undefined;
    }
    const width = one(256),
      height = one(257),
      bits = values(258),
      compression = one(259, 1),
      photo = one(262),
      offsets = values(273),
      counts = values(279),
      samples = one(277, 1),
      rows = one(278, 0xffffffff),
      planar = one(284, 1),
      orient = one(274, 1),
      fill = one(266, 1),
      predictor = one(317, 1),
      sampleFormat = values(339, [1]);
    if (
      !width ||
      !height ||
      !bits ||
      !offsets ||
      !counts ||
      !rows ||
      !samples ||
      !sampleFormat ||
      width * height > 2 * 1024 * 1024 ||
      !Number.isSafeInteger(width * height) ||
      ![1, 8, 32946].includes(compression ?? 0) ||
      planar !== 1 ||
      orient !== 1 ||
      fill !== 1 ||
      (predictor !== 1 && !(predictor === 2 && [8, 32946].includes(compression ?? 0))) ||
      !sampleFormat.every((v) => v === 1) ||
      ![1, samples].includes(sampleFormat.length) ||
      fields.has(338)
    )
      return;
    if (!(
      (samples === 1 &&
        [0, 1].includes(photo!) &&
        bits.length === 1 &&
        [8, 16].includes(bits[0]!)) ||
      (samples === 3 && photo === 2 && bits.length === 3 && bits.every((v) => v === 8))
    ))
      return;
    const bytesPerPixel = samples * (bits[0]! / 8),
      numStrips = Math.ceil(height / rows);
    if (numStrips > 4096 || offsets.length !== numStrips || counts.length !== numStrips) return;
    const strips: [number, number][] = [];
    for (let i = 0; i < numStrips; i++) {
      const start = offsets[i]!,
        length = counts[i]!,
        expected = width * Math.min(rows, height - i * rows) * bytesPerPixel,
        end = start + length;
      if (
        (compression === 1 && length !== expected) ||
        (compression !== 1 && !length) ||
        end > bytes.length ||
        ranges.some(([a, b]) => start < b && end > a) ||
        strips.some(([a, b]) => start < b && end > a)
      )
        return;
      strips.push([start, end]);
    }
    const xRes = fields.get(282),
      yRes = fields.get(283);
    if (!!xRes !== !!yRes) return;
    let aspect = width / height,
      spacingKnown = false;
    if (xRes && yRes) {
      if (xRes.type !== 5 || yRes.type !== 5 || xRes.count !== 1 || yRes.count !== 1) return;
      const xd = view.getUint32(xRes.at + 4, little),
        yd = view.getUint32(yRes.at + 4, little),
        xn = view.getUint32(xRes.at, little),
        yn = view.getUint32(yRes.at, little);
      if (!xd || !yd || !xn || !yn) return;
      aspect = (width * yn * xd) / (height * xn * yd);
      spacingKnown = true;
    }
    if (!Number.isFinite(aspect) || aspect < 0.001 || aspect > 1000) return;
    const rgba = new Uint8ClampedArray(width * height * 4);
    let pixel = 0;
    for (let i = 0; i < strips.length; i++) {
      const [start, end] = strips[i]!;
      const expected = width * Math.min(rows, height - i * rows) * bytesPerPixel;
      const data =
        compression === 1
          ? bytes.subarray(start, end)
          : await inflateTiffStrip(bytes.subarray(start, end), expected);
      if (!data || data.length !== expected) return;
      const stripView = new DataView(data.buffer, data.byteOffset, data.byteLength);
      if (predictor === 2) {
        const rowBytes = width * bytesPerPixel;
        for (let row = 0; row < data.length; row += rowBytes) {
          if (bits[0] === 8) {
            for (let at = row + bytesPerPixel; at < row + rowBytes; at++)
              data[at] = (data[at]! + data[at - bytesPerPixel]!) & 0xff;
          } else {
            for (let at = row + 2; at < row + rowBytes; at += 2)
              stripView.setUint16(
                at,
                (stripView.getUint16(at, little) + stripView.getUint16(at - 2, little)) & 0xffff,
                little,
              );
          }
        }
      }
      for (let at = 0; at < data.length; at += bytesPerPixel) {
        let r: number, g: number, b: number;
        if (samples === 3) {
          r = data[at]!;
          g = data[at + 1]!;
          b = data[at + 2]!;
        } else {
          r = bits[0] === 16 ? Math.round(stripView.getUint16(at, little) / 257) : data[at]!;
          if (photo === 0) r = 255 - r;
          g = r;
          b = r;
        }
        rgba[pixel * 4] = r;
        rgba[pixel * 4 + 1] = g;
        rgba[pixel * 4 + 2] = b;
        rgba[pixel * 4 + 3] = 255;
        pixel++;
      }
    }
    return {
      width,
      height,
      pages: directories.length,
      aspect,
      spacingKnown,
      displayReducedPrecision: bits[0] === 16,
      rgba,
    };
  } catch {
    return;
  }
}
