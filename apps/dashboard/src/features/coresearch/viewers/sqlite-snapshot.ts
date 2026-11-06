export interface SnapshotRow {
  rowid: string;
  fields: string[];
}
export interface SnapshotTable {
  name: string;
  rows: SnapshotRow[];
  truncated: boolean;
  unavailable?: string;
}
export interface SqliteSnapshot {
  tables: SnapshotTable[];
  truncated: boolean;
  walMode: boolean;
}
const MAX_FILE = 4 * 1024 * 1024,
  MAX_PAGES = 256,
  MAX_ROWS = 25,
  MAX_TABLES = 40,
  MAX_RECORD = 65536,
  MAX_OUTPUT = 262144;
export function readSqliteSnapshot(bytes: Uint8Array): SqliteSnapshot | undefined {
  if (bytes.length < 512 || bytes.length > MAX_FILE) return undefined;
  try {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const magic = 'SQLite format 3\0';
    if ([...magic].some((c, i) => bytes[i] !== c.charCodeAt(0))) return undefined;
    const u16 = (p: number) => view.getUint16(p, false),
      u32 = (p: number) => view.getUint32(p, false);
    const rawSize = u16(16),
      pageSize = rawSize === 1 ? 65536 : rawSize,
      reserved = bytes[20]!;
    if (
      pageSize < 512 ||
      pageSize > 65536 ||
      (pageSize & (pageSize - 1)) !== 0 ||
      bytes.length % pageSize ||
      reserved > pageSize - 480 ||
      ![1, 2].includes(bytes[18]!) ||
      ![1, 2].includes(bytes[19]!)
    )
      return undefined;
    const pageCount = bytes.length / pageSize,
      usable = pageSize - reserved,
      encoding = u32(56);
    if (![1, 2, 3].includes(encoding) || u32(28) > pageCount) return undefined;
    const decoder = new TextDecoder(
      encoding === 1 ? 'utf-8' : encoding === 2 ? 'utf-16le' : 'utf-16be',
      { fatal: true },
    );
    const readVar = (start: number, end: number): { value: bigint; next: number } => {
      let v = 0n,
        p = start;
      for (let i = 0; i < 9; i++) {
        if (p >= end) throw Error('varint');
        const b = bytes[p++]!;
        if (i === 8) return { value: (v << 8n) | BigInt(b), next: p };
        v = (v << 7n) | BigInt(b & 127);
        if (!(b & 128)) return { value: v, next: p };
      }
      throw Error('varint');
    };
    const asSize = (v: bigint) => {
      if (v > BigInt(MAX_RECORD)) throw Error('size');
      return Number(v);
    };
    let outputBudget = MAX_OUTPUT;
    const record = (start: number, length: number): string[] => {
      const end = start + length;
      if (length > MAX_RECORD || end > bytes.length) throw Error('record');
      if (length > outputBudget) throw Error('output limit');
      let h = readVar(start, end),
        header = asSize(h.value),
        p = h.next;
      if (header < p - start || start + header > end) throw Error('header');
      const codes: number[] = [];
      while (p < start + header) {
        h = readVar(p, start + header);
        if (h.value > BigInt(MAX_RECORD * 2 + 13)) throw Error('serial');
        codes.push(Number(h.value));
        p = h.next;
        if (codes.length > 100) throw Error('columns');
      }
      let valueOffset = start + header;
      const result: string[] = [];
      for (const code of codes) {
        if (code === 0) {
          result.push('NULL');
          continue;
        }
        if (code === 8 || code === 9) {
          result.push(String(code - 8));
          continue;
        }
        if (code === 10 || code === 11) throw Error('serial');
        const size = code >= 12 ? Math.floor((code - 12) / 2) : [0, 1, 2, 3, 4, 6, 8, 8][code];
        if (size === undefined || size < 0 || valueOffset + size > end) throw Error('field');
        if (code === 7) {
          result.push(String(view.getFloat64(valueOffset, false)));
        } else if (code >= 12) {
          if (code % 2 === 0) result.push(`[BLOB ${size} bytes]`);
          else result.push(decoder.decode(bytes.subarray(valueOffset, valueOffset + size)));
        } else {
          let n = 0n;
          for (let i = 0; i < size; i++) n = (n << 8n) | BigInt(bytes[valueOffset + i]!);
          result.push(BigInt.asIntN(size * 8, n).toString());
        }
        valueOffset += size;
      }
      if (valueOffset !== end) throw Error('length');
      const characters = result.reduce((n, field) => n + field.length, 0);
      if (characters > outputBudget) throw Error('output limit');
      outputBudget -= characters;
      return result;
    };
    let budget = MAX_PAGES;
    const walk = (root: number, maxRows: number) => {
      const rows: SnapshotRow[] = [],
        seen = new Set<number>();
      let truncated = false,
        previous: bigint | undefined;
      const visit = (page: number, depth: number): void => {
        if (rows.length >= maxRows) {
          truncated = true;
          return;
        }
        if (
          !Number.isInteger(page) ||
          page < 1 ||
          page > pageCount ||
          seen.has(page) ||
          depth > 20 ||
          --budget < 0
        )
          throw Error('page');
        seen.add(page);
        const base = (page - 1) * pageSize,
          start = base + (page === 1 ? 100 : 0),
          end = base + usable;
        if (start + 8 > end) throw Error('page');
        const kind = bytes[start],
          interior = kind === 5;
        if (kind !== 5 && kind !== 13) throw Error('non-rowid');
        if (u16(start + 1) !== 0) throw Error('freeblocks unsupported');
        const hdr = interior ? 12 : 8,
          n = u16(start + 3),
          ptr = start + hdr;
        if (ptr + n * 2 > end) throw Error('pointer');
        const rawContent = u16(start + 5);
        if (rawContent === 0 && pageSize !== 65536) throw Error('content area');
        const content = rawContent === 0 ? 65536 : rawContent;
        if (content > usable || base + content < ptr + n * 2) throw Error('content area');
        const offsets: number[] = [],
          unique = new Set<number>();
        const extents: { start: number; end: number }[] = [];
        for (let i = 0; i < n; i++) {
          const off = u16(ptr + i * 2),
            cell = base + off;
          if (off < hdr || cell < base + content || cell >= end || unique.has(off))
            throw Error('cell');
          unique.add(off);
          offsets.push(off);
          let cursor = cell;
          if (interior) {
            if (cursor + 4 > end) throw Error('child');
            cursor = readVar(cursor + 4, end).next;
          } else {
            const payload = readVar(cursor, end),
              size = asSize(payload.value);
            cursor = readVar(payload.next, end).next;
            if (size > usable - 35 || cursor + size > end) throw Error('overflow');
            cursor += size;
          }
          extents.push({ start: cell, end: cursor });
        }
        extents.sort((a, b) => a.start - b.start);
        for (let i = 1; i < extents.length; i++)
          if (extents[i]!.start < extents[i - 1]!.end) throw Error('overlapping cells');
        for (const off of offsets) {
          if (rows.length >= maxRows) {
            truncated = true;
            break;
          }
          let pos = base + off;
          if (interior) {
            if (pos + 4 > end) throw Error('child');
            const child = u32(pos);
            pos += 4;
            const key = readVar(pos, end);
            visit(child, depth + 1);
            if (key.next > end) throw Error('key');
          } else {
            const len = readVar(pos, end),
              size = asSize(len.value);
            pos = len.next;
            const key = readVar(pos, end);
            pos = key.next;
            if (size > usable - 35 || pos + size > end) throw Error('overflow');
            const rowid = BigInt.asIntN(64, key.value);
            if (previous !== undefined && rowid <= previous) throw Error('order');
            previous = rowid;
            rows.push({ rowid: rowid.toString(), fields: record(pos, size) });
          }
        }
        if (interior && rows.length < maxRows) visit(u32(start + 8), depth + 1);
        else if (interior) truncated = true;
      };
      visit(root, 0);
      return { rows, truncated };
    };
    const schema = walk(1, MAX_TABLES + 1);
    const tables: SnapshotTable[] = [];
    const roots = new Set<number>([1]);
    for (const row of schema.rows) {
      const f = row.fields;
      if (f.length !== 5) throw Error('schema');
      if (f[0] !== 'table') continue;
      const name = f[1]!,
        root = Number(f[3]);
      if (name.length > 512 || !Number.isSafeInteger(root) || root < 0 || root > pageCount)
        throw Error('schema');
      if (root !== 0) {
        if (roots.has(root)) throw Error('duplicate root');
        roots.add(root);
      }
      if (name.startsWith('sqlite_')) continue;
      if (tables.length >= MAX_TABLES) break;
      if (root === 0) {
        tables.push({
          name,
          rows: [],
          truncated: false,
          unavailable: 'Virtual table or unsupported table',
        });
        continue;
      }
      try {
        tables.push({ name, ...walk(root, MAX_ROWS) });
      } catch (error) {
        tables.push({
          name,
          rows: [],
          truncated: false,
          unavailable:
            error instanceof Error && error.message === 'output limit'
              ? 'Preview output limit reached'
              : 'Unsupported or invalid table pages',
        });
      }
    }
    return {
      tables,
      truncated: schema.truncated || schema.rows.length > MAX_TABLES,
      walMode: bytes[18] === 2 || bytes[19] === 2,
    };
  } catch {
    return undefined;
  }
}
