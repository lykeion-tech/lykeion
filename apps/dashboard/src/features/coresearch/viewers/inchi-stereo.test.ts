// @vitest-environment node
import { createRequire } from 'node:module';
import { expect, it } from 'vitest';
import { inchiRecord, depictInchi, safeInchiMolfile } from './inchi';
import { safeStereoStructure, tetrahedralMolfile } from './inchi-stereo';

it('reconstructs opposite lactic acid enantiomers and keeps their stereochemistry after layout', async () => {
  const init = createRequire(import.meta.url)('@rdkit/rdkit');
  const kit = await init();
  const images: string[] = [];
  // Independent PubChem reference identifiers: CIDs 107689 and 61503.
  for (const input of [
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/t2-/m0/s1',
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/t2-/m1/s1',
  ]) {
    expect(inchiRecord(input)).toBe(input);
    const molfile = await tetrahedralMolfile(input);
    expect(molfile && safeInchiMolfile(molfile)).toBe(true);
    const svg = depictInchi(kit, molfile!, input);
    expect(svg).toContain('<svg');
    images.push(svg!);
  }
  expect(images[0]).not.toBe(images[1]);
});
it('rejects unbounded structures, non-tetrahedral entries and invalid neighbor references', async () => {
  const { structureFromInchi } = await import('./vendor/inchi/inchi-js.js');
  const structure = await structureFromInchi(
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/t2-/m0/s1',
  );
  expect(safeStereoStructure(structure.atoms, structure.stereo)).toBe(true);
  expect(safeStereoStructure(Array(201).fill(structure.atoms[0]), structure.stereo)).toBe(false);
  expect(safeStereoStructure(structure.atoms, [{ ...structure.stereo[0]!, type: 1 }])).toBe(false);
  expect(
    safeStereoStructure(structure.atoms, [{ ...structure.stereo[0]!, neighbors: [999, 0, 1, 2] }]),
  ).toBe(false);
});
it('fails closed for geometric stereo that the reconstruction helper drops', async () => {
  for (const input of [
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3+',
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3-',
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/i1+1/t2-/m0/s1',
  ]) {
    expect(inchiRecord(input)).toBeUndefined();
    expect(await tetrahedralMolfile(input)).toBeUndefined();
  }
});
