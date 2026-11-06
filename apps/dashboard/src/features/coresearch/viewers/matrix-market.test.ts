import { describe, expect, it } from 'vitest';
import { parseMatrixMarket } from './matrix-market';

describe('Matrix Market previews', () => {
  it('sums sparse entries and reflects symmetric entries', () => {
    const x = parseMatrixMarket(
      '%%MatrixMarket matrix coordinate real symmetric\n% example\n3 3 3\n1 2 2\n1 2 3\n3 3 4\n',
      false,
    );
    expect(x?.values).toEqual([0, 5, 0, 5, 0, 0, 0, 0, 4]);
  });
  it('applies skew symmetry and rejects invalid diagonals', () => {
    expect(
      parseMatrixMarket(
        '%%MatrixMarket matrix coordinate integer skew-symmetric\n2 2 1\n2 1 3\n',
        false,
      )?.values,
    ).toEqual([0, -3, 3, 0]);
    expect(
      parseMatrixMarket(
        '%%MatrixMarket matrix coordinate real skew-symmetric\n2 2 1\n1 1 1\n',
        false,
      ),
    ).toBeUndefined();
  });
  it('rejects mixed triangular halves in symmetric and skew matrices', () => {
    expect(
      parseMatrixMarket(
        '%%MatrixMarket matrix coordinate real symmetric\n2 2 2\n1 2 3\n2 1 3\n',
        false,
      ),
    ).toBeUndefined();
    expect(
      parseMatrixMarket(
        '%%MatrixMarket matrix coordinate integer skew-symmetric\n2 2 2\n2 1 3\n1 2 -3\n',
        false,
      ),
    ).toBeUndefined();
    expect(
      parseMatrixMarket(
        '%%MatrixMarket matrix coordinate real symmetric\n3 3 2\n1 2 3\n3 2 4\n',
        false,
      ),
    ).toBeUndefined();
  });
  it('reads dense column-major matrices', () => {
    expect(
      parseMatrixMarket('%%MatrixMarket matrix array real general\n2 2\n1\n3\n2\n4\n', false)
        ?.values,
    ).toEqual([1, 2, 3, 4]);
  });
  it('rejects unsupported, truncated, malformed, and incomplete inputs', () => {
    expect(
      parseMatrixMarket('%%MatrixMarket matrix coordinate complex hermitian\n2 2 0\n', false),
    ).toBeUndefined();
    expect(
      parseMatrixMarket('%%MatrixMarket matrix coordinate real general\n2 2 1\n3 1 2\n', false),
    ).toBeUndefined();
    expect(
      parseMatrixMarket('%%MatrixMarket matrix coordinate real general\n2 2 1\n', false),
    ).toBeUndefined();
    expect(
      parseMatrixMarket('%%MatrixMarket matrix coordinate real general\n2 2 0\n', true),
    ).toBeUndefined();
  });
  it('bounds sampling of huge sparse dimensions', () => {
    const x = parseMatrixMarket(
      '%%MatrixMarket matrix coordinate pattern general\n1000000 2000000 1\n1000000 2000000\n',
      false,
    );
    expect(x?.values).toHaveLength(100);
    expect(x?.rows.at(-1)).toBe(1000000);
    expect(x?.columns.at(-1)).toBe(2000000);
    expect(x?.values.at(-1)).toBe(1);
  });
});
