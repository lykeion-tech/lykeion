import { describe, expect, it } from 'vitest';
import { mol2Geometry } from './mol2-geometry';
const ethanol = `@<TRIPOS>MOLECULE\nETHANOL\n 3 2 0 0 0\nSMALL\nNO_CHARGES\n\n@<TRIPOS>ATOM\n 1 C1 1.000 2.000 3.000 C.3 1 ETH 0.000\n 2 C2 2.500 2.000 3.000 C.3 1 ETH 0.000\n 3 O1 3.500 2.000 3.000 O.3 1 ETH 0.000\n@<TRIPOS>BOND\n 1 1 2 1\n 2 2 3 1\n`;
describe('MOL2 geometry', () => {
  it('preserves explicit bonds and coordinates', () => {
    const result = mol2Geometry(ethanol);
    expect(result).toMatchObject({ atoms: 3, format: 'mol2' });
    expect(result?.text).toContain('1.000 2.000 3.000');
    expect(result?.text).toContain('2 2 3 1');
  });
  it('rejects unsupported bond types, duplicate IDs, missing bonds and multiple molecules', () => {
    expect(mol2Geometry(ethanol.replace('2 2 3 1', '2 2 3 ar'))).toBeUndefined();
    expect(mol2Geometry(ethanol.replace(' 3 O1', ' 2 O1'))).toBeUndefined();
    expect(mol2Geometry(ethanol.replace(' 2 2 3 1\n', ''))).toBeUndefined();
    expect(mol2Geometry(ethanol + ethanol)).toBeUndefined();
    expect(mol2Geometry(ethanol.replace(' 2 C2', ' 01 C2'))).toBeUndefined();
    expect(mol2Geometry(ethanol.replace('2 2 3 1', '2 1 2 1'))).toBeUndefined();
    expect(mol2Geometry(ethanol.replace('2 2 3 1', '2 2 8 1'))).toBeUndefined();
  });
});
