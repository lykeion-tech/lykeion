// @vitest-environment node
import { createRequire } from 'node:module';
import { expect, it } from 'vitest';
import { inchiRecord, depictInchi, safeInchiMolfile } from './inchi';
import { tetrahedralMolfile } from './inchi-stereo';

it('preserves the InChI Trust deuterium stereochemistry example in the final drawing', async () => {
  // InChI Trust technical FAQ, isotopic stereochemistry example.
  const input = 'InChI=1S/C3H7Cl2N/c1-3(5,6)2-4/h2,6H2,1H3/t3-/m1/s1/i2D/t2-,3-';
  const kit = await createRequire(import.meta.url)('@rdkit/rdkit')();
  expect(inchiRecord(input)).toBe(input);
  const molfile = await tetrahedralMolfile(input);
  expect(molfile && safeInchiMolfile(molfile)).toBe(true);
  expect(depictInchi(kit, molfile!, input, true)).toContain('<svg');
});
it('preserves PubChem carbon-13 methane isotope in the final drawing', async () => {
  const input = 'InChI=1S/CH4/h1H4/i1+1'; // PubChem CID 105026.
  const kit = await createRequire(import.meta.url)('@rdkit/rdkit')();
  const { molfileFromInchi } = await import('./vendor/inchi/inchi-js.js');
  const result = await molfileFromInchi(input);
  expect(inchiRecord(input)).toBe(input);
  expect(depictInchi(kit, result.molfile, input, true)).toContain('<svg');
});
it('preserves PubChem D-alanine-3-13C absolute stereo and isotope', async () => {
  const input = 'InChI=1S/C3H7NO2/c1-2(4)3(5)6/h2H,4H2,1H3,(H,5,6)/t2-/m1/s1/i1+1'; // PubChem CID 12205393.
  const kit = await createRequire(import.meta.url)('@rdkit/rdkit')();
  expect(inchiRecord(input)).toBe(input);
  const molfile = await tetrahedralMolfile(input);
  expect(molfile && safeInchiMolfile(molfile)).toBe(true);
  expect(depictInchi(kit, molfile!, input, true)).toContain('<svg');
});
