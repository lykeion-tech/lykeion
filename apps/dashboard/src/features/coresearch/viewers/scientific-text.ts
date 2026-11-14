export interface SequenceRecord {
  name: string;
  sequence: string;
  length: number;
  gc?: number;
  quality?: number;
  protein?: boolean;
}
export interface SequencePreview {
  records: SequenceRecord[];
  partial: boolean;
}

const MAX_RECORDS = 100;
const MAX_SEQUENCE = 400;

function record(
  name: string,
  sequence: string,
  quality?: string,
  kind: 'protein' | 'nucleotide' | 'unknown' = 'unknown',
): SequenceRecord {
  const normalized = sequence.toUpperCase();
  const protein = kind === 'protein';
  const nucleotide = kind === 'nucleotide' && !/[^ACGTUNRYKMSWBDHV.\-]/.test(normalized);
  const called = normalized.match(/[ACGTU]/g)?.length ?? 0;
  const gc = normalized.match(/[GC]/g)?.length ?? 0;
  return {
    name,
    sequence: sequence.slice(0, MAX_SEQUENCE),
    length: sequence.length,
    ...(called && nucleotide ? { gc: (gc / called) * 100 } : {}),
    ...(protein ? { protein: true } : {}),
    ...(quality === undefined
      ? {}
      : { quality: [...quality].reduce((s, c) => s + c.charCodeAt(0) - 33, 0) / quality.length }),
  };
}

export function sequencePreview(
  text: string,
  extension: string,
  partial: boolean,
): SequencePreview | undefined {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const records: SequenceRecord[] = [];
  let omitted = partial;
  if (['fasta', 'fa', 'faa', 'fna', 'ffn'].includes(extension)) {
    let name = '',
      sequence = '';
    const add = () => {
      if (!name || !sequence) return false;
      if (records.length < MAX_RECORDS)
        records.push(
          record(
            name,
            sequence,
            undefined,
            extension === 'faa'
              ? 'protein'
              : ['fna', 'ffn'].includes(extension)
                ? 'nucleotide'
                : 'unknown',
          ),
        );
      else omitted = true;
      return true;
    };
    for (const line of lines) {
      if (line.startsWith('>')) {
        if (name && !add()) return undefined;
        name = line.slice(1).trim();
        sequence = '';
      } else if (line.trim()) {
        if (!name) return undefined;
        sequence += line.replace(/\s/g, '');
      }
    }
    if (!partial && name && !add()) return undefined;
  } else if (['fastq', 'fq'].includes(extension)) {
    let i = 0;
    while (i < lines.length && lines[i] !== '') {
      const name = lines[i++]!;
      if (!name.startsWith('@') || name.length < 2) return undefined;
      let sequence = '';
      while (i < lines.length && !lines[i]!.startsWith('+')) sequence += lines[i++]!.trim();
      if (i >= lines.length) {
        if (partial) break;
        return undefined;
      }
      i++;
      let quality = '';
      while (i < lines.length && quality.length < sequence.length) quality += lines[i++]!;
      if (!sequence || quality.length !== sequence.length || /[^!-~]/.test(quality)) {
        if (partial && i >= lines.length) break;
        return undefined;
      }
      if (records.length < MAX_RECORDS)
        records.push(record(name.slice(1).trim(), sequence, quality, 'nucleotide'));
      else omitted = true;
    }
    if (lines.slice(i).some((line) => line.trim())) return undefined;
  } else return undefined;
  return records.length ? { records, partial: omitted } : undefined;
}

const headers: Record<string, string[]> = {
  bed: [
    'chrom',
    'start',
    'end',
    'name',
    'score',
    'strand',
    'thickStart',
    'thickEnd',
    'itemRgb',
    'blockCount',
    'blockSizes',
    'blockStarts',
  ],
  gff: ['seqid', 'source', 'type', 'start', 'end', 'score', 'strand', 'phase', 'attributes'],
  sam: ['QNAME', 'FLAG', 'RNAME', 'POS', 'MAPQ', 'CIGAR', 'RNEXT', 'PNEXT', 'TLEN', 'SEQ', 'QUAL'],
};
export function scientificTable(text: string, extension: string, partial: boolean) {
  const kind = ['gff3', 'gtf'].includes(extension) ? 'gff' : extension;
  if (!['vcf', 'bed', 'gff', 'sam'].includes(kind)) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  if (partial) lines.pop();
  const metadata: string[] = [],
    rows: string[][] = [];
  let header = headers[kind]?.slice() ?? [];
  let truncated = partial;
  for (const line of lines) {
    if (!line) continue;
    if (kind === 'vcf' && line.startsWith('#CHROM\t')) {
      header = line.slice(1).split('\t').slice(0, 100);
    } else if (line.startsWith('#') || (kind === 'sam' && /^@(HD|SQ|RG|PG|CO)\t/.test(line))) {
      if (metadata.length < 50) metadata.push(line.slice(0, 2000));
      else truncated = true;
    } else {
      const cells = line.split('\t');
      if (cells.length > 100) truncated = true;
      if (rows.length < 200) rows.push(cells.slice(0, 100));
      else truncated = true;
    }
  }
  if (!rows.length && !metadata.length && !header.length) return undefined;
  const width = Math.min(100, Math.max(header.length, ...rows.map((row) => row.length)));
  header = Array.from({ length: width }, (_, i) => header[i] || `Column ${i + 1}`);
  return { metadata, header, rows, partial: truncated };
}
