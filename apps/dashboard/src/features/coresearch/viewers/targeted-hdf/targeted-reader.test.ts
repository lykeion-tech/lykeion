import { readFileSync } from 'node:fs';
import { expect, it, beforeAll } from 'vitest';
import { readTargetedHdf } from './targeted-reader';
const dir = 'apps/dashboard/src/features/coresearch/viewers/';
const fixture = (name: string) => new Uint8Array(readFileSync(`${dir}fixtures/${name}`));
const wasm = new Uint8Array(readFileSync(`${dir}targeted-hdf/hdf-reader.wasm`));
beforeAll(() => {
  Object.assign(globalThis, {
    WorkerGlobalScope: function () {},
    self: { location: { href: 'https://local.invalid/' } },
  });
});
it('reads bounded root numeric samples from genuine NetCDF4 and MATLAB 7.3 files', async () => {
  const n = await readTargetedHdf(fixture('targeted-real.nc4'), 'netcdf', wasm);
  expect(n?.items).toEqual([{ name: 'a', type: 'float64', shape: [3], values: ['1', '2', '3'] }]);
  expect(n?.partial).toBe(true);
  const m = await readTargetedHdf(fixture('targeted-real-mat73.mat'), 'matlab', wasm);
  expect(m?.items).toEqual([
    { name: 'a', type: 'float64', shape: [1, 3], values: ['1', '2', '3'] },
  ]);
});
it('rejects soft/external links and omits external/virtual data', async () => {
  for (const f of ['targeted-soft.nc4', 'targeted-external.nc4'])
    expect(await readTargetedHdf(fixture(f), 'netcdf', wasm)).toBeUndefined();
  for (const f of ['targeted-storage.nc4', 'targeted-virtual.nc4'])
    expect(await readTargetedHdf(fixture(f), 'netcdf', wasm)).toMatchObject({
      items: [],
      partial: true,
    });
});
it('rejects mismatched formats and malformed header', async () => {
  expect(await readTargetedHdf(fixture('targeted-real.nc4'), 'matlab', wasm)).toBeUndefined();
  expect(await readTargetedHdf(fixture('targeted-real-mat73.mat'), 'netcdf', wasm)).toBeUndefined();
  const bad = fixture('targeted-real.nc4');
  bad[0] = 0;
  expect(await readTargetedHdf(bad, 'netcdf', wasm)).toBeUndefined();
});
it('retains uint64 precision with compressed chunks and never manufactures unwritten values', async () => {
  const c = await readTargetedHdf(fixture('targeted-compressed.nc4'), 'netcdf', wasm);
  expect(c?.items[0]?.values).toEqual(['0', '9007199254740993', '18446744073709551615']);
  expect(await readTargetedHdf(fixture('targeted-unwritten.nc4'), 'netcdf', wasm)).toMatchObject({
    items: [],
    partial: true,
  });
  expect(await readTargetedHdf(fixture('targeted-cycle.nc4'), 'netcdf', wasm)).toMatchObject({
    items: [],
    partial: true,
  });
});
it('ships a 128 MiB maximum-memory module without socket, fetch, or dynamic loader imports', () => {
  const imports = WebAssembly.Module.imports(new WebAssembly.Module(wasm));
  expect(imports.filter((i) => /socket|sock_|fetch|dlopen/i.test(i.name))).toEqual([]);
  let p = 8;
  const read = () => {
    let n = 0,
      shift = 0,
      b: number;
    do {
      b = wasm[p++]!;
      n += (b & 127) * 2 ** shift;
      shift += 7;
    } while (b & 128);
    return n;
  };
  let maximum = 0;
  while (p < wasm.length) {
    const id = wasm[p++]!,
      size = read(),
      end = p + size;
    if (id === 5) {
      expect(read()).toBe(1);
      expect(read()).toBe(1);
      read();
      maximum = read();
    }
    p = end;
  }
  expect(maximum).toBe(2048);
});
it('omits a compressed chunk larger than the native decode budget', async () => {
  expect(await readTargetedHdf(fixture('targeted-oversized.nc4'), 'netcdf', wasm)).toMatchObject({
    items: [],
    partial: true,
  });
});
