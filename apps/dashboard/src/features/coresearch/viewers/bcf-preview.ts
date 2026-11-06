export interface BcfPreview {
  rows: string[][];
  truncated: boolean;
}
const LIMIT = 4 * 1024 * 1024;
export function parseBcf(bytes: Uint8Array, partial: boolean): BcfPreview | undefined {
  try {
    if (
      bytes.length < 9 ||
      bytes.length > LIMIT ||
      bytes[0] !== 66 ||
      bytes[1] !== 67 ||
      bytes[2] !== 70 ||
      bytes[3] !== 2 ||
      bytes[4] !== 2
    )
      return;
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const length = view.getUint32(5, true);
    if (!length || length > 1048576 || 9 + length > bytes.length || bytes[8 + length] !== 0) return;
    const header = new TextDecoder('utf-8', { fatal: true })
      .decode(bytes.subarray(9, 9 + length))
      .replace(/\0+$/, '');
    if (header.includes('\0')) return;
    const lines = header.trimEnd().split('\n');
    if (
      !lines[0]?.startsWith('##fileformat=VCF') ||
      !lines.at(-1)?.startsWith('#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO')
    )
      return;
    const names: string[] = [],
      seen = new Set<string>(),
      explicit: boolean[] = [];
    for (const line of lines) {
      if (!line.startsWith('##contig=<')) continue;
      if (line.length > 4096 || !line.endsWith('>')) return;
      const fields = line.slice(10, -1).split(/,(?=(?:[^"\\]|\\.|"(?:[^"\\]|\\.)*")*$)/);
      const ids = fields.filter((s) => s.startsWith('ID=')),
        indexes = fields.filter((s) => s.startsWith('IDX='));
      if (ids.length !== 1 || indexes.length > 1) return;
      const name = ids[0]!.slice(3),
        rawIndex = indexes[0]?.slice(4);
      if (!name || name.length > 512 || /[\x00-\x20"<>]/.test(name) || seen.has(name)) return;
      const n = rawIndex === undefined ? names.length : Number(rawIndex);
      if (
        !Number.isSafeInteger(n) ||
        n < 0 ||
        n > 9999 ||
        names[n] !== undefined ||
        (rawIndex !== undefined && !/^\d+$/.test(rawIndex))
      )
        return;
      names[n] = name;
      seen.add(name);
      explicit.push(rawIndex !== undefined);
    }
    if (explicit.some(Boolean) && !explicit.every(Boolean)) return;
    const headerFields = lines.at(-1)!.split('\t');
    if (
      headerFields.slice(0, 8).join('\t') !== '#CHROM\tPOS\tID\tREF\tALT\tQUAL\tFILTER\tINFO' ||
      (headerFields.length > 8 && headerFields[8] !== 'FORMAT')
    )
      return;
    const samples = headerFields.length;
    if (samples !== 8 && samples < 10) return;
    const sampleCount = samples === 8 ? 0 : samples - 9,
      rows: string[][] = [];
    let pos = 9 + length;
    const decoder = new TextDecoder('utf-8', { fatal: true });
    while (pos < bytes.length && rows.length < 200) {
      if (pos + 8 > bytes.length) {
        if (partial) break;
        return;
      }
      const shared = view.getUint32(pos, true),
        indiv = view.getUint32(pos + 4, true),
        start = pos + 8;
      if (shared < 24 || shared > LIMIT || indiv > LIMIT) return;
      if (start + shared + indiv > bytes.length) {
        if (partial) break;
        return;
      }
      let end = start + shared;
      const chrom = view.getInt32(start, true),
        position = view.getInt32(start + 4, true),
        rlen = view.getInt32(start + 8, true),
        qual = view.getUint32(start + 12, true),
        packed = view.getUint32(start + 16, true),
        counts = view.getUint32(start + 20, true);
      const alleles = packed >>> 16,
        nInfo = packed & 0xffff,
        nSample = counts & 0xffffff,
        nFmt = counts >>> 24;
      if (
        chrom < 0 ||
        !names[chrom] ||
        position < 0 ||
        rlen < 1 ||
        !alleles ||
        alleles > 1000 ||
        nSample !== sampleCount ||
        (!nFmt && indiv !== 0) ||
        (nFmt > 0 && indiv === 0)
      )
        return;
      let p = start + 24;
      const typed = (multiplier = 1): { type: number; len: number; at: number } => {
        if (p >= end) throw Error();
        const tag = bytes[p++]!,
          type = tag & 15;
        let n = tag >>> 4;
        if (n === 15) {
          if (p >= end) throw Error();
          const extended = bytes[p++]!,
            t = extended & 15;
          if (extended >>> 4 !== 1 || ![1, 2, 3].includes(t)) throw Error();
          const size = t === 1 ? 1 : t === 2 ? 2 : 4;
          if (p + size > end) throw Error();
          n =
            size === 1
              ? view.getInt8(p)
              : size === 2
                ? view.getInt16(p, true)
                : view.getInt32(p, true);
          p += size;
        }
        const size = ({ 0: 0, 1: 1, 2: 2, 3: 4, 5: 4, 7: 1 } as Record<number, number>)[type];
        if (
          size === undefined ||
          n < 0 ||
          n > 1048576 ||
          (type === 0 && n !== 0) ||
          n * size * multiplier > end - p
        )
          throw Error();
        const at = p;
        p += n * size * multiplier;
        return { type, len: n, at };
      };
      const readText = (empty: boolean) => {
        const t = typed();
        if (t.type !== 7 || (!empty && !t.len) || t.len > 4096) throw Error();
        const s = decoder.decode(bytes.subarray(t.at, t.at + t.len));
        if (/[\x00-\x20\x7f]/.test(s)) throw Error();
        return s || '.';
      };
      const id = readText(true),
        ref = readText(false),
        alt: string[] = [];
      for (let i = 1; i < alleles; i++) alt.push(readText(false));
      if (![0, 1, 2, 3].includes(typed().type)) return;
      for (let i = 0; i < nInfo; i++) {
        const key = typed();
        if (![1, 2, 3].includes(key.type) || key.len !== 1) return;
        typed();
      }
      if (p !== end) return;
      end += indiv;
      for (let i = 0; i < nFmt; i++) {
        const key = typed();
        if (![1, 2, 3].includes(key.type) || key.len !== 1 || !nSample) return;
        typed(nSample);
      }
      if (p !== end) return;
      const q = qual === 0x7f800001 ? '.' : String(view.getFloat32(start + 12, true));
      if (q !== '.' && !Number.isFinite(Number(q))) return;
      rows.push([names[chrom]!, String(position + 1), id, ref, alt.join(',') || '.', q]);
      pos = end;
    }
    return { rows, truncated: partial || pos < bytes.length };
  } catch {
    return;
  }
}
