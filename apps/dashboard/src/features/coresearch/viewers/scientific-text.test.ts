import { describe, expect, it } from 'vitest';
import { sequencePreview, scientificTable } from './scientific-text';

describe('scientific text previews', () => {
  it('summarizes FASTA records and counts GC among called bases', () => {
    const result = sequencePreview('>alpha description\nACGTNN\n>beta\nGGCC\n', 'fna', false);
    expect(result?.records).toEqual([
      { name: 'alpha description', sequence: 'ACGTNN', length: 6, gc: 50 },
      { name: 'beta', sequence: 'GGCC', length: 4, gc: 100 },
    ]);
    expect(result?.partial).toBe(false);
  });
  it('previews FASTQ quality and rejects malformed records', () => {
    const result = sequencePreview('@read1\nACGT\n+\nIIII\n', 'fastq', false);
    expect(result?.records[0]).toMatchObject({ name: 'read1', length: 4, gc: 50, quality: 40 });
    expect(sequencePreview('@read1\nACGT\n+\nII\n', 'fastq', false)).toBeUndefined();
  });
  it('does not infer nucleotide content from ambiguous FASTA extensions', () => {
    for (const ext of ['fa', 'fasta']) {
      for (const seq of ['ACGT', 'MART']) {
        expect(sequencePreview(`>record\n${seq}\n`, ext, false)?.records[0]?.gc).toBeUndefined();
      }
    }
  });
  it('rejects blank lines hiding later FASTQ content', () => {
    expect(
      sequencePreview('@r1\nACGT\n+\nIIII\n\n@r2\nACGT\n+\nIIII\n', 'fastq', false),
    ).toBeUndefined();
  });
  it('does not report DNA GC content for protein FASTA records', () => {
    const result = sequencePreview('>protein\nACGT\n', 'faa', false);
    expect(result?.records[0]?.gc).toBeUndefined();
  });
  it('does not assert complete records at truncated input boundaries', () => {
    const result = sequencePreview('>one\nACGT\n>two\nAC', 'fa', true);
    expect(result?.records.map((r) => r.name)).toEqual(['one']);
    expect(result?.partial).toBe(true);
  });
  it('uses VCF header metadata and body rather than treating metadata as data', () => {
    const table = scientificTable(
      '##fileformat=VCFv4.2\n#CHROM\tPOS\tID\tREF\tALT\n1\t3\t.\tA\tG\n',
      'vcf',
      false,
    );
    expect(table?.metadata).toEqual(['##fileformat=VCFv4.2']);
    expect(table?.header).toEqual(['CHROM', 'POS', 'ID', 'REF', 'ALT']);
    expect(table?.rows).toEqual([['1', '3', '.', 'A', 'G']]);
  });
  it('renders headerless BED/GFF and SAM metadata as columns', () => {
    expect(scientificTable('chr1\t10\t20\tgene\n', 'bed', false)?.header.slice(0, 4)).toEqual([
      'chrom',
      'start',
      'end',
      'name',
    ]);
    const sam = scientificTable(
      '@HD\tVN:1.6\nr1\t0\tchr1\t10\t60\t4M\t*\t0\t0\tACGT\tIIII\n',
      'sam',
      false,
    );
    expect(sam?.metadata).toEqual(['@HD\tVN:1.6']);
    expect(sam?.header[0]).toBe('QNAME');
    expect(sam?.rows[0]?.[0]).toBe('r1');
  });
});
