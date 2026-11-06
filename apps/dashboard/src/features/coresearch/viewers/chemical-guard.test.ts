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

it('previews Cartesian mmCIF atom sites and rejects incomplete or alternate geometry', () => {
  const cif = `data_example\n_entry.id example\nloop_\n_atom_site.group_PDB\n_atom_site.type_symbol\n_atom_site.label_atom_id\n_atom_site.Cartn_x\n_atom_site.Cartn_y\n_atom_site.Cartn_z\n_atom_site.label_alt_id\n_atom_site.pdbx_PDB_model_num\nATOM N N 11.104 13.207 10.500 . 1\nATOM C CA 12.201 13.300 10.500 . 1\n#\n`;
  expect(chemicalInput(cif, 'cif', false)).toMatchObject({ atoms: 2, format: 'xyz' });
  expect(chemicalInput(cif.replace('10.500 . 1', '10.500 B 1'), 'mmcif', false)).toBeUndefined();
  expect(
    chemicalInput(cif.replace('_atom_site.Cartn_z', '_atom_site.fract_z'), 'cif', false),
  ).toBeUndefined();
  expect(chemicalInput(cif.replace('10.500 . 1', '? . 1'), 'cif', false)).toBeUndefined();
});

// Atom-site excerpt from the public RCSB PDB entry 1CRN.
it('reads real mmCIF atom-site columns without changing coordinates', () => {
  const input =
    'data_1CRN\nloop_\n_atom_site.group_PDB \n_atom_site.id \n_atom_site.type_symbol \n_atom_site.label_atom_id \n_atom_site.label_alt_id \n_atom_site.label_comp_id \n_atom_site.label_asym_id \n_atom_site.label_entity_id \n_atom_site.label_seq_id \n_atom_site.pdbx_PDB_ins_code \n_atom_site.Cartn_x \n_atom_site.Cartn_y \n_atom_site.Cartn_z \n_atom_site.occupancy \n_atom_site.B_iso_or_equiv \n_atom_site.pdbx_formal_charge \n_atom_site.auth_seq_id \n_atom_site.auth_comp_id \n_atom_site.auth_asym_id \n_atom_site.auth_atom_id \n_atom_site.pdbx_PDB_model_num \nATOM 1   N N   . THR A 1 1  ? 17.047 14.099 3.625  1.00 13.79 ? 1  THR A N   1 \nATOM 2   C CA  . THR A 1 1  ? 16.967 12.784 4.338  1.00 10.80 ? 1  THR A CA  1 \nATOM 3   C C   . THR A 1 1  ? 15.685 12.755 5.133  1.00 9.19  ? 1  THR A C   1 \n#\n';
  const result = chemicalInput(input, 'mmcif', false);
  expect(result?.atoms).toBe(3);
  expect(result?.text).toContain('N 17.047 14.099 3.625');
});
