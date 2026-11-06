import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
import { parseClassicNetcdf } from './netcdf-classic';
const fixture = (name: string) =>
  new Uint8Array(readFileSync(`apps/dashboard/src/features/coresearch/viewers/fixtures/${name}`));
it.each([1, 2])(
  'reads real CDF-%i dimensions, metadata, fixed and interleaved record values',
  (version) => {
    const p = parseClassicNetcdf(fixture(`classic-cdf${version}.nc`));
    expect(p?.variables.map((v) => v.name)).toEqual(['x', 'temp', 'flag']);
    expect(p?.variables.find((v) => v.name === 'temp')?.values).toEqual([
      '1',
      '2',
      '3',
      '4',
      '5',
      '6',
    ]);
    expect(p?.variables.find((v) => v.name === 'temp')?.attributes).toMatchObject({
      units: 'K',
      scale_factor: '0.5',
      _FillValue: '-999',
    });
    expect(p?.variables.find((v) => v.name === 'flag')?.values).toEqual(['7', '8']);
    expect(p?.variables.find((v) => v.name === 'x')?.values).toEqual(['-1', '0', '100']);
  },
);
it.each([1, 2])('reads unpadded single record variable in CDF-%i', (version) => {
  expect(
    parseClassicNetcdf(fixture(`cdf${version}-single-record.nc`))?.variables[0]?.values,
  ).toEqual(['1', '2', '3', '4', '5', '6']);
});
it('rejects invalid signature, truncated input and unsupported streaming count', () => {
  const f = fixture('classic-cdf1.nc');
  expect(parseClassicNetcdf(f.slice(0, -1))).toBeUndefined();
  const s = f.slice();
  s.set([255, 255, 255, 255], 4);
  expect(parseClassicNetcdf(s)).toBeUndefined();
  expect(parseClassicNetcdf(new Uint8Array([0, 1]))).toBeUndefined();
});
it('rejects forged oversized dimensions, list counts and out-of-file offsets', () => {
  const f = fixture('classic-cdf1.nc');
  const huge = f.slice();
  huge.set([0x7f, 0xff, 0xff, 0xff], 12);
  expect(parseClassicNetcdf(huge)).toBeUndefined();
  const dim = f.slice();
  const marker = [0, 0, 0, 1, 120, 0, 0, 0, 0, 0, 0, 3];
  const at = dim.findIndex((_, i) => marker.every((x, j) => dim[i + j] === x));
  expect(at).toBeGreaterThan(0);
  dim.set([0x7f, 0xff, 0xff, 0xff], at + 8);
  expect(parseClassicNetcdf(dim)).toBeUndefined();
  const fakeVersion = f.slice();
  fakeVersion[3] = 5;
  expect(parseClassicNetcdf(fakeVersion)).toBeUndefined();
});
it('rejects misaligned variable offsets and missing fixed-variable padding', () => {
  const f = fixture('fixed-padding.nc');
  expect(parseClassicNetcdf(f)).toBeDefined();
  const overlapping = f.slice();
  new DataView(overlapping.buffer).setUint32(112, 117, false);
  expect(parseClassicNetcdf(overlapping)).toBeUndefined();
  expect(parseClassicNetcdf(f.slice(0, -1))).toBeUndefined();
});
