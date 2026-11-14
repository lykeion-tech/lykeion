import { describe, expect, it } from 'vitest';
import { coordinateGeometry } from './coordinate-geometry';
const gro =
  'water\n2\n    1WAT      O    1   0.100   0.200   0.300\n    1WAT      H    2   0.110   0.220   0.330\n   1.00000   1.00000   1.00000\n';
describe('coordinate geometry', () => {
  it('parses single XYZ frame with exact coordinates', () => {
    expect(coordinateGeometry('2\nwater\nO 1 2 3\nH 1.1 2.2 3.3\n', 'xyz')?.text).toContain(
      'O 1 2 3',
    );
  });
  it('converts GRO coordinates from nm to angstroms', () => {
    const v = coordinateGeometry(gro, 'gro');
    expect(v?.atoms).toBe(2);
    expect(v?.text).toContain('O 1 2 3');
  });
  it('rejects ambiguous atom names, invalid data, and multiple frames', () => {
    expect(coordinateGeometry(gro.replace('      O', '     CA'), 'gro')).toBeUndefined();
    expect(coordinateGeometry('1\nx\nC NaN 0 0\n', 'xyz')).toBeUndefined();
    expect(coordinateGeometry('1\nx\nC 0 0 0\n1\nx\nC 0 0 0\n', 'xyz')).toBeUndefined();
    expect(coordinateGeometry('1001\nx\n', 'xyz')).toBeUndefined();
  });
});
