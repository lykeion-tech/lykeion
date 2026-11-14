import { describe, expect, it } from 'vitest';
import { parseAlignment, parseTree, treeViewportWidth } from './alignment-tree';

describe('alignment and phylogenetic previews', () => {
  it('combines CLUSTAL blocks and validates aligned lengths', () => {
    expect(
      parseAlignment('CLUSTAL W\n\na ACG-\nb ATG-\n\na CC\nb GG\n', 'aln', false)?.rows,
    ).toEqual([
      { name: 'a', sequence: 'ACG-CC' },
      { name: 'b', sequence: 'ATG-GG' },
    ]);
    expect(parseAlignment('CLUSTAL W\n\na ACG\nb AT\n', 'aln', false)).toBeUndefined();
  });
  it('reads Stockholm and simple sequential PHYLIP alignments', () => {
    expect(parseAlignment('# STOCKHOLM 1.0\na ACG\nb A-G\n//\n', 'sto', false)?.rows).toHaveLength(
      2,
    );
    expect(
      parseAlignment('# STOCKHOLM 1.0\na ACG\n//\n# STOCKHOLM 1.0\nb ACT\n//\n', 'sto', false),
    ).toBeUndefined();
    expect(parseAlignment('2 4\na ACGT\nb A-GT\n', 'phy', false)?.rows).toHaveLength(2);
    expect(parseAlignment('2 4\na ACGT\n', 'phy', false)).toBeUndefined();
  });
  it('parses Newick topology and rejects malformed, truncated, and excessive trees', () => {
    const tree = parseTree("(('A B':0.1,C:0.2)inner,D);", false);
    expect(tree?.children[0]?.children.map((n) => n.name)).toEqual(['A B', 'C']);
    expect(parseTree('[&R] (A,B);', false)?.children).toHaveLength(2);
    expect(treeViewportWidth([{ x: 100, name: 'W'.repeat(200) }])).toBeGreaterThanOrEqual(3300);
    expect(parseTree('(A,B', false)).toBeUndefined();
    expect(parseTree('(A,B);', true)).toBeUndefined();
    expect(parseTree('('.repeat(70) + 'A' + ')'.repeat(70) + ';', false)).toBeUndefined();
  });
});
