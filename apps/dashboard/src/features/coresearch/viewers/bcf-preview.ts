export interface BcfPreview {
  rows: string[][];
  truncated: boolean;
  samples: string[];
  samplesTruncated: boolean;
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
    const vcfVersion = /^##fileformat=VCFv(\d+)\.(\d+)$/.exec(lines[0]!);
    const names: string[] = [],
      seen = new Set<string>(),
      explicit: boolean[] = [];
    // BCF shares one numeric dictionary across FILTER, INFO and FORMAT;
    // PASS is reserved at zero even when omitted from the header.
    const filters = new Map<number, string>([[0, 'PASS']]);
    const dictionary = new Map<number, string>([[0, 'PASS']]);
    const dictionaryNames = new Map<string, number>([['PASS', 0]]);
    const info = new Map<number, { name: string; type: string; number: string }>();
    let genotypeIndex: number | undefined;
    let dictionaryMode: 'explicit' | 'implicit' | undefined;
    for (const line of lines) {
      if (/^##(?:FILTER|INFO|FORMAT)=</.test(line)) {
        if (line.length > 4096 || !line.endsWith('>')) return;
        const fields = line
          .slice(line.indexOf('<') + 1, -1)
          .split(/,(?=(?:[^"\\]|\\.|"(?:[^"\\]|\\.)*")*$)/);
        const ids = fields.filter((s) => s.startsWith('ID='));
        const indexes = fields.filter((s) => s.startsWith('IDX='));
        if (ids.length !== 1 || indexes.length > 1) return;
        const name = ids[0]!.slice(3),
          raw = indexes[0]?.slice(4);
        if (!name || name.length > 512 || /[\x00-\x20"<>;,=]/.test(name)) return;
        if (name !== 'PASS') {
          const mode = raw === undefined ? 'implicit' : 'explicit';
          if (dictionaryMode && dictionaryMode !== mode) return;
          dictionaryMode = mode;
        }
        if (raw !== undefined && !/^\d+$/.test(raw)) return;
        const index =
          raw === undefined ? (dictionaryNames.get(name) ?? dictionary.size) : Number(raw);
        if (
          !Number.isSafeInteger(index) ||
          index > 9999 ||
          (dictionary.has(index) && dictionary.get(index) !== name) ||
          (dictionaryNames.has(name) && dictionaryNames.get(name) !== index)
        )
          return;
        dictionary.set(index, name);
        dictionaryNames.set(name, index);
        if (line.startsWith('##FILTER=<')) filters.set(index, name);
        if (line.startsWith('##FORMAT=<') && name === 'GT') {
          if (
            genotypeIndex !== undefined ||
            !fields.includes('Type=String') ||
            !fields.includes('Number=1')
          )
            return;
          genotypeIndex = index;
        }
        if (line.startsWith('##INFO=<')) {
          const types = fields.filter((s) => s.startsWith('Type='));
          const numbers = fields.filter((s) => s.startsWith('Number='));
          if (types.length !== 1 || numbers.length !== 1 || info.has(index)) return;
          const type = types[0]!.slice(5),
            number = numbers[0]!.slice(7);
          if (
            !['Integer', 'Float', 'String', 'Character', 'Flag'].includes(type) ||
            !/^(?:\d+|[ARG.])$/.test(number) ||
            (type === 'Flag' && number !== '0')
          )
            return;
          info.set(index, { name, type, number });
        }
      }
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
    const sampleNames = headerFields.slice(9, 25);
    if (
      sampleNames.some((name) => !name || name.length > 512 || /[\x00-\x20\x7f]/.test(name)) ||
      new Set(sampleNames).size !== sampleNames.length
    )
      return;
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
      const filter = typed();
      if (![0, 1, 2, 3].includes(filter.type) || filter.len > 1000) return;
      const filterValues: string[] = [];
      if (filter.type !== 0) {
        const size = filter.type === 1 ? 1 : filter.type === 2 ? 2 : 4;
        const missing = filter.type === 1 ? -128 : filter.type === 2 ? -32768 : -2147483648;
        for (let i = 0; i < filter.len; i++) {
          const at = filter.at + i * size;
          const n =
            size === 1
              ? view.getInt8(at)
              : size === 2
                ? view.getInt16(at, true)
                : view.getInt32(at, true);
          if (n === missing && filter.len === 1) break;
          const value = filters.get(n);
          if (!value || filterValues.includes(value)) return;
          filterValues.push(value);
        }
        if (filterValues.includes('PASS') && filterValues.length !== 1) return;
      }
      if (nInfo > 256) return;
      const infoValues: string[] = [];
      const seenInfo = new Set<number>();
      let infoSize = 0;
      for (let i = 0; i < nInfo; i++) {
        const key = typed();
        if (![1, 2, 3].includes(key.type) || key.len !== 1) return;
        const index =
          key.type === 1
            ? view.getInt8(key.at)
            : key.type === 2
              ? view.getInt16(key.at, true)
              : view.getInt32(key.at, true);
        const declaration = info.get(index);
        if (!declaration || seenInfo.has(index)) return;
        seenInfo.add(index);
        const value = typed();
        if (value.len > 1024) return;
        if (declaration.type === 'Flag') {
          if (value.len !== 0) return;
          infoValues.push(declaration.name);
          infoSize += declaration.name.length + 1;
          if (infoSize > 16384) return;
          continue;
        }
        const parts: string[] = [];
        if (declaration.type === 'Integer') {
          if (![1, 2, 3].includes(value.type)) return;
          const size = value.type === 1 ? 1 : value.type === 2 ? 2 : 4;
          const missing = value.type === 1 ? -128 : value.type === 2 ? -32768 : -2147483648;
          let ended = false;
          for (let j = 0; j < value.len; j++) {
            const at = value.at + j * size;
            const n =
              size === 1
                ? view.getInt8(at)
                : size === 2
                  ? view.getInt16(at, true)
                  : view.getInt32(at, true);
            if (n === missing + 1) {
              ended = true;
              continue;
            }
            if (ended) return;
            parts.push(n === missing ? '.' : String(n));
          }
        } else if (declaration.type === 'Float') {
          if (value.type !== 5) return;
          let ended = false;
          for (let j = 0; j < value.len; j++) {
            const at = value.at + j * 4,
              bits = view.getUint32(at, true);
            if (bits === 0x7f800002) {
              ended = true;
              continue;
            }
            if (ended) return;
            const n = view.getFloat32(at, true);
            if (bits !== 0x7f800001 && !Number.isFinite(n)) return;
            parts.push(bits === 0x7f800001 ? '.' : Object.is(n, -0) ? '-0' : String(n));
          }
        } else {
          if (value.type !== 7) return;
          const text = decoder.decode(bytes.subarray(value.at, value.at + value.len));
          if (/[\x00-\x20\x7f;=]/.test(text)) return;
          parts.push(...(text || '.').split(','));
        }
        if (
          declaration.type === 'Character' &&
          parts.some((part) => part !== '.' && [...part].length !== 1)
        )
          return;
        const entry = `${declaration.name}=${parts.join(',') || '.'}`;
        infoSize += entry.length + 1;
        if (infoSize > 16384) return;
        infoValues.push(entry);
      }
      if (p !== end) return;
      end += indiv;
      let genotypes: string[] | undefined;
      for (let i = 0; i < nFmt; i++) {
        const key = typed();
        if (![1, 2, 3].includes(key.type) || key.len !== 1 || !nSample) return;
        const index =
          key.type === 1
            ? view.getInt8(key.at)
            : key.type === 2
              ? view.getInt16(key.at, true)
              : view.getInt32(key.at, true);
        const value = typed(nSample);
        if (index === genotypeIndex) {
          // VCF 4.4 introduced meaningful leading phase markers. Fail closed
          // until those markers can be represented without ambiguity.
          if (
            !vcfVersion ||
            Number(vcfVersion[1]) > 4 ||
            (Number(vcfVersion[1]) === 4 && Number(vcfVersion[2]) >= 4)
          )
            return;
          if (genotypes || ![1, 2, 3].includes(value.type) || value.len < 1 || value.len > 64)
            return;
          const size = value.type === 1 ? 1 : value.type === 2 ? 2 : 4;
          const missing = value.type === 1 ? -128 : value.type === 2 ? -32768 : -2147483648;
          genotypes = [];
          for (let sample = 0; sample < sampleNames.length; sample++) {
            const pieces: string[] = [];
            let ended = false;
            for (let j = 0; j < value.len; j++) {
              const at = value.at + (sample * value.len + j) * size;
              const n =
                size === 1
                  ? view.getInt8(at)
                  : size === 2
                    ? view.getInt16(at, true)
                    : view.getInt32(at, true);
              if (n === missing + 1) {
                ended = true;
                continue;
              }
              if (ended || n < 0) return;
              const allele = (n >> 1) - 1;
              if (allele >= alleles) return;
              pieces.push(`${j ? (n & 1 ? '|' : '/') : ''}${allele < 0 ? '.' : allele}`);
            }
            if (!pieces.length) return;
            genotypes.push(pieces.join(''));
          }
        }
      }
      if (p !== end) return;
      const q = qual === 0x7f800001 ? '.' : String(view.getFloat32(start + 12, true));
      if (q !== '.' && !Number.isFinite(Number(q))) return;
      rows.push([
        names[chrom]!,
        String(position + 1),
        id,
        ref,
        alt.join(',') || '.',
        q,
        filterValues.join(';') || '.',
        infoValues.join(';') || '.',
        ...(genotypes ?? sampleNames.map(() => '—')),
      ]);
      pos = end;
    }
    return {
      rows,
      truncated: partial || pos < bytes.length,
      samples: sampleNames,
      samplesTruncated: sampleCount > sampleNames.length,
    };
  } catch {
    return;
  }
}
