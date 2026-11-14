export type CompactValue =
  bigint | number | boolean | Uint8Array | CompactValue[] | Map<number, CompactValue>;
export function readCompactStruct(
  bytes: Uint8Array,
  start: number,
  end: number,
): { fields: Map<number, CompactValue>; next: number } | undefined {
  if (start < 0 || end > bytes.length || start > end) return undefined;
  let p = start,
    nodes = 0;
  const byte = () => {
    if (p >= end) throw Error();
    return bytes[p++]!;
  };
  const varint = (limit: number) => {
    let v = 0n;
    for (let i = 0; i < limit; i++) {
      const b = byte();
      if (i === 9 && b > 1) throw Error();
      v |= BigInt(b & 127) << BigInt(i * 7);
      if (!(b & 128)) return v;
    }
    throw Error();
  };
  const size = () => {
    const v = varint(5);
    if (v > 65536n) throw Error();
    return Number(v);
  };
  const struct = (depth: number): Map<number, CompactValue> => {
    if (depth > 24) throw Error();
    const fields = new Map<number, CompactValue>();
    let id = 0;
    for (;;) {
      const h = byte(),
        type = h & 15;
      if (!type) {
        if (h !== 0) throw Error();
        return fields;
      }
      const delta = h >> 4;
      if (delta) id += delta;
      else {
        const n = varint(3);
        const signed = (n >> 1n) ^ -(n & 1n);
        if (signed < 1n || signed > 32767n) throw Error();
        id = Number(signed);
      }
      if (fields.has(id) || fields.size >= 1024) throw Error();
      fields.set(id, value(type, depth + 1, true));
    }
  };
  const value = (type: number, depth: number, field = false): CompactValue => {
    if (++nodes > 20000 || depth > 24) throw Error();
    if (type === 1 || type === 2)
      return field
        ? type === 1
        : (() => {
            const b = byte();
            if (b !== 1 && b !== 2) throw Error();
            return b === 1;
          })();
    if (type === 3) return byte();
    if (type >= 4 && type <= 6) {
      const n = varint(type === 6 ? 10 : 5);
      const x = (n >> 1n) ^ -(n & 1n);
      if (
        (type === 4 && (x < -32768n || x > 32767n)) ||
        (type === 5 && (x < -2147483648n || x > 2147483647n))
      )
        throw Error();
      return x;
    }
    if (type === 7) {
      if (p + 8 > end) throw Error();
      const v = new DataView(bytes.buffer, bytes.byteOffset + p, 8).getFloat64(0, true);
      p += 8;
      return v;
    }
    if (type === 8) {
      const n = size();
      if (p + n > end) throw Error();
      const v = bytes.subarray(p, p + n);
      p += n;
      return v;
    }
    if (type === 9 || type === 10) {
      const h = byte();
      const n = h >> 4 === 15 ? size() : h >> 4;
      if (n > 1024 || (h & 15) === 0) throw Error();
      const a: CompactValue[] = [];
      for (let i = 0; i < n; i++) a.push(value(h & 15, depth + 1));
      return a;
    }
    if (type === 11) {
      const n = size();
      if (n > 1024) throw Error();
      if (!n) return [];
      const h = byte();
      const a: CompactValue[] = [];
      for (let i = 0; i < n; i++) {
        a.push(value(h >> 4, depth + 1), value(h & 15, depth + 1));
      }
      return a;
    }
    if (type === 12) return struct(depth + 1);
    throw Error();
  };
  try {
    const fields = struct(0);
    return { fields, next: p };
  } catch {
    return undefined;
  }
}
