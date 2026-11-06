import { expect, it } from 'vitest';
import { parseDicom } from './dicom-preview';
import { readFileSync } from 'node:fs';
type Entry = [number, number, string, Uint8Array];
const bytes = (...values: number[]) => new Uint8Array(values);
const us = (n: number) => bytes(n & 255, n >>> 8);
const str = (s: string) => new TextEncoder().encode(s.length % 2 ? `${s} ` : s);
const entry = (group: number, element: number, vr: string, data: Uint8Array) => {
  const long = ['OB', 'OW', 'SQ', 'UN', 'UT'].includes(vr),
    head = new Uint8Array(long ? 12 : 8),
    view = new DataView(head.buffer);
  view.setUint16(0, group, true);
  view.setUint16(2, element, true);
  head[4] = vr.charCodeAt(0);
  head[5] = vr.charCodeAt(1);
  if (long) view.setUint32(8, data.length, true);
  else view.setUint16(6, data.length, true);
  return [...head, ...data];
};
function file(
  changes: {
    bits?: number;
    stored?: number;
    signed?: number;
    photo?: string;
    pixels?: number[];
    syntax?: string;
    extra?: Entry[];
    rows?: number;
    cols?: number;
    implicitData?: boolean;
  } = {},
) {
  const bits = changes.bits ?? 8,
    stored = changes.stored ?? bits;
  const data: Entry[] = [
    [0x0002, 0x0010, 'UI', str(changes.syntax ?? '1.2.840.10008.1.2.1')],
    [0x0010, 0x0010, 'PN', str('PRIVATE PATIENT SENTINEL')],
    [0x0028, 0x0002, 'US', us(1)],
    [0x0028, 0x0004, 'CS', str(changes.photo ?? 'MONOCHROME2')],
    [0x0028, 0x0010, 'US', us(changes.rows ?? 1)],
    [0x0028, 0x0011, 'US', us(changes.cols ?? 2)],
    [0x0028, 0x0100, 'US', us(bits)],
    [0x0028, 0x0101, 'US', us(stored)],
    [0x0028, 0x0102, 'US', us(stored - 1)],
    [0x0028, 0x0103, 'US', us(changes.signed ?? 0)],
    ...(changes.extra ?? []),
    [0x7fe0, 0x0010, bits === 8 ? 'OB' : 'OW', bytes(...(changes.pixels ?? [0, 255]))],
  ];
  return new Uint8Array([
    ...new Uint8Array(128),
    68,
    73,
    67,
    77,
    ...data.flatMap((x) => {
      if (!changes.implicitData || x[0] === 0x0002) return entry(...x);
      const head = new Uint8Array(8),
        view = new DataView(head.buffer);
      view.setUint16(0, x[0], true);
      view.setUint16(2, x[1], true);
      view.setUint32(4, x[3].length, true);
      return [...head, ...x[3]];
    }),
  ]);
}
it('reads independently produced implicit-VR little-endian monochrome images without metadata', () => {
  for (const bits of [8, 16]) {
    const input = readFileSync(
      `apps/dashboard/src/features/coresearch/viewers/fixtures/dicom/implicit-mono-${bits}.dcm`,
    );
    const result = parseDicom(input, 0);
    expect(result).toMatchObject({ width: 2, height: 2, frames: 1 });
    expect([...result!.rgba]).toEqual([
      0, 0, 0, 255, 64, 64, 64, 255, 128, 128, 128, 255, 255, 255, 255, 255,
    ]);
    expect(JSON.stringify(result)).not.toMatch(/PRIVATE|SYNTHETIC/);
  }
});
it('enforces transform, sequence, pixel and length safety for implicit VR', () => {
  const options = { syntax: '1.2.840.10008.1.2', implicitData: true };
  expect(parseDicom(file(options), 0)).toMatchObject({ width: 2, height: 1 });
  expect(
    parseDicom(file({ ...options, bits: 16, stored: 12, signed: 1, pixels: [0xff, 0x0f, 1, 0] }), 0)
      ?.rgba,
  ).toEqual(new Uint8ClampedArray([0, 0, 0, 255, 255, 255, 255, 255]));
  expect(
    parseDicom(file({ ...options, extra: [[0x0028, 0x1050, 'DS', str('20')]] }), 0),
  ).toBeUndefined();
  expect(
    parseDicom(file({ ...options, extra: [[0x0008, 0x1110, 'SQ', bytes()]] }), 0),
  ).toBeUndefined();
  expect(
    parseDicom(file({ ...options, extra: [[0x0011, 0x1001, 'LO', str('private')]] }), 0),
  ).toBeUndefined();
  expect(
    parseDicom(file({ ...options, extra: [[0x0028, 0x0030, 'DS', str('1\\2')]] }), 0),
  ).toBeUndefined();
  expect(parseDicom(file({ ...options, pixels: [0, 1, 2, 3] }), 0)).toBeUndefined();
  const frames = file({
    ...options,
    extra: [[0x0028, 0x0008, 'IS', str('2')]],
    pixels: [0, 0, 200, 100],
  });
  expect([...parseDicom(frames, 1)!.rgba]).toEqual([255, 255, 255, 255, 0, 0, 0, 255]);
  expect(parseDicom(frames, 2)).toBeUndefined();
  expect(parseDicom(file({ ...options, syntax: '1.2.840.10008.1.2.4.50' }), 0)).toBeUndefined();
  const invalid = file(options);
  const at = invalid.findIndex(
    (v, i) => v === 0x10 && invalid[i + 1] === 0 && invalid[i + 2] === 0x10 && invalid[i + 3] === 0,
  );
  expect(at).toBeGreaterThan(132);
  new DataView(invalid.buffer).setUint32(at + 4, 0xffffffff, true);
  expect(parseDicom(invalid, 0)).toBeUndefined();
  expect(parseDicom(file(options).slice(0, -1), 0)).toBeUndefined();
});
it('renders unsigned native monochrome pixels without exposing patient tags', () => {
  const result = parseDicom(file(), 0);
  expect(result).toMatchObject({ width: 2, height: 1, frames: 1 });
  expect([...result!.rgba]).toEqual([0, 0, 0, 255, 255, 255, 255, 255]);
  expect(JSON.stringify(result)).not.toContain('PRIVATE PATIENT SENTINEL');
});
it('honors signed stored values and MONOCHROME1 inversion', () => {
  const result = parseDicom(
    file({ bits: 16, stored: 12, signed: 1, photo: 'MONOCHROME1', pixels: [0xff, 0x0f, 1, 0] }),
    0,
  );
  expect([...result!.rgba]).toEqual([255, 255, 255, 255, 0, 0, 0, 255]);
});
it('selects a bounded frame and supports constant images', () => {
  const multi = file({ extra: [[0x0028, 0x0008, 'IS', str('2')]], pixels: [0, 0, 200, 100] });
  expect([...parseDicom(multi, 0)!.rgba]).toEqual([128, 128, 128, 255, 128, 128, 128, 255]);
  expect([...parseDicom(multi, 1)!.rgba]).toEqual([255, 255, 255, 255, 0, 0, 0, 255]);
  expect(parseDicom(multi, 2)).toBeUndefined();
});
it('rejects nonsquare or invalid pixel geometry rather than distorting the image', () => {
  expect(parseDicom(file({ extra: [[0x0028, 0x0030, 'DS', str('0.5\\0.5')]] }), 0)).toBeDefined();
  expect(parseDicom(file({ extra: [[0x0028, 0x0034, 'IS', str('1\\1')]] }), 0)).toBeDefined();
  expect(parseDicom(file({ extra: [[0x0028, 0x0030, 'DS', str('0.5\\1')]] }), 0)).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x0028, 0x0034, 'IS', str('2\\1')]] }), 0)).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x0018, 0x1164, 'DS', str('1\\2')]] }), 0)).toBeUndefined();
  expect(
    parseDicom(
      file({ extra: [[0x0028, 0x0030, 'DS', str('9999999999999998\\9999999999999999')]] }),
      0,
    ),
  ).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x0028, 0x0030, 'DS', str('0\\0')]] }), 0)).toBeUndefined();
});
it('rejects unsupported syntax, transforms, overlays, color and malformed files', () => {
  expect(parseDicom(file({ syntax: '1.2.840.10008.1.2' }), 0)).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x0028, 0x1050, 'DS', str('20')]] }), 0)).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x6000, 0x0010, 'US', us(1)]] }), 0)).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x0028, 0x3010, 'SQ', bytes()]] }), 0)).toBeUndefined();
  expect(parseDicom(file({ extra: [[0x0008, 0x1110, 'SQ', bytes()]] }), 0)).toBeUndefined();
  expect(parseDicom(file({ photo: 'RGB' }), 0)).toBeUndefined();
  expect(parseDicom(file({ rows: 65535, cols: 65535 }), 0)).toBeUndefined();
  expect(parseDicom(file().slice(0, -1), 0)).toBeUndefined();
  expect(parseDicom(file(), 1)).toBeUndefined();
});
