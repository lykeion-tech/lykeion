import { readFileSync } from 'node:fs';
import { expect, it } from 'vitest';
it('ships the targeted HDF reader dependency and fixture notices as dashboard public assets', () => {
  const root = 'apps/dashboard/';
  for (const name of [
    'Emscripten',
    'HDF5',
    'PyTables-fixture',
    'h5wasm-and-HDF5',
    'libaec',
    'zlib',
  ]) {
    expect(readFileSync(`${root}public/licenses/targeted-hdf-${name}.txt`, 'utf8')).toBe(
      readFileSync(
        `${root}src/features/coresearch/viewers/targeted-hdf/licenses/${name}.txt`,
        'utf8',
      ),
    );
  }
});
