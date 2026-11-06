const MAGIC = [0x93, 0x4e, 0x55, 0x4d, 0x50, 0x59];
const MAX_HEADER = 65536;
const MAX_VALUES = 100;
export interface NpyPreview {
  shape: number[];
  dtype: string;
  order: 'C' | 'Fortran';
  values: string[];
  total: number;
  sampleShape: number[];
  sampled: boolean;
  sampleIndices: number[][];
}

// Parse a small whitelist of the Python-literal header grammar, never execute it.
function headerFields(header: string) {
  let rest = header.trim();
  if (!rest.startsWith('{') || !rest.endsWith('}')) return undefined;
  rest = rest.slice(1, -1).trim();
  const fields: Record<string, string> = {};
  while (rest) {
    const item = rest.match(
      /^(['"])(descr|fortran_order|shape)\1\s*:\s*(?:(['"])([^'"\r\n]*)\3|(True|False)|(\([^()]*\)))\s*(,|$)/,
    );
    if (!item) return undefined;
    const key = item[2]!;
    if (Object.hasOwn(fields, key)) return undefined;
    fields[key] = item[4] ?? item[5] ?? item[6]!;
    rest = rest.slice(item[0].length).trim();
  }
  return Object.keys(fields).length === 3 ? fields : undefined;
}

export async function readNpy(blob: Blob, selected?: number[]): Promise<NpyPreview | undefined> {
  try {
    if (blob.size < 10) return undefined;
    const prefix = new Uint8Array(await blob.slice(0, 12).arrayBuffer());
    if (!MAGIC.every((byte, i) => prefix[i] === byte)) return undefined;
    const major = prefix[6],
      minor = prefix[7];
    if (![1, 2, 3].includes(major!) || minor !== 0) return undefined;
    const lengthBytes = major === 1 ? 2 : 4;
    if (prefix.length < 8 + lengthBytes) return undefined;
    const headerLength =
      new DataView(prefix.buffer).getUint16(8, true) +
      (lengthBytes === 4 ? new DataView(prefix.buffer).getUint16(10, true) * 65536 : 0);
    const offset = 8 + lengthBytes + headerLength;
    if (!headerLength || headerLength > MAX_HEADER || offset > blob.size) return undefined;
    const headerBytes = await blob.slice(8 + lengthBytes, offset).arrayBuffer();
    const fields = headerFields(
      new TextDecoder(major === 3 ? 'utf-8' : 'latin1', { fatal: true }).decode(headerBytes),
    );
    if (!fields || !['True', 'False'].includes(fields.fortran_order!)) return undefined;
    const type = fields.descr?.match(/^([<>|=])([iufb?])(1|2|4|8)$/);
    if (!type) return undefined;
    const order = type[1]!,
      kind = type[2]!,
      size = Number(type[3]);
    if (
      (kind === 'f' && ![4, 8].includes(size)) ||
      (['b', '?'].includes(kind) && size !== 1) ||
      (order === '|' && size !== 1)
    )
      return undefined;
    const shapeText = fields.shape!;
    if (!/^\(\s*(?:\d+\s*,\s*)*\d*\s*\)$/.test(shapeText)) return undefined;
    const pieces = shapeText
      .slice(1, -1)
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    if (pieces.length > 32 || (pieces.length === 1 && !shapeText.includes(','))) return undefined;
    const shape = pieces.map(Number);
    if (shape.some((n) => !Number.isSafeInteger(n) || n < 0)) return undefined;
    let total = 1;
    for (const dimension of shape) {
      total *= dimension;
      if (!Number.isSafeInteger(total)) return undefined;
    }
    const byteCount = total * size;
    if (!Number.isSafeInteger(byteCount) || offset + byteCount !== blob.size) return undefined;
    const fortran = fields.fortran_order === 'True';
    const nativeLittle = new Uint8Array(new Uint16Array([1]).buffer)[0] === 1;
    const little = order === '<' || order === '|' || (order === '=' && nativeLittle);
    const leadingShape = shape.slice(0, Math.max(0, shape.length - 2));
    const selection = selected ?? leadingShape.map(() => 0);
    if (
      selection.length !== leadingShape.length ||
      selection.some(
        (n, i) =>
          !Number.isSafeInteger(n) ||
          n < 0 ||
          (leadingShape[i] === 0 ? n !== 0 : n >= leadingShape[i]!),
      )
    )
      return undefined;
    const displayShape = shape.slice(leadingShape.length);
    const empty = total === 0;
    const sampleShape = empty
      ? displayShape.map(() => 0)
      : displayShape.length === 2
        ? [
            Math.min(displayShape[0]!, 10),
            Math.min(displayShape[1]!, Math.floor(MAX_VALUES / Math.min(displayShape[0]!, 10))),
          ]
        : displayShape.length === 1
          ? [Math.min(displayShape[0]!, MAX_VALUES)]
          : [];
    const sampleCount = empty ? 0 : sampleShape.reduce((a, b) => a * b, 1);
    const sampled = displayShape.some((n, i) => sampleShape[i]! < n);
    const sampleAt = (idx: number, count: number, size: number) =>
      count <= 1 ? 0 : Math.floor((idx * (size - 1)) / (count - 1));
    const sampleIndices = displayShape.map((size, axis) =>
      Array.from({ length: sampleShape[axis]! }, (_, index) =>
        sampleAt(index, sampleShape[axis]!, size),
      ),
    );
    const values: string[] = [];
    for (let index = 0; index < sampleCount; index++) {
      const coords = [...selection];
      if (displayShape.length === 2) {
        coords.push(
          sampleAt(Math.floor(index / sampleShape[1]!), sampleShape[0]!, displayShape[0]!),
        );
        coords.push(sampleAt(index % sampleShape[1]!, sampleShape[1]!, displayShape[1]!));
      } else if (displayShape.length === 1)
        coords.push(sampleAt(index, sampleShape[0]!, displayShape[0]!));
      let physical = 0,
        stride = 1;
      if (fortran) {
        for (let dim = 0; dim < shape.length; dim++) {
          physical += coords[dim]! * stride;
          stride *= shape[dim]!;
        }
      } else {
        for (let dim = shape.length - 1; dim >= 0; dim--) {
          physical += coords[dim]! * stride;
          stride *= shape[dim]!;
        }
      }
      const start = offset + physical * size;
      const data = new DataView(await blob.slice(start, start + size).arrayBuffer());
      let value: number | bigint | boolean;
      if (kind === 'f')
        value = size === 4 ? data.getFloat32(0, little) : data.getFloat64(0, little);
      else if (kind === 'b' || kind === '?') value = data.getUint8(0) !== 0;
      else if (size === 8)
        value = kind === 'i' ? data.getBigInt64(0, little) : data.getBigUint64(0, little);
      else if (size === 4)
        value = kind === 'i' ? data.getInt32(0, little) : data.getUint32(0, little);
      else if (size === 2)
        value = kind === 'i' ? data.getInt16(0, little) : data.getUint16(0, little);
      else value = kind === 'i' ? data.getInt8(0) : data.getUint8(0);
      values.push(String(value));
    }
    return {
      shape,
      dtype: fields.descr!,
      order: fortran ? 'Fortran' : 'C',
      values,
      total,
      sampleShape,
      sampled,
      sampleIndices,
    };
  } catch {
    return undefined;
  }
}
