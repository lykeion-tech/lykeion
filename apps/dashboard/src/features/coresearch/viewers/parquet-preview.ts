import { decompressSnappyRaw } from './snappy-raw';
import { dictionaryIndexes } from './parquet-dictionary';
import { optionalLevels } from './parquet-levels';
import { readCompactStruct, type CompactValue } from './compact-thrift';
export interface ParquetPreview {
  columns: { name: string; type: string }[];
  rows: (string | null)[][];
  totalRows: string;
  truncated: boolean;
}
type Fields = Map<number, CompactValue>;
const map = (v: CompactValue | undefined): Fields => {
  if (!(v instanceof Map)) throw Error();
  return v;
};
const list = (v: CompactValue | undefined): CompactValue[] => {
  if (!Array.isArray(v)) throw Error();
  return v;
};
const integer = (v: CompactValue | undefined): bigint => {
  if (typeof v !== 'bigint') throw Error();
  return v;
};
const num = (v: CompactValue | undefined, max = Number.MAX_SAFE_INTEGER): number => {
  const x = integer(v);
  if (x < 0n || x > BigInt(max)) throw Error();
  return Number(x);
};
const decode = (v: CompactValue | undefined): string => {
  if (!(v instanceof Uint8Array) || v.length > 1024) throw Error();
  return new TextDecoder('utf-8', { fatal: true }).decode(v);
};
const crc32 = (bytes: Uint8Array) => {
  let c = 0xffffffff;
  for (const b of bytes) {
    c ^= b;
    for (let i = 0; i < 8; i++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return (c ^ 0xffffffff) >>> 0;
};
const types: Record<number, string> = {
  0: 'BOOLEAN',
  1: 'INT32',
  2: 'INT64',
  4: 'FLOAT',
  5: 'DOUBLE',
  6: 'BYTE_ARRAY',
};
export function parseParquet(bytes: Uint8Array): ParquetPreview | undefined {
  try {
    if (bytes.length < 12 || bytes.length > 32 * 1024 * 1024) throw Error();
    const magic = (i: number) =>
      bytes[i] === 80 && bytes[i + 1] === 65 && bytes[i + 2] === 82 && bytes[i + 3] === 49;
    if (!magic(0) || !magic(bytes.length - 4)) throw Error();
    const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength),
      len = dv.getUint32(bytes.length - 8, true),
      footer = bytes.length - 8 - len;
    if (len > 1024 * 1024 || footer < 4) throw Error();
    const parsed = readCompactStruct(bytes, footer, bytes.length - 8);
    if (!parsed || parsed.next !== bytes.length - 8) throw Error();
    const meta = parsed.fields;
    if (num(meta.get(1), 2) < 1 || meta.has(8) || meta.has(9)) throw Error();
    const schema = list(meta.get(2));
    if (schema.length < 2 || schema.length > 51) throw Error();
    const root = map(schema[0]);
    if (root.has(1) || num(root.get(5), 50) !== schema.length - 1) throw Error();
    const columns = schema.slice(1).map((item) => {
      const s = map(item),
        type = num(s.get(1), 6),
        name = decode(s.get(4));
      if (
        !(type in types) ||
        !name ||
        s.has(5) ||
        !s.has(3) ||
        num(s.get(3), 2) > 1 ||
        s.has(7) ||
        s.has(8)
      )
        throw Error();
      if (s.has(6) && (type !== 6 || integer(s.get(6)) !== 0n)) throw Error();
      let text = s.has(6);
      if (s.has(10)) {
        const logical = map(s.get(10));
        if (type !== 6 || logical.size !== 1 || !logical.has(1) || map(logical.get(1)).size)
          throw Error();
        text = true;
      }
      return { name, type, text, optional: num(s.get(3), 2) === 1 };
    });
    if (new Set(columns.map((c) => c.name)).size !== columns.length) throw Error();
    const total = integer(meta.get(3));
    if (total < 0n) throw Error();
    const groups = list(meta.get(4));
    if (groups.length > 128) throw Error();
    const ranges: [number, number][] = [];
    let rowCount = 0n;
    const chunks = groups.map((g) => {
      const group = map(g),
        rows = num(group.get(3), 1000000000),
        cs = list(group.get(1));
      rowCount += BigInt(rows);
      if (cs.length !== columns.length) throw Error();
      return {
        rows,
        columns: cs.map((item, i) => {
          const chunk = map(item);
          if (chunk.has(1) || chunk.has(8) || chunk.has(9)) throw Error();
          const m = map(chunk.get(3));
          if (
            num(m.get(1), 6) !== columns[i]!.type ||
            ![0, 1].includes(num(m.get(4), 100)) ||
            num(m.get(5), 1000000000) !== rows
          )
            throw Error();
          const enc = list(m.get(2));
          if (!enc.every((e) => e === 0n || e === 2n || e === 3n || e === 8n) || !enc.includes(0n))
            throw Error();
          const path = list(m.get(3));
          if (path.length !== 1 || decode(path[0]) !== columns[i]!.name) throw Error();
          const dataStart = num(m.get(9), footer),
            start = m.has(11) ? num(m.get(11), footer) : dataStart,
            size = num(m.get(7), footer);
          const codec = num(m.get(4), 1),
            uncompressedSize = num(m.get(6), 1024 * 1024 * 1024);
          if (
            (codec === 0 && uncompressedSize !== size) ||
            start < 4 ||
            size > footer - start ||
            dataStart < start ||
            dataStart >= start + size
          )
            throw Error();
          ranges.push([start, start + size]);
          return { start, dataStart, end: start + size, codec };
        }),
      };
    });
    if (rowCount !== total) throw Error();
    ranges.sort((a, b) => a[0] - b[0]);
    for (let i = 1; i < ranges.length; i++) if (ranges[i]![0] < ranges[i - 1]![1]) throw Error();
    const output: (string | null)[][] = [];
    let decompressedBytes = 0;
    for (const group of chunks) {
      const need = Math.min(group.rows, 200 - output.length);
      if (need <= 0) continue;
      const samples = group.columns.map((chunk, index) => {
        let p = chunk.start,
          seen = 0;
        const out: (string | null)[] = [];
        const column = columns[index]!;
        const page = () => {
          const header = readCompactStruct(bytes, p, Math.min(chunk.end, p + 16384));
          if (!header) throw Error();
          p = header.next;
          const h = header.fields,
            expanded = num(h.get(2), 8 * 1024 * 1024),
            length = num(h.get(3), chunk.end - p);
          if (p + length > chunk.end) throw Error();
          const compressed = bytes.subarray(p, p + length);
          p += length;
          if (h.has(4) && Number(integer(h.get(4))) >>> 0 !== crc32(compressed)) throw Error();
          decompressedBytes += expanded;
          if (decompressedBytes > 32 * 1024 * 1024) throw Error();
          const body =
            chunk.codec === 1
              ? decompressSnappyRaw(compressed, expanded, 8 * 1024 * 1024)
              : compressed;
          if (!body || body.length !== expanded) throw Error();
          return { h, body };
        };
        const plain = (body: Uint8Array, count: number): string[] => {
          let pos = 0;
          const view = new DataView(body.buffer, body.byteOffset, body.byteLength);
          const take = (nbytes: number) => {
            if (nbytes < 0 || nbytes > body.length - pos) throw Error();
            const old = pos;
            pos += nbytes;
            return old;
          };
          const values: string[] = [];
          const boolStart = column.type === 0 ? take(Math.ceil(count / 8)) : 0;
          for (let i = 0; i < count; i++) {
            let value: string;
            if (column.type === 0) value = String(!!(body[boolStart + (i >> 3)]! & (1 << (i & 7))));
            else if (column.type === 1) value = String(view.getInt32(take(4), true));
            else if (column.type === 2) value = String(view.getBigInt64(take(8), true));
            else if (column.type === 4 || column.type === 5) {
              const x =
                column.type === 4 ? view.getFloat32(take(4), true) : view.getFloat64(take(8), true);
              value = Object.is(x, -0) ? '-0' : String(x);
            } else {
              const size = view.getUint32(take(4), true);
              if (size > 1024 * 1024) throw Error();
              const at = take(size);
              value = column.text
                ? new TextDecoder('utf-8', { fatal: true }).decode(body.subarray(at, at + size))
                : `[${size} bytes]`;
              if (value.length > 512) value = value.slice(0, 512) + '…';
            }
            values.push(value);
          }
          if (pos !== body.length) throw Error();
          return values;
        };
        let dictionary: string[] | undefined;
        if (chunk.start !== chunk.dataStart) {
          const { h, body } = page();
          if (num(h.get(1), 10) !== 2 || h.has(5) || h.has(6) || h.has(8)) throw Error();
          const dictHeader = map(h.get(7)),
            count = num(dictHeader.get(1), 10000),
            encoding = num(dictHeader.get(2), 10);
          if ((encoding !== 0 && encoding !== 2) || p !== chunk.dataStart || column.type === 0)
            throw Error();
          dictionary = plain(body, count);
        }
        while (seen < need) {
          if (p < chunk.dataStart) throw Error();
          const { h, body } = page();
          if (num(h.get(1), 10) !== 0 || h.has(6) || h.has(7) || h.has(8)) throw Error();
          const ph = map(h.get(5)),
            n = num(ph.get(1), 10000),
            encoding = num(ph.get(2), 10);
          if (!n || n > group.rows - seen || (encoding !== 0 && encoding !== 2 && encoding !== 8))
            throw Error();
          let pos = 0,
            levels: boolean[];
          if (column.optional) {
            if (num(ph.get(3), 10) !== 3) throw Error();
            const decoded = optionalLevels(body, n);
            if (!decoded) throw Error();
            levels = decoded.levels;
            pos = decoded.bytes;
          } else levels = Array(n).fill(true);
          const present = levels.reduce((total, bit) => total + Number(bit), 0);
          let values: string[];
          if (encoding === 0) values = plain(body.subarray(pos), present);
          else {
            if (!dictionary) throw Error();
            const indexes = dictionaryIndexes(body.subarray(pos), present, dictionary.length);
            if (!indexes) throw Error();
            values = indexes.map((i) => dictionary![i]!);
          }
          let valueIndex = 0;
          for (const defined of levels) {
            const value = defined ? values[valueIndex++]! : null;
            if (out.length < need) out.push(value);
          }
          seen += n;
        }
        return out;
      });
      for (let i = 0; i < need; i++) output.push(samples.map((s) => s[i] ?? null));
    }
    return {
      columns: columns.map((c) => ({ name: c.name, type: types[c.type]! })),
      rows: output,
      totalRows: String(total),
      truncated: total > BigInt(output.length),
    };
  } catch {
    return undefined;
  }
}
