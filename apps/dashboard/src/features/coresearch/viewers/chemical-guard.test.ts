import { describe, expect, it } from 'vitest';
import { chemicalInput } from './chemical-guard';
const pdb =
  'ATOM      1  C   GLY A   1       1.000   2.000   3.000  1.00  0.00           C  \nEND\n';
const sdf =
  'Example\n  Lykeion\n\n  1  0  0  0  0  0            999 V2000\n    1.0000    2.0000    3.0000 C   0  0  0  0  0  0  0  0  0  0  0  0\nM  END\n$$$$\n';
describe('chemical file guard', () => {
  it('accepts bounded PDB and V2000 SDF geometry', () => {
    expect(chemicalInput(pdb, 'pdb', false)?.atoms).toBe(1);
    expect(chemicalInput(sdf, 'sdf', false)?.atoms).toBe(1);
  });
  it('rejects truncated, invalid and multi-model data', () => {
    expect(chemicalInput(pdb, 'pdb', true)).toBeUndefined();
    expect(chemicalInput(pdb.replace('   1.000', '     nan'), 'pdb', false)).toBeUndefined();
    expect(chemicalInput(sdf + sdf, 'sdf', false)).toBeUndefined();
    expect(
      chemicalInput('MODEL        1\n' + pdb + 'ENDMDL\nMODEL        2\n' + pdb, 'pdb', false),
    ).toBeUndefined();
  });
});
