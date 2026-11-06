import { describe, expect, it } from 'vitest';
import { inchiRecord, depictInchi, safeInchiMolfile } from './inchi';

const validMolfile =
  'Structure #1.\n  InChIV10\n\n  1  0  0  0  0  0  0  0  0  0  1 V2000\n    0.0000    0.0000    0.0000 C   0  0  0     0  0  0  0  0  0\nM  END\n$$$$\n';

describe('InChI preview', () => {
  it('accepts a bounded single standard identifier and rejects incomplete or multiple records', () => {
    expect(inchiRecord('InChI=1S/CH4/h1H4')).toBe('InChI=1S/CH4/h1H4');
    expect(inchiRecord('InChI=1S/CH4\nInChI=1S/CH4')).toBeUndefined();
    expect(inchiRecord('InChI=1S/C3H6O3/c1-2-3/t2-/m0/s1')).toBe(
      'InChI=1S/C3H6O3/c1-2-3/t2-/m0/s1',
    );
    expect(inchiRecord('InChI=1S/' + 'C'.repeat(2050))).toBeUndefined();
  });
  it('lays out reconstructed molecules before rendering and deletes them', () => {
    const calls: string[] = [];
    const mol = {
      is_valid: () => true,
      get_num_atoms: () => 3,
      get_inchi: () => 'InChI=1S/C2H6O/c1-2-3/h3H,2H2,1H3',
      set_new_coords: () => calls.push('layout'),
      get_svg: () => {
        calls.push('svg');
        return '<svg></svg>';
      },
      delete: () => calls.push('delete'),
    };
    expect(
      depictInchi({ get_mol: () => mol }, validMolfile, 'InChI=1S/C2H6O/c1-2-3/h3H,2H2,1H3'),
    ).toBe('<svg></svg>');
    expect(calls).toEqual(['layout', 'svg', 'delete']);
  });
  it('rejects structures whose reconstructed identifier lost stereochemistry', () => {
    const mol = {
      is_valid: () => true,
      get_num_atoms: () => 3,
      get_inchi: () => 'InChI=1S/C3H6O3',
      set_new_coords: () => {},
      get_svg: () => '<svg></svg>',
      delete: () => {},
    };
    expect(
      depictInchi({ get_mol: () => mol }, validMolfile, 'InChI=1S/C3H6O3/t2-'),
    ).toBeUndefined();
  });
  it('rejects stereochemistry lost during coordinate generation', () => {
    let calls = 0;
    const input = 'InChI=1S/CH4/h1H4';
    const mol = {
      is_valid: () => true,
      get_num_atoms: () => 1,
      get_inchi: () => (++calls === 1 ? input : 'different'),
      set_new_coords: () => {},
      get_svg: () => '<svg></svg>',
      delete: () => {},
    };
    expect(depictInchi({ get_mol: () => mol }, validMolfile, input)).toBeUndefined();
  });
});

describe('bundled InChI converter', () => {
  it('reconstructs real local structures and rejects malformed identifiers', async () => {
    const { molfileFromInchi } = await import('./vendor/inchi/inchi-js.js');
    for (const input of [
      'InChI=1S/CH4/h1H4',
      'InChI=1S/C2H6O/c1-2-3/h3H,2H2,1H3',
      'InChI=1S/CH4/h1H4/i1+1',
      'InChI=1S/ClH/h1H/p-1',
    ]) {
      const result = await molfileFromInchi(input);
      expect(result.returnCode).toBe(0);
      expect(result.molfile).toContain('V2000');
      expect(safeInchiMolfile(result.molfile)).toBe(true);
    }
    expect((await molfileFromInchi('InChI=1S/garbage')).returnCode).toBe(-1);
  });
});

describe('InChI structure preflight', () => {
  it('accepts a bounded V2000 molecule and rejects bad counts or trailing data', () => {
    expect(safeInchiMolfile(validMolfile)).toBe(true);
    expect(
      safeInchiMolfile(
        validMolfile.replace(
          '  1  0  0  0  0  0  0  0  0  0  1 V2000',
          '999  0  0  0  0  0  0  0  0  0  1 V2000',
        ),
      ),
    ).toBe(false);
    expect(safeInchiMolfile(validMolfile + 'garbage')).toBe(false);
    expect(safeInchiMolfile(validMolfile.replace(' V2000', ' V3000'))).toBe(false);
    expect(safeInchiMolfile(validMolfile.replace(' C   0', ' Qq  0'))).toBe(false);
  });
  it('never invokes RDKit for an unbounded or malformed structure', () => {
    let called = false;
    expect(
      depictInchi(
        {
          get_mol: () => {
            called = true;
            return null;
          },
        },
        'x'.repeat(70000),
        'InChI=1S/CH4/h1H4',
      ),
    ).toBeUndefined();
    expect(called).toBe(false);
  });
});

it('fails closed when the real converter loses stereochemistry', async () => {
  // PubChem CID 62695 (trans-2-butene), its opposite geometric isomer,
  // and opposite enantiomers of lactic acid.
  const identifiers = [
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3+',
    'InChI=1S/C4H8/c1-3-4-2/h3-4H,1-2H3/b4-3-',
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/t2-/m0/s1',
    'InChI=1S/C3H6O3/c1-2(4)3(5)6/h2,4H,1H3,(H,5,6)/t2-/m1/s1',
  ];
  const { createRequire } = await import('node:module');
  const { molfileFromInchi } = await import('./vendor/inchi/inchi-js.js');
  const init = createRequire(import.meta.url)('@rdkit/rdkit');
  const kit = await init();
  for (const input of identifiers) {
    const result = await molfileFromInchi(input);
    expect(result.returnCode).toBe(0);
    if (input.includes('/b')) expect(inchiRecord(input)).toBeUndefined();
    expect(depictInchi(kit, result.molfile, input)).toBeUndefined();
  }
});
