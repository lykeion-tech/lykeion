import { describe, expect, it, vi } from 'vitest';
import { smilesRecord, depictSmiles, safeMoleculeSvg } from './smiles';

describe('SMILES preview', () => {
  it('accepts one bounded molecule and rejects multiple records and InChI', () => {
    expect(smilesRecord(' CCO ethanol \n')).toBe('CCO');
    expect(smilesRecord('CCO\nCCC\n')).toBeUndefined();
    expect(smilesRecord('InChI=1S/CH4/h1H4')).toBeUndefined();
    expect(smilesRecord('C'.repeat(513))).toBeUndefined();
  });
  it('deletes molecules after success and on errors', () => {
    const del = vi.fn(),
      mol = {
        is_valid: () => true,
        get_num_atoms: () => 3,
        get_svg: () => '<svg></svg>',
        delete: del,
      };
    expect(depictSmiles({ get_mol: () => mol }, 'CCO')).toEqual('<svg></svg>');
    expect(del).toHaveBeenCalledTimes(1);
    expect(
      depictSmiles(
        {
          get_mol: () => ({
            ...mol,
            get_svg: () => {
              throw Error('bad');
            },
          }),
        },
        'CCO',
      ),
    ).toBeUndefined();
    expect(del).toHaveBeenCalledTimes(2);
    expect(
      depictSmiles({ get_mol: () => ({ ...mol, get_num_atoms: () => 201 }) }, 'CCO'),
    ).toBeUndefined();
    expect(del).toHaveBeenCalledTimes(3);
  });
  it('rejects active or externally linked SVG', () => {
    expect(safeMoleculeSvg('<svg><script>alert(1)</script></svg>')).toBe(false);
    expect(safeMoleculeSvg('<svg><image href="https://example.com/a"/></svg>')).toBe(false);
  });
});
