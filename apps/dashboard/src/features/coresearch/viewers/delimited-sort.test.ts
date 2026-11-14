import { describe, expect, it } from 'vitest';
import { sortRows } from './delimited-sort';
describe('delimited table sort', () => {
  it('compares decimals exactly, including large integers, negatives and exponents', () => {
    const rows = [['9007199254740993'], ['-2.01'], ['9007199254740992'], ['1e-4'], [''], ['-2.1']];
    expect(sortRows(rows, 0, 'ascending').map(r => r[0])).toEqual(['-2.1', '-2.01', '1e-4', '9007199254740992', '9007199254740993', '']);
    expect(sortRows(rows, 0, 'descending').map(r => r[0])).toEqual(['9007199254740993', '9007199254740992', '1e-4', '-2.01', '-2.1', '']);
  });
  it('sorts mixed text stably without changing values or treating literal null as empty', () => {
    const rows = [['b', '1'], ['', '2'], ['a', '3'], ['a', '4'], ['null', '5']];
    expect(sortRows(rows, 0, 'ascending').map(r => r[1])).toEqual(['3', '4', '1', '5', '2']);
    expect(rows[0]).toEqual(['b', '1']);
  });
});
