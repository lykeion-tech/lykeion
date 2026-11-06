export interface DicomPreview {
  width: number;
  height: number;
  frames: number;
  rgba: Uint8ClampedArray<ArrayBuffer>;
}
const MAX_BYTES = 32 * 1024 * 1024;
const MAX_PIXELS = 4 * 1024 * 1024;
const LONG_VR = new Set(['OB', 'OD', 'OF', 'OL', 'OV', 'OW', 'SQ', 'UC', 'UN', 'UR', 'UT']);
const SHORT_VR = new Set([
  'AE',
  'AS',
  'AT',
  'CS',
  'DA',
  'DS',
  'DT',
  'FD',
  'FL',
  'IS',
  'LO',
  'LT',
  'PN',
  'SH',
  'SL',
  'SS',
  'ST',
  'TM',
  'UI',
  'UL',
  'US',
]);
const relevant = new Set([
  '0002:0010',
  '0028:0002',
  '0028:0004',
  '0028:0008',
  '0028:0010',
  '0028:0011',
  '0028:0100',
  '0028:0101',
  '0028:0102',
  '0028:0103',
  '7fe0:0010',
]);
const tag = (group: number, element: number) =>
  `${group.toString(16).padStart(4, '0')}:${element.toString(16).padStart(4, '0')}`;
export function parseDicom(bytes: Uint8Array, frame: number): DicomPreview | undefined {
  try {
    if (
      bytes.length < 132 ||
      bytes.length > MAX_BYTES ||
      !Number.isSafeInteger(frame) ||
      frame < 0 ||
      bytes[128] !== 68 ||
      bytes[129] !== 73 ||
      bytes[130] !== 67 ||
      bytes[131] !== 77
    )
      return;
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    let at = 132,
      count = 0,
      inData = false,
      pixelOffset = -1,
      pixelLength = 0,
      pixelVr = '';
    const found = new Set<string>(),
      fields = new Map<string, number | string>();
    while (at < bytes.length) {
      if (++count > 10_000 || at + 8 > bytes.length) return;
      const group = view.getUint16(at, true),
        element = view.getUint16(at + 2, true),
        key = tag(group, element);
      if (group === 0xfffe || (group >= 0x6000 && group <= 0x60ff)) return;
      if (group === 0x0002) {
        if (inData) return;
      } else inData = true;
      if (
        (group === 0x0028 &&
          ((element >= 0x1050 && element <= 0x1056) ||
            element === 0x1040 ||
            element === 0x1041 ||
            (element >= 0x3000 && element <= 0x3010) ||
            element === 0x0120 ||
            element === 0x0121 ||
            element === 0x3110 ||
            element === 0x9110)) ||
        group === 0x2050 ||
        (group === 0x0018 && element >= 0x1600 && element <= 0x1623) ||
        (group === 0x7fe0 && [0x0008, 0x0009].includes(element))
      )
        return;
      const vr = String.fromCharCode(bytes[at + 4]!, bytes[at + 5]!);
      if (vr === 'SQ') return;
      const long = LONG_VR.has(vr);
      if (!long && !SHORT_VR.has(vr)) return;
      if (long && (at + 12 > bytes.length || view.getUint16(at + 6, true) !== 0)) return;
      const header = long ? 12 : 8,
        length = long ? view.getUint32(at + 8, true) : view.getUint16(at + 6, true);
      if (length === 0xffffffff || length % 2 || at + header + length > bytes.length) return;
      const start = at + header;
      if (['0028:0030', '0028:0034', '0018:1164', '0018:2010', '3002:0011'].includes(key)) {
        if (found.has(key) || vr !== (key === '0028:0034' ? 'IS' : 'DS') || length > 64) return;
        found.add(key);
        const parts = String.fromCharCode(...bytes.subarray(start, start + length))
          .trim()
          .split('\\')
          .map((part) => part.trim());
        const validNumber =
          key === '0028:0034' ? /^\+?\d+$/ : /^\+?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/;
        if (
          parts.length !== 2 ||
          parts[0] !== parts[1] ||
          parts.some((part) => !validNumber.test(part))
        )
          return;
        const dimensions = parts.map(Number);
        if (
          dimensions.some((value) => !Number.isFinite(value) || value <= 0) ||
          dimensions[0] !== dimensions[1]
        )
          return;
      }
      if (relevant.has(key)) {
        if (found.has(key)) return;
        found.add(key);
        if (key === '7fe0:0010') {
          if (!['OB', 'OW'].includes(vr)) return;
          pixelOffset = start;
          pixelLength = length;
          pixelVr = vr;
        } else if (['0002:0010', '0028:0004', '0028:0008'].includes(key)) {
          const expected = key === '0002:0010' ? 'UI' : key === '0028:0004' ? 'CS' : 'IS';
          if (vr !== expected || length > 64) return;
          const text = String.fromCharCode(...bytes.subarray(start, start + length)).replace(
            /[\0 ]+$/g,
            '',
          );
          fields.set(key, text);
        } else {
          if (vr !== 'US' || length !== 2) return;
          fields.set(key, view.getUint16(start, true));
        }
      }
      at = start + length;
    }
    if (fields.get('0002:0010') !== '1.2.840.10008.1.2.1') return;
    const width = fields.get('0028:0011'),
      height = fields.get('0028:0010'),
      samples = fields.get('0028:0002');
    const allocated = fields.get('0028:0100'),
      stored = fields.get('0028:0101'),
      high = fields.get('0028:0102'),
      signed = fields.get('0028:0103'),
      photo = fields.get('0028:0004');
    const rawFrames = fields.get('0028:0008') ?? '1';
    if (typeof rawFrames !== 'string' || !/^[1-9]\d*$/.test(rawFrames)) return;
    const frames = Number(rawFrames);
    if (
      typeof width !== 'number' ||
      typeof height !== 'number' ||
      !width ||
      !height ||
      samples !== 1 ||
      ![8, 16].includes(allocated as number) ||
      typeof stored !== 'number' ||
      stored < 1 ||
      stored > (allocated as number) ||
      high !== stored - 1 ||
      ![0, 1].includes(signed as number) ||
      !['MONOCHROME1', 'MONOCHROME2'].includes(photo as string) ||
      !Number.isSafeInteger(frames) ||
      frames > 32 ||
      frame >= frames
    )
      return;
    const countPixels = width * height,
      sampleSize = (allocated as number) / 8,
      total = countPixels * frames;
    if (
      !Number.isSafeInteger(total) ||
      total > MAX_PIXELS ||
      pixelOffset < 0 ||
      pixelLength !== total * sampleSize + ((total * sampleSize) % 2) ||
      (allocated === 16 && pixelVr !== 'OW')
    )
      return;
    const values = new Int32Array(countPixels),
      mask = (1 << stored) - 1,
      sign = 1 << (stored - 1);
    let min = Infinity,
      max = -Infinity;
    for (let i = 0; i < countPixels; i++) {
      const pos = pixelOffset + (frame * countPixels + i) * sampleSize;
      const raw = sampleSize === 1 ? bytes[pos]! : view.getUint16(pos, true);
      if ((raw & ~mask) !== 0) return;
      const sample = signed === 1 && (raw & sign) !== 0 ? (raw & mask) - (1 << stored) : raw & mask;
      values[i] = sample;
      min = Math.min(min, sample);
      max = Math.max(max, sample);
    }
    const rgba = new Uint8ClampedArray(countPixels * 4);
    for (let i = 0; i < countPixels; i++) {
      let intensity = min === max ? 128 : Math.round(((values[i]! - min) * 255) / (max - min));
      if (photo === 'MONOCHROME1') intensity = 255 - intensity;
      rgba[i * 4] = intensity;
      rgba[i * 4 + 1] = intensity;
      rgba[i * 4 + 2] = intensity;
      rgba[i * 4 + 3] = 255;
    }
    return { width, height, frames, rgba };
  } catch {
    return;
  }
}
