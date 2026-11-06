import { decodeFitsRice } from './fits-rice';
import { listFitsHdus } from './fits-preview';
const LIMIT = 8 * 1024 * 1024;
const str = (v: string | undefined) => v?.match(/^'([^']*)'$/)?.[1]?.trim();
const integer = (v: string | undefined) => (v && /^[+-]?\d+$/.test(v) ? Number(v) : NaN);
async function unpack(input: Blob, expected: number): Promise<Uint8Array | undefined> {
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    if (input.size > LIMIT || expected < 1 || expected > LIMIT) return;
    reader = input.stream().pipeThrough(new DecompressionStream('gzip')).getReader();
    const out = new Uint8Array(expected);
    let offset = 0;
    while (true) {
      const part = await reader.read();
      if (part.done) break;
      if (part.value.length > expected - offset) {
        await reader.cancel();
        return;
      }
      out.set(part.value, offset);
      offset += part.value.length;
    }
    return offset === expected ? out : undefined;
  } catch {
    await reader?.cancel().catch(() => {});
    return;
  }
}
/** Narrow lossless GZIP_1/RICE_1 two-dimensional tiled image convention; no quantization. */
export async function inflateFitsTiles(blob: Blob): Promise<Blob | undefined> {
  try {
    if (blob.size > 32 * 1024 * 1024) return;
    const hdus = await listFitsHdus(blob);
    if (!hdus) return;
    const candidates = hdus.filter((h) => h.fields.get('ZIMAGE') === 'T');
    if (candidates.length !== 1) return;
    const h = candidates[0]!,
      f = h.fields;
    if (
      str(f.get('XTENSION')) !== 'BINTABLE' ||
      !['GZIP_1', 'RICE_1'].includes(str(f.get('ZCMPTYPE')) ?? '') ||
      f.has('ZQUANTIZ') ||
      f.has('ZSCALE') ||
      f.has('ZZERO') ||
      f.has('ZMASKCMP') ||
      (str(f.get('ZCMPTYPE')) === 'GZIP_1' && [...f.keys()].some((k) => /^Z(NAME|VAL)\d+$/.test(k)))
    )
      return;
    const bitpix = integer(f.get('ZBITPIX')),
      rank = integer(f.get('ZNAXIS')),
      width = integer(f.get('ZNAXIS1')),
      height = integer(f.get('ZNAXIS2')),
      tw = f.has('ZTILE1') ? integer(f.get('ZTILE1')) : width,
      th = f.has('ZTILE2') ? integer(f.get('ZTILE2')) : 1,
      tableWidth = integer(f.get('NAXIS1')),
      rows = integer(f.get('NAXIS2')),
      heapSize = integer(f.get('PCOUNT'));
    if (
      ![8, 16, 32, 64, -32, -64].includes(bitpix) ||
      rank !== 2 ||
      ![width, height, tw, th, rows, heapSize].every(Number.isSafeInteger) ||
      width < 1 ||
      height < 1 ||
      tw < 1 ||
      th < 1 ||
      heapSize < 0 ||
      tableWidth !== 8 ||
      integer(f.get('TFIELDS')) !== 1 ||
      str(f.get('TTYPE1')) !== 'COMPRESSED_DATA' ||
      !/^1?PB(?:\(\d+\))?$/.test(str(f.get('TFORM1')) ?? '')
    )
      return;
    if (
      (f.has('ZPCOUNT') && integer(f.get('ZPCOUNT')) !== 0) ||
      (f.has('ZGCOUNT') && integer(f.get('ZGCOUNT')) !== 1)
    )
      return;
    const codec = str(f.get('ZCMPTYPE'));
    if (codec === 'RICE_1') {
      if (![8, 16, 32].includes(bitpix)) return;
      const keys = [...f.keys()].filter((k) => /^Z(NAME|VAL)\d+$/.test(k));
      if (keys.length !== 4) return;
      const parameters = new Map<string, number>();
      for (const i of [1, 2]) {
        const name = str(f.get(`ZNAME${i}`));
        const value = integer(f.get(`ZVAL${i}`));
        if (!name || parameters.has(name) || !Number.isSafeInteger(value)) return;
        parameters.set(name, value);
      }
      if (parameters.get('BLOCKSIZE') !== 32 || parameters.get('BYTEPIX') !== bitpix / 8) return;
    }
    const size = Math.abs(bitpix) / 8,
      total = width * height * size,
      across = Math.ceil(width / tw),
      down = Math.ceil(height / th),
      tileCount = across * down;
    if (
      !Number.isSafeInteger(total) ||
      total > LIMIT ||
      !Number.isSafeInteger(tileCount) ||
      tileCount < 1 ||
      tileCount > 128 ||
      rows !== tileCount
    )
      return;
    const tableBytes = tableWidth * rows,
      heap = f.has('THEAP') ? integer(f.get('THEAP')) : tableBytes;
    if (!Number.isSafeInteger(heap) || heap < tableBytes || heap > tableBytes + heapSize) return;
    const available = tableBytes + heapSize - heap,
      descriptors = new DataView(await blob.slice(h.offset, h.offset + tableBytes).arrayBuffer());
    if (descriptors.byteLength !== tableBytes) return;
    const image = new Uint8Array(total),
      ranges: [number, number][] = [];
    let decoded = 0;
    for (let i = 0; i < tileCount; i++) {
      const len = descriptors.getInt32(i * 8, false),
        offset = descriptors.getInt32(i * 8 + 4, false);
      if (
        len <= 0 ||
        offset < 0 ||
        offset + len > available ||
        ranges.some(([a, b]) => offset < b && offset + len > a)
      )
        return;
      ranges.push([offset, offset + len]);
      const x = (i % across) * tw,
        y = Math.floor(i / across) * th,
        w = Math.min(tw, width - x),
        ht = Math.min(th, height - y),
        expected = w * ht * size;
      decoded += expected;
      if (decoded > LIMIT) return;
      const compressed = blob.slice(h.offset + heap + offset, h.offset + heap + offset + len);
      const tile =
        codec === 'RICE_1'
          ? decodeFitsRice(new Uint8Array(await compressed.arrayBuffer()), w * ht, size, 32)
          : await unpack(compressed, expected);
      if (!tile) return;
      for (let row = 0; row < ht; row++)
        image.set(
          tile.subarray(row * w * size, (row + 1) * w * size),
          ((y + row) * width + x) * size,
        );
    }
    const cards: string[] = [
      'SIMPLE  =                    T',
      `BITPIX  = ${String(bitpix).padStart(20)}`,
      'NAXIS   =                    2',
      `NAXIS1  = ${String(width).padStart(20)}`,
      `NAXIS2  = ${String(height).padStart(20)}`,
    ];
    for (const k of ['BSCALE', 'BZERO', 'BLANK', 'ZBLANK']) {
      const value = f.get(k);
      if (value === undefined) continue;
      if (k === 'ZBLANK' && f.has('BLANK')) return;
      const dest = k === 'ZBLANK' ? 'BLANK' : k;
      if (
        (dest === 'BLANK' ? /^[+-]?\d+$/ : /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eEdD][+-]?\d+)?$/).test(
          value,
        ) === false
      )
        return;
      cards.push(`${dest.padEnd(8)}= ${value.padStart(20)}`);
    }
    cards.push('END');
    const header = new TextEncoder().encode(
      cards
        .map((c) => c.padEnd(80, ' '))
        .join('')
        .padEnd(Math.ceil((cards.length * 80) / 2880) * 2880, ' '),
    );
    const padding = new Uint8Array((2880 - (image.length % 2880)) % 2880);
    return new Blob([header, image, padding]);
  } catch {
    return;
  }
}
