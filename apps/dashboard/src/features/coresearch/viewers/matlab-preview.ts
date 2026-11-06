export interface MatlabPreview {
  variables: {
    name: string;
    shape: number[];
    type: string;
    values: string[];
    truncated: boolean;
  }[];
}
const miSizes: Record<number, number> = {
  1: 1,
  2: 1,
  3: 2,
  4: 2,
  5: 4,
  6: 4,
  7: 4,
  9: 8,
  12: 8,
  13: 8,
};
const miNames: Record<number, string> = {
  1: 'int8',
  2: 'uint8',
  3: 'int16',
  4: 'uint16',
  5: 'int32',
  6: 'uint32',
  7: 'single',
  9: 'double',
  12: 'int64',
  13: 'uint64',
};
const classes: Record<number, string> = {
  6: 'double',
  7: 'single',
  8: 'int8',
  9: 'uint8',
  10: 'int16',
  11: 'uint16',
  12: 'int32',
  13: 'uint32',
  14: 'int64',
  15: 'uint64',
};
async function inflate(data: Uint8Array): Promise<Uint8Array | undefined> {
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    reader = new Blob([new Uint8Array(data)])
      .stream()
      .pipeThrough(new DecompressionStream('deflate'))
      .getReader();
    const chunks: Uint8Array[] = [];
    let total = 0;
    while (true) {
      const r = await reader.read();
      if (r.done) break;
      total += r.value.length;
      if (total > 8 * 1024 * 1024) {
        await reader.cancel();
        return;
      }
      chunks.push(r.value);
    }
    const output = new Uint8Array(total);
    let at = 0;
    for (const c of chunks) {
      output.set(c, at);
      at += c.length;
    }
    return output;
  } catch {
    await reader?.cancel().catch(() => {});
    return;
  }
}
export async function parseMatlab(bytes: Uint8Array): Promise<MatlabPreview | undefined> {
  try {
    if (bytes.length < 20 || bytes.length > 32 * 1024 * 1024) throw Error();
    const vars: MatlabPreview['variables'] = [];
    let expanded = 0;
    const sample = (data: Uint8Array, type: number, little: boolean, count: number) => {
      const size = miSizes[type];
      if (!size || count * size !== data.length) throw Error();
      const d = new DataView(data.buffer, data.byteOffset, data.byteLength);
      const out: string[] = [];
      for (let i = 0; i < Math.min(200, count); i++) {
        const at = i * size;
        let v: number | bigint;
        switch (type) {
          case 1:
            v = d.getInt8(at);
            break;
          case 2:
            v = d.getUint8(at);
            break;
          case 3:
            v = d.getInt16(at, little);
            break;
          case 4:
            v = d.getUint16(at, little);
            break;
          case 5:
            v = d.getInt32(at, little);
            break;
          case 6:
            v = d.getUint32(at, little);
            break;
          case 7:
            v = d.getFloat32(at, little);
            break;
          case 9:
            v = d.getFloat64(at, little);
            break;
          case 12:
            v = d.getBigInt64(at, little);
            break;
          case 13:
            v = d.getBigUint64(at, little);
            break;
          default:
            throw Error();
        }
        out.push(Object.is(v, -0) ? '-0' : String(v));
      }
      return out;
    };
    const isV5 =
      bytes.length >= 128 &&
      ((bytes[126] === 73 && bytes[127] === 77) || (bytes[126] === 77 && bytes[127] === 73));
    if (!isV5) {
      let p = 0;
      while (p < bytes.length) {
        if (vars.length >= 128 || bytes.length - p < 20) throw Error();
        const head = new DataView(bytes.buffer, bytes.byteOffset + p, 20);
        const leCode = head.getInt32(0, true),
          beCode = head.getInt32(0, false);
        const little =
          leCode >= 0 && leCode < 1000 ? true : beCode >= 1000 && beCode < 2000 ? false : undefined;
        if (little === undefined) throw Error();
        const code = little ? leCode : beCode,
          machine = Math.floor(code / 1000),
          order = Math.floor(code / 100) % 10,
          numeric = Math.floor(code / 10) % 10,
          kind = code % 10;
        if (machine !== (little ? 0 : 1) || order !== 0 || kind !== 0 || numeric > 5) throw Error();
        const rows = head.getInt32(4, little),
          cols = head.getInt32(8, little),
          imag = head.getInt32(12, little),
          nl = head.getInt32(16, little);
        if (rows < 0 || cols < 0 || imag !== 0 || nl < 2 || nl > 1025) throw Error();
        p += 20;
        if (nl > bytes.length - p || bytes[p + nl - 1] !== 0) throw Error();
        const name = new TextDecoder('utf-8', { fatal: true }).decode(
          bytes.subarray(p, p + nl - 1),
        );
        if (!name || /[\x00-\x1f]/.test(name)) throw Error();
        p += nl;
        const type = [9, 7, 5, 3, 4, 2][numeric]!;
        const count = rows * cols,
          size = miSizes[type]!;
        if (!Number.isSafeInteger(count) || count > Math.floor((bytes.length - p) / size))
          throw Error();
        const len = count * size,
          values = sample(bytes.subarray(p, p + len), type, little, count);
        p += len;
        vars.push({
          name,
          shape: [rows, cols],
          type: miNames[type]!,
          values,
          truncated: count > 200,
        });
      }
    } else {
      const little = bytes[126] === 73;
      const header = new DataView(bytes.buffer, bytes.byteOffset, 128);
      if (header.getUint16(124, little) !== 0x0100) throw Error();
      type Tag = { type: number; data: Uint8Array; next: number };
      const tag = (buf: Uint8Array, at: number, limit: number, top = false): Tag => {
        if (at < 0 || at + 8 > limit || limit > buf.length) throw Error();
        const d = new DataView(buf.buffer, buf.byteOffset, buf.byteLength),
          raw = d.getUint32(at, little);
        let type: number, len: number, start: number, next: number;
        if (raw > 0xffff) {
          type = little ? raw & 0xffff : raw >>> 16;
          len = little ? raw >>> 16 : raw & 0xffff;
          if (!type || !len || len > 4) throw Error();
          start = at + 4;
          next = at + 8;
        } else {
          type = raw;
          len = d.getUint32(at + 4, little);
          start = at + 8;
          next = start + len + (top && type === 15 ? 0 : (8 - (len % 8)) % 8);
          if (next > limit) throw Error();
        }
        if (start + len > limit) throw Error();
        return { type, data: buf.subarray(start, start + len), next };
      };
      const matrix = (payload: Uint8Array) => {
        let p = 0;
        const next = () => {
          const t = tag(payload, p, payload.length);
          p = t.next;
          return t;
        };
        const flags = next();
        if (flags.type !== 6 || flags.data.length !== 8) throw Error();
        const f = new DataView(flags.data.buffer, flags.data.byteOffset, 8),
          word = f.getUint32(0, little),
          cls = word & 255;
        if (
          !classes[cls] ||
          (word & ~0x4ff) !== 0 ||
          (word & 0xa00) !== 0 ||
          f.getUint32(4, little) !== 0
        )
          throw Error();
        const dimensions = next();
        if (
          dimensions.type !== 5 ||
          dimensions.data.length < 8 ||
          dimensions.data.length % 4 ||
          dimensions.data.length > 32
        )
          throw Error();
        const dv = new DataView(
            dimensions.data.buffer,
            dimensions.data.byteOffset,
            dimensions.data.byteLength,
          ),
          shape: number[] = [];
        let count = 1;
        for (let i = 0; i < dimensions.data.length; i += 4) {
          const n = dv.getInt32(i, little);
          if (n < 0) throw Error();
          shape.push(n);
          count *= n;
          if (!Number.isSafeInteger(count) || count > 32 * 1024 * 1024) throw Error();
        }
        const nt = next();
        if (nt.type !== 1 || !nt.data.length || nt.data.length > 1024) throw Error();
        const name = new TextDecoder('utf-8', { fatal: true }).decode(nt.data);
        if (/[\x00-\x1f]/.test(name)) throw Error();
        const data = next();
        if (p !== payload.length || !miSizes[data.type] || classes[cls] !== miNames[data.type])
          throw Error();
        const values = sample(data.data, data.type, little, count);
        return {
          name,
          shape,
          type: `${classes[cls]} (stored ${miNames[data.type]})`,
          values,
          truncated: count > 200,
        };
      };
      let p = 128;
      while (p < bytes.length) {
        if (vars.length >= 128) throw Error();
        const t = tag(bytes, p, bytes.length, true);
        p = t.next;
        if (t.type === 14) vars.push(matrix(t.data));
        else if (t.type === 15) {
          const d = await inflate(t.data);
          if (!d) throw Error();
          expanded += d.length;
          if (expanded > 32 * 1024 * 1024) throw Error();
          const inner = tag(d, 0, d.length);
          if (inner.type !== 14 || inner.next !== d.length) throw Error();
          vars.push(matrix(inner.data));
        } else throw Error();
      }
    }
    if (!vars.length || new Set(vars.map((v) => v.name)).size !== vars.length) throw Error();
    return { variables: vars };
  } catch {
    return undefined;
  }
}
