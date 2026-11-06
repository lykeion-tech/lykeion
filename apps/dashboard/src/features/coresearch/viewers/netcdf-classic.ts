export interface NetcdfVariable {
  name: string;
  type: string;
  dimensions: string[];
  shape: number[];
  values: string[];
  attributes: Record<string, string>;
  attributeTypes: Record<string, string>;
  truncated: boolean;
}
export interface NetcdfPreview {
  variables: NetcdfVariable[];
  globalAttributes: Record<string, string>;
  globalAttributeTypes: Record<string, string>;
}
const sizes = [0, 1, 1, 2, 4, 4, 8, 1, 2, 4, 8, 8],
  names = [
    '',
    'BYTE',
    'CHAR',
    'SHORT',
    'INT',
    'FLOAT',
    'DOUBLE',
    'UBYTE',
    'USHORT',
    'UINT',
    'INT64',
    'UINT64',
  ];
export function parseClassicNetcdf(bytes: Uint8Array): NetcdfPreview | undefined {
  try {
    if (
      bytes.length < 32 ||
      bytes.length > 32 * 1024 * 1024 ||
      bytes[0] !== 67 ||
      bytes[1] !== 68 ||
      bytes[2] !== 70 ||
      (bytes[3] !== 1 && bytes[3] !== 2 && bytes[3] !== 5)
    )
      throw Error();
    const version = bytes[3],
      view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    let pos = 4;
    const take = (n: number) => {
      if (!Number.isSafeInteger(n) || n < 0 || n > bytes.length - pos) throw Error();
      const p = pos;
      pos += n;
      return p;
    };
    const u32 = () => view.getUint32(take(4), false);
    const nonneg = () => {
      if (version !== 5) return u32();
      const value = view.getBigInt64(take(8), false);
      if (value < 0n || value > BigInt(Number.MAX_SAFE_INTEGER)) throw Error();
      return Number(value);
    };
    const off = () => (version === 1 ? BigInt(u32()) : view.getBigUint64(take(8), false));
    const padding = (n: number) => {
      const pad = (4 - (n % 4)) % 4,
        at = take(pad);
      for (let i = 0; i < pad; i++) if (bytes[at + i] !== 0) throw Error();
    };
    const str = () => {
      const n = nonneg();
      if (!n || n > 1024) throw Error();
      const at = take(n),
        result = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(at, at + n));
      padding(n);
      if (/[\x00-\x1f\x7f/]/.test(result)) throw Error();
      return result;
    };
    const list = (tag: number, read: () => void) => {
      const t = u32(),
        count = nonneg();
      if (t === 0) {
        if (count !== 0) throw Error();
        return;
      }
      if (t !== tag || count > 128) throw Error();
      for (let i = 0; i < count; i++) read();
    };
    const val = (at: number, type: number) => {
      let v: number | bigint;
      switch (type) {
        case 1:
          v = view.getInt8(at);
          break;
        case 2:
          return bytes[at]! >= 32 && bytes[at]! < 127
            ? String.fromCharCode(bytes[at]!)
            : `0x${bytes[at]!.toString(16).padStart(2, '0')}`;
        case 3:
          v = view.getInt16(at, false);
          break;
        case 4:
          v = view.getInt32(at, false);
          break;
        case 5:
          v = view.getFloat32(at, false);
          break;
        case 6:
          v = view.getFloat64(at, false);
          break;
        case 7:
          v = view.getUint8(at);
          break;
        case 8:
          v = view.getUint16(at, false);
          break;
        case 9:
          v = view.getUint32(at, false);
          break;
        case 10:
          v = view.getBigInt64(at, false);
          break;
        case 11:
          v = view.getBigUint64(at, false);
          break;
        default:
          throw Error();
      }
      return Object.is(v, -0) ? '-0' : String(v);
    };
    const attrs = () => {
      const attrs: Record<string, string> = Object.create(null),
        types: Record<string, string> = Object.create(null);
      list(12, () => {
        const name = str(),
          type = u32(),
          n = nonneg(),
          size = sizes[type];
        if (!size || (version !== 5 && type > 6) || n > 1024 || Object.hasOwn(attrs, name))
          throw Error();
        const length = n * size,
          at = take(length);
        padding(length);
        let value: string;
        if (type === 2) {
          value = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(at, at + length));
          if (value.length > 1024) throw Error();
        } else {
          if (n > 32) throw Error();
          value = Array.from({ length: n }, (_, i) => val(at + i * size, type)).join(', ');
        }
        attrs[name] = value;
        types[name] = names[type]!;
      });
      return { attrs, types };
    };
    const records = nonneg();
    if (records === 0xffffffff) throw Error();
    const dims: { name: string; length: number }[] = [];
    list(10, () => {
      const name = str(),
        length = nonneg();
      if (dims.some((d) => d.name === name) || (!length && dims.some((d) => d.length === 0)))
        throw Error();
      dims.push({ name, length });
    });
    const globals = attrs();
    const vars: {
      name: string;
      ids: number[];
      type: number;
      vsize: number;
      begin: bigint;
      attrs: Record<string, string>;
      types: Record<string, string>;
      record: boolean;
      slab: bigint;
      shape: number[];
    }[] = [];
    list(11, () => {
      const name = str(),
        rank = nonneg();
      if (rank > 8 || vars.some((v) => v.name === name)) throw Error();
      const ids: number[] = [];
      for (let i = 0; i < rank; i++) {
        const id = nonneg();
        if (id >= dims.length || (dims[id]!.length === 0 && i !== 0)) throw Error();
        ids.push(id);
      }
      const a = attrs(),
        type = u32(),
        vsize = nonneg(),
        begin = off(),
        size = sizes[type];
      if (!size || (version !== 5 && type > 6) || begin % 4n !== 0n) throw Error();
      const record = ids.length > 0 && dims[ids[0]!]!.length === 0;
      let slab = BigInt(size);
      for (const id of ids.slice(record ? 1 : 0)) {
        slab *= BigInt(dims[id]!.length);
        if (slab > BigInt(bytes.length)) throw Error();
      }
      const padded = (slab + 3n) & ~3n;
      if (vsize !== Number(padded) && !(record && size < 4 && vsize === Number(slab)))
        throw Error();
      const shape = ids.map((id) => dims[id]!.length || records);
      vars.push({
        name,
        ids,
        type,
        vsize,
        begin,
        attrs: a.attrs,
        types: a.types,
        record,
        slab,
        shape,
      });
    });
    const headerEnd = BigInt(pos),
      recordVars = vars.filter((v) => v.record),
      stride =
        recordVars.length === 1 && sizes[recordVars[0]!.type]! < 4
          ? recordVars[0]!.slab
          : recordVars.reduce((a, v) => a + BigInt(v.vsize), 0n);
    const fixed = vars
      .filter((v) => !v.record)
      .map((v) => [v.begin, v.begin + BigInt(v.vsize)] as const)
      .sort((a, b) => (a[0] < b[0] ? -1 : 1));
    for (let i = 0; i < fixed.length; i++) {
      const [start, end] = fixed[i]!;
      if (start < headerEnd || end > BigInt(bytes.length) || (i > 0 && start < fixed[i - 1]![1]))
        throw Error();
    }
    if (recordVars.length > 1 && recordVars.some((v) => BigInt(v.vsize) !== ((v.slab + 3n) & ~3n)))
      throw Error();
    if (recordVars.length) {
      const sorted = [...recordVars].sort((a, b) => (a.begin < b.begin ? -1 : 1)),
        start = sorted[0]!.begin;
      let expected = start;
      for (const v of sorted) {
        if (v.begin !== expected) throw Error();
        expected += BigInt(v.vsize);
      }
      if (start < headerEnd || fixed.some((r) => r[1] > start)) throw Error();
      if (
        records > 0 &&
        start +
          BigInt(records - 1) * stride +
          (recordVars.length === 1 ? recordVars[0]!.slab : stride) >
          BigInt(bytes.length)
      )
        throw Error();
    }
    const variables = vars.map((v) => {
      const count = v.record
        ? BigInt(records) * (v.slab / BigInt(sizes[v.type]!))
        : v.slab / BigInt(sizes[v.type]!);
      const sample = Number(count < 200n ? count : 200n),
        values: string[] = [];
      const perRecord = v.slab / BigInt(sizes[v.type]!);
      for (let i = 0; i < sample; i++) {
        const k = BigInt(i),
          offset = v.record
            ? v.begin + (k / perRecord) * stride + (k % perRecord) * BigInt(sizes[v.type]!)
            : v.begin + k * BigInt(sizes[v.type]!);
        if (offset + BigInt(sizes[v.type]!) > BigInt(bytes.length)) throw Error();
        values.push(val(Number(offset), v.type));
      }
      return {
        name: v.name,
        type: names[v.type]!,
        dimensions: v.ids.map((id) => dims[id]!.name),
        shape: v.shape,
        values,
        attributes: v.attrs,
        attributeTypes: v.types,
        truncated: count > BigInt(sample),
      };
    });
    return { variables, globalAttributes: globals.attrs, globalAttributeTypes: globals.types };
  } catch {
    return undefined;
  }
}
