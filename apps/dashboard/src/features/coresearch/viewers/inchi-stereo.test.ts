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
it('does not pass geometric or isotopic stereo to the tetrahedral helper', async () => {
  for (const input of [
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3+',
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3-',
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/i1+1/t2-/m0/s1',
  ]) {
    expect(await tetrahedralMolfile(input)).toBeUndefined();
  }
});

it('reconstructs independent cis/trans 2-butene and verifies the final drawn geometry', async () => {
  const { geometricMolfileCandidates } = await import('./inchi-stereo');
  const kit = await createRequire(import.meta.url)('@rdkit/rdkit')();
  const images: string[] = [];
  // PubChem CIDs 62695 and 5287573, trans- and cis-2-butene respectively.
  for (const input of [
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3+',
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3-',
  ]) {
    expect(inchiRecord(input)).toBe(input);
    const candidates = await geometricMolfileCandidates(input);
    expect(candidates).toHaveLength(2);
    const matched = candidates.map((m) => depictInchi(kit, m, input, true)).filter(Boolean);
    expect(matched).toHaveLength(1);
    images.push(matched[0]!);
  }
  expect(images[0]).not.toBe(images[1]);
  // PubChem CID 444266, maleic acid: the selected geometry must match exactly.
  const maleic = 'InChI=1S/C4H4O4/c5-3(6)1-2-4(7)8/h1-2H,(H,5,6)(H,7,8)/b2-1-';
  const candidates = await geometricMolfileCandidates(maleic);
  expect(candidates.map((m) => depictInchi(kit, m, maleic, true)).filter(Boolean)).toHaveLength(1);
});
it('rejects geometric identifiers with multiple, mixed or isotopic stereo', async () => {
  const { geometricMolfileCandidates } = await import('./inchi-stereo');
  for (const input of [
    'InChI=1S/C6H10/c1-3-5-6-4-2/h3-6H,1-2H3/b5-3+,6-4-',
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3+/t2-/m0/s1',
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3+/i1+1',
  ]) {
    expect(inchiRecord(input)).toBeUndefined();
    expect(await geometricMolfileCandidates(input)).toEqual([]);
  }
});
