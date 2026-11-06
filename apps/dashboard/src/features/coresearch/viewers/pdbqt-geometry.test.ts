import { describe, expect, it } from 'vitest';
import { pdbqtGeometry } from './pdbqt-geometry';
// Atom records from the AutoDock Vina public 1iep_ligand.pdbqt example.
const sample = `ROOT\nATOM      1  C1  LIG L   1      15.822  54.641  12.322  1.00  0.00     0.038 A \nATOM     13  N2  LIG L   1      14.101  57.840  12.327  1.00  0.00    -0.214 NA\nATOM     25  O1  LIG L   1      14.789  52.612  15.983  1.00  0.00    -0.270 OA\nENDROOT\nTORSDOF 0\n`;
describe('PDBQT atom types', () => {
  it('maps AutoDock acceptor types to their real elements', () => {
    const parsed = pdbqtGeometry(sample);
    expect(parsed?.text).toContain('C 15.822 54.641 12.322');
    expect(parsed?.text).toContain('N 14.101 57.840 12.327');
    expect(parsed?.text).toContain('O 14.789 52.612 15.983');
  });
  it('rejects unsupported types, invalid coordinates and multiple models', () => {
    expect(pdbqtGeometry(sample.replace(' NA\n', ' ZZ\n'))).toBeUndefined();
    expect(pdbqtGeometry(sample.replace(' NA\n', ' Na\n'))).toBeUndefined();
    expect(pdbqtGeometry('MODEL 1\n' + sample + 'ENDMDL\n')).toBeTruthy();
    expect(pdbqtGeometry('MODEL 1\n' + sample)).toBeUndefined();
    expect(pdbqtGeometry(sample.replace('  14.101', '     nan'))).toBeUndefined();
    expect(
      pdbqtGeometry('MODEL 1\n' + sample + 'ENDMDL\nMODEL 2\n' + sample + 'ENDMDL\n'),
    ).toBeUndefined();
  });
});
