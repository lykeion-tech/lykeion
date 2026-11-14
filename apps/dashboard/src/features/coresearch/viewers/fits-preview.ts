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
interface FitsHdu {
  index: number;
  label: string;
  supported: boolean;
  shape: number[];
  bitpix: number;
  offset: number;
  fields: Map<string, string>;
}
function fitsInteger(fields: Map<string, string>, key: string) {
  const value = fields.get(key);
  return value && /^[+-]?\d+$/.test(value) ? Number(value) : NaN;
}
function fitsReal(fields: Map<string, string>, key: string, fallback: number) {
  const value = fields.get(key);
  return value === undefined
    ? fallback
    : /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eEdD][+-]?\d+)?$/.test(value)
      ? Number(value.replace(/[dD]/, 'e'))
      : NaN;
}
export async function listFitsHdus(blob: Blob): Promise<FitsHdu[] | undefined> {
  try {
    if (blob.size < BLOCK || blob.size % BLOCK !== 0) return undefined;
    const hdus: FitsHdu[] = [];
    let cursor = 0;
    while (cursor < blob.size) {
      if (hdus.length >= 64) return undefined;
      const head = new Uint8Array(
        await blob.slice(cursor, Math.min(blob.size, cursor + MAX_HEADER)).arrayBuffer(),
      );
      const fields = new Map<string, string>();
      let end = -1;
      for (let i = 0; i + 80 <= head.length; i += 80) {
        const bytes = head.subarray(i, i + 80);
        if (bytes.some((b) => b < 32 || b > 126)) return undefined;
        const card = String.fromCharCode(...bytes),
          key = card.slice(0, 8).trim();
        if (i === 0 && key !== (hdus.length ? 'XTENSION' : 'SIMPLE')) return undefined;
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
        (!hdus.length && fields.get('SIMPLE') !== 'T') ||
        (fields.has('GROUPS') && fields.get('GROUPS') !== 'F')
      )
        return undefined;
      const kind = hdus.length
        ? fields
            .get('XTENSION')
            ?.match(/^'([^']+)'$/)?.[1]
            ?.trim()
        : 'PRIMARY';
      if (!kind || !['PRIMARY', 'IMAGE', 'TABLE', 'BINTABLE'].includes(kind)) return undefined;
      const bitpix = fitsInteger(fields, 'BITPIX'),
        naxis = fitsInteger(fields, 'NAXIS');
      if (
        ![8, 16, 32, 64, -32, -64].includes(bitpix) ||
        !Number.isSafeInteger(naxis) ||
        naxis < 0 ||
        naxis > 8
      )
        return undefined;
      const shape = Array.from({ length: naxis }, (_, i) => fitsInteger(fields, `NAXIS${i + 1}`));
      if (shape.some((n) => !Number.isSafeInteger(n) || n < 0)) return undefined;
      let total = naxis ? 1 : 0;
      for (const n of shape) {
        total *= n;
        if (!Number.isSafeInteger(total)) return undefined;
      }
      const pcount = hdus.length ? fitsInteger(fields, 'PCOUNT') : 0,
        gcount = hdus.length ? fitsInteger(fields, 'GCOUNT') : 1;
      if (
        !Number.isSafeInteger(pcount) ||
        pcount < 0 ||
        !Number.isSafeInteger(gcount) ||
        gcount < 1
      )
        return undefined;
      if (kind === 'TABLE' || kind === 'BINTABLE') {
        if (bitpix !== 8 || naxis !== 2 || gcount !== 1) return undefined;
        if (kind === 'TABLE' && (pcount !== 0 || fields.has('THEAP'))) return undefined;
        if (fields.has('THEAP')) {
          const heap = fitsInteger(fields, 'THEAP');
          if (!Number.isSafeInteger(heap) || heap < total || heap > total + pcount)
            return undefined;
        }
      }
      const byteCount = ((total + pcount) * gcount * Math.abs(bitpix)) / 8;
      const offset = cursor + Math.ceil(end / BLOCK) * BLOCK;
      const next = offset + Math.ceil(byteCount / BLOCK) * BLOCK;
      if (
        !Number.isSafeInteger(byteCount) ||
        !Number.isSafeInteger(next) ||
        next > blob.size ||
        next <= cursor
      )
        return undefined;
      const name = fields
        .get('EXTNAME')
        ?.match(/^'([^']*)'$/)?.[1]
        ?.trim()
        .slice(0, 80);
      const supported =
        (kind === 'PRIMARY' || kind === 'IMAGE') &&
        shape.length > 0 &&
        shape.every((n) => n > 0) &&
        pcount === 0 &&
        gcount === 1 &&
        fields.get('ZIMAGE') !== 'T';
      hdus.push({
        index: hdus.length,
        label: hdus.length ? name || `${kind} extension` : 'Primary HDU',
        supported,
        shape,
        bitpix,
        offset,
        fields,
      });
      cursor = next;
    }
    return hdus;
  } catch {
    return undefined;
  }
}
export async function readFits(
  blob: Blob,
  selection?: number[],
  hduIndex = 0,
): Promise<FitsPreview | undefined> {
  try {
    const hdus = await listFitsHdus(blob),
      hdu = hdus?.[hduIndex];
    if (!hdu?.supported) return undefined;
    const { fields, shape, bitpix, offset } = hdu;
    const integer = (key: string) => fitsInteger(fields, key);
    const real = (key: string, fallback: number) => fitsReal(fields, key, fallback);
    const size = Math.abs(bitpix) / 8;
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
