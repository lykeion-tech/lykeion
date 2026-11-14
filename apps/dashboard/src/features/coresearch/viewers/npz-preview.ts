const MAX_ARCHIVE = 32 * 1024 * 1024;
const MAX_ARRAY = 16 * 1024 * 1024;
const MAX_ENTRIES = 128;
export interface NpzEntry {
  name: string;
  offset: number;
  compressed: number;
  size: number;
  crc: number;
  method: number;
}
const u16 = (view: DataView, offset: number) => view.getUint16(offset, true);
const u32 = (view: DataView, offset: number) => view.getUint32(offset, true);
const same = (a: Uint8Array, b: Uint8Array) =>
  a.length === b.length && a.every((x, i) => x === b[i]);

// A bounded ZIP directory reader. Filenames are labels and are never used as paths.
export async function listZip(blob: Blob, maxEntries: number): Promise<NpzEntry[] | undefined> {
  try {
    if (blob.size < 22 || blob.size > MAX_ARCHIVE) return;
    const tailStart = Math.max(0, blob.size - 65557);
    const tailBytes = new Uint8Array(await blob.slice(tailStart).arrayBuffer());
    const tail = new DataView(tailBytes.buffer);
    let end = -1;
    for (let i = tailBytes.length - 22; i >= 0; i--) {
      if (u32(tail, i) === 0x06054b50 && i + 22 + u16(tail, i + 20) === tailBytes.length) {
        end = i;
        break;
      }
    }
    if (end < 0 || u16(tail, end + 4) || u16(tail, end + 6)) return;
    const count = u16(tail, end + 10),
      length = u32(tail, end + 12),
      offset = u32(tail, end + 16);
    if (
      !count ||
      count > maxEntries ||
      u16(tail, end + 8) !== count ||
      offset + length !== tailStart + end
    )
      return;
    const directory = new Uint8Array(await blob.slice(offset, offset + length).arrayBuffer());
    const view = new DataView(directory.buffer);
    const result: NpzEntry[] = [],
      names = new Set<string>(),
      ranges: [number, number][] = [];
    let at = 0;
    for (let n = 0; n < count; n++) {
      if (at + 46 > length || u32(view, at) !== 0x02014b50) return;
      const flags = u16(view, at + 8),
        method = u16(view, at + 10),
        crc = u32(view, at + 16),
        compressed = u32(view, at + 20),
        size = u32(view, at + 24);
      const nameLength = u16(view, at + 28),
        extraLength = u16(view, at + 30),
        commentLength = u16(view, at + 32),
        local = u32(view, at + 42);
      const next = at + 46 + nameLength + extraLength + commentLength;
      if (
        (flags & ~0x0800) !== 0 ||
        ![0, 8].includes(method) ||
        !nameLength ||
        nameLength > 1024 ||
        next > length ||
        local + 30 > offset ||
        u16(view, at + 34) !== 0 ||
        [compressed, size, local].includes(0xffffffff)
      )
        return;
      const nameBytes = directory.slice(at + 46, at + 46 + nameLength);
      const name = new TextDecoder('utf-8', { fatal: true }).decode(nameBytes);
      if (name.includes('\0') || names.has(name)) return;
      names.add(name);
      const localBytes = new Uint8Array(await blob.slice(local, local + 30).arrayBuffer());
      const head = new DataView(localBytes.buffer);
      if (
        localBytes.length !== 30 ||
        u32(head, 0) !== 0x04034b50 ||
        u16(head, 6) !== flags ||
        u16(head, 8) !== method ||
        u32(head, 14) !== crc ||
        u16(head, 26) !== nameLength
      )
        return;
      const localExtraLength = u16(head, 28),
        start = local + 30 + nameLength + localExtraLength;
      if (
        start + compressed > offset ||
        !same(
          nameBytes,
          new Uint8Array(await blob.slice(local + 30, local + 30 + nameLength).arrayBuffer()),
        )
      )
        return;
      const localSize = u32(head, 22),
        localCompressed = u32(head, 18);
      if (localSize === 0xffffffff || localCompressed === 0xffffffff) {
        const extra = new DataView(await blob.slice(local + 30 + nameLength, start).arrayBuffer());
        let p = 0,
          valid = false;
        while (p + 4 <= extra.byteLength) {
          const id = u16(extra, p),
            l = u16(extra, p + 2);
          p += 4;
          if (p + l > extra.byteLength) return;
          if (id === 1) {
            let q = p;
            if (localSize === 0xffffffff) {
              if (q + 8 > p + l || extra.getBigUint64(q, true) !== BigInt(size)) return;
              q += 8;
            } else if (localSize !== size) return;
            if (localCompressed === 0xffffffff) {
              if (q + 8 > p + l || extra.getBigUint64(q, true) !== BigInt(compressed)) return;
            } else if (localCompressed !== compressed) return;
            valid = true;
            break;
          }
          p += l;
        }
        if (!valid) return;
      } else if (localSize !== size || localCompressed !== compressed) return;
      if (method === 0 && compressed !== size) return;
      if (ranges.some(([a, b]) => local < b && start + compressed > a)) return;
      ranges.push([local, start + compressed]);
      result.push({ name, offset: start, compressed, size, crc, method });
      at = next;
    }
    return at === length ? result : undefined;
  } catch {
    return undefined;
  }
}

export async function listNpz(blob: Blob): Promise<NpzEntry[] | undefined> {
  const entries = await listZip(blob, MAX_ENTRIES);
  return entries?.every((entry) => entry.name.endsWith('.npy')) ? entries : undefined;
}

function crcUpdate(crc: number, bytes: Uint8Array): number {
  for (const byte of bytes) {
    crc ^= byte;
    for (let i = 0; i < 8; i++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return crc;
}
export async function extractNpz(blob: Blob, entry: NpzEntry): Promise<Blob | undefined> {
  if (
    entry.size > MAX_ARRAY ||
    entry.offset < 0 ||
    entry.compressed < 0 ||
    entry.offset + entry.compressed > blob.size ||
    ![0, 8].includes(entry.method)
  )
    return;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    const input = blob.slice(entry.offset, entry.offset + entry.compressed).stream();
    const stream =
      entry.method === 8 ? input.pipeThrough(new DecompressionStream('deflate-raw')) : input;
    reader = stream.getReader();
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    let size = 0,
      crc = 0xffffffff;
    while (true) {
      const item = await reader.read();
      if (item.done) break;
      size += item.value.byteLength;
      if (size > MAX_ARRAY || size > entry.size) {
        await reader.cancel();
        return;
      }
      crc = crcUpdate(crc, item.value);
      chunks.push(new Uint8Array(item.value));
    }
    if (size !== entry.size || (crc ^ 0xffffffff) >>> 0 !== entry.crc) return;
    return new Blob(chunks);
  } catch {
    await reader?.cancel().catch(() => {});
    return;
  }
}
