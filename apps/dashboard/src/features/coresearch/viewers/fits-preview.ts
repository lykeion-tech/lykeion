const BLOCK = 2880;
const MAX_HEADER = BLOCK * 32;
export interface FitsPreview {
  shape: number[];
  values: string[];
  rows: number[];
  columns: number[];
  sampled: boolean;
  bitpix: number;
  scale: number;
  zero: number;
}
export async function readFits(blob: Blob, selection?: number[]): Promise<FitsPreview | undefined> {
  try {
    if (blob.size < BLOCK || blob.size % BLOCK !== 0) return undefined;
    const head = new Uint8Array(await blob.slice(0, Math.min(blob.size, MAX_HEADER)).arrayBuffer());
    const fields = new Map<string, string>();
    let end = -1;
    for (let i = 0; i + 80 <= head.length; i += 80) {
      const bytes = head.subarray(i, i + 80);
      if (bytes.some((b) => b < 32 || b > 126)) return undefined;
      const card = String.fromCharCode(...bytes),
        key = card.slice(0, 8).trim();
      if (i === 0 && key !== 'SIMPLE') return undefined;
      if (key === 'END') {
        end = i + 80;
        break;
      }
      if (!key || key === 'COMMENT' || key === 'HISTORY') continue;
      if (card[8] !== '=' || card[9] !== ' ') continue;
      if (fields.has(key)) return undefined;
      fields.set(key, card.slice(10).split('/')[0]!.trim());
    }
    if (
      end < 0 ||
      fields.get('SIMPLE') !== 'T' ||
      (fields.has('GROUPS') && fields.get('GROUPS') !== 'F')
    )
      return undefined;
    const integer = (key: string) => {
      const s = fields.get(key);
      return s && /^[+-]?\d+$/.test(s) ? Number(s) : NaN;
    };
    const real = (key: string, fallback: number) => {
      const s = fields.get(key);
      return s === undefined
        ? fallback
        : /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eEdD][+-]?\d+)?$/.test(s)
          ? Number(s.replace(/[dD]/, 'e'))
          : NaN;
    };
    const bitpix = integer('BITPIX'),
      naxis = integer('NAXIS');
    if (
      ![8, 16, 32, 64, -32, -64].includes(bitpix) ||
      !Number.isSafeInteger(naxis) ||
      naxis < 1 ||
      naxis > 8
    )
      return undefined;
    const shape = Array.from({ length: naxis }, (_, i) => integer(`NAXIS${i + 1}`));
    if (shape.some((n) => !Number.isSafeInteger(n) || n < 1)) return undefined;
    let total = 1;
    for (const dim of shape) {
      total *= dim;
      if (!Number.isSafeInteger(total)) return undefined;
    }
    const size = Math.abs(bitpix) / 8,
      offset = Math.ceil(end / BLOCK) * BLOCK,
      byteCount = total * size;
    if (
      !Number.isSafeInteger(byteCount) ||
      offset + Math.ceil(byteCount / BLOCK) * BLOCK > blob.size
    )
      return undefined;
    const scale = real('BSCALE', 1),
      zero = real('BZERO', 0);
    if (
      !Number.isFinite(scale) ||
      !Number.isFinite(zero) ||
      (Number.isInteger(scale) && !Number.isSafeInteger(scale)) ||
      (Number.isInteger(zero) && !Number.isSafeInteger(zero))
    )
      return undefined;
    const blank = fields.has('BLANK') ? integer('BLANK') : undefined;
    if (blank !== undefined && (!Number.isSafeInteger(blank) || bitpix < 0)) return undefined;
    const extras = shape.slice(2),
      selected = selection ?? extras.map(() => 0);
    if (
      selected.length !== extras.length ||
      selected.some((v, i) => !Number.isSafeInteger(v) || v < 0 || v >= extras[i]!)
    )
      return undefined;
    const cols = shape[0]!,
      rowsCount = shape[1] ?? 1;
    const axis = (length: number) => {
      const count = Math.min(10, length);
      return Array.from({ length: count }, (_, i) =>
        count === 1 ? 0 : Math.floor((i * (length - 1)) / (count - 1)),
      );
    };
    const columns = axis(cols),
      rows = axis(rowsCount),
      values: string[] = [];
    let base = 0,
      stride = cols * rowsCount;
    for (let i = 0; i < selected.length; i++) {
      base += selected[i]! * stride;
      stride *= extras[i]!;
    }
    for (const row of rows)
      for (const col of columns) {
        const start = offset + (base + row * cols + col) * size;
        const data = new DataView(await blob.slice(start, start + size).arrayBuffer());
        let raw: number;
        if (bitpix === 8) raw = data.getUint8(0);
        else if (bitpix === 16) raw = data.getInt16(0, false);
        else if (bitpix === 32) raw = data.getInt32(0, false);
        else if (bitpix === 64) {
          const big = data.getBigInt64(0, false);
          raw = Number(big);
          if (!Number.isSafeInteger(raw)) return undefined;
        } else if (bitpix === -32) raw = data.getFloat32(0, false);
        else raw = data.getFloat64(0, false);
        if ((blank !== undefined && raw === blank) || Number.isNaN(raw)) {
          values.push('blank');
          continue;
        }
        if (bitpix > 0 && Number.isInteger(scale) && !Number.isSafeInteger(raw * scale))
          return undefined;
        const value = raw * scale + zero;
        if (!Number.isFinite(value)) return undefined;
        if (
          bitpix > 0 &&
          Number.isInteger(scale) &&
          Number.isInteger(zero) &&
          !Number.isSafeInteger(value)
        )
          return undefined;
        values.push(String(value));
      }
    return {
      shape,
      values,
      rows,
      columns,
      sampled: rows.length < rowsCount || columns.length < cols,
      bitpix,
      scale,
      zero,
    };
  } catch {
    return undefined;
  }
}
