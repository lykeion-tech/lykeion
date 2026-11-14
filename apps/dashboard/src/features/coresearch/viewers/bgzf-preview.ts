const LIMIT = 4 * 1024 * 1024;
export async function readBgzf(
  blob: Blob,
): Promise<{ bytes: Uint8Array; partial: boolean } | undefined> {
  if (blob.size > 32 * 1024 * 1024) return;
  try {
    const blocks: Uint8Array[] = [];
    let offset = 0,
      total = 0,
      count = 0;
    while (offset < blob.size) {
      if (++count > 1024 || offset + 18 > blob.size) return;
      const head = new Uint8Array(await blob.slice(offset, offset + 12).arrayBuffer());
      if (head[0] !== 31 || head[1] !== 139 || head[2] !== 8 || head[3] !== 4) return;
      const xlen = head[10]! | (head[11]! << 8);
      if (xlen < 6 || xlen > 4096 || offset + 12 + xlen + 8 > blob.size) return;
      const extra = new Uint8Array(await blob.slice(offset + 12, offset + 12 + xlen).arrayBuffer());
      let p = 0,
        size = 0;
      while (p < xlen) {
        if (p + 4 > xlen) return;
        const n = extra[p + 2]! | (extra[p + 3]! << 8);
        if (p + 4 + n > xlen) return;
        if (extra[p] === 66 && extra[p + 1] === 67) {
          if (n !== 2 || size) return;
          size = 1 + (extra[p + 4]! | (extra[p + 5]! << 8));
        }
        p += 4 + n;
      }
      if (size < 12 + xlen + 8 || size > 65536 || offset + size > blob.size) return;
      const member = new Uint8Array(await blob.slice(offset, offset + size).arrayBuffer());
      const expected = new DataView(member.buffer).getUint32(size - 4, true);
      if (expected > 65536) return;
      if (total + expected > LIMIT) return { bytes: concat(blocks, total), partial: true };
      const reader = new Blob([member])
        .stream()
        .pipeThrough(new DecompressionStream('gzip'))
        .getReader();
      const output = new Uint8Array(expected);
      let written = 0;
      try {
        for (;;) {
          const result = await reader.read();
          if (result.done) break;
          if (result.value.length > expected - written) {
            await reader.cancel();
            return;
          }
          output.set(result.value, written);
          written += result.value.length;
        }
      } finally {
        reader.releaseLock();
      }
      if (written !== expected) return;
      blocks.push(output);
      total += written;
      offset += size;
    }
    return { bytes: concat(blocks, total), partial: false };
  } catch {
    return;
  }
}
function concat(blocks: Uint8Array[], length: number) {
  const result = new Uint8Array(length);
  let offset = 0;
  for (const block of blocks) {
    result.set(block, offset);
    offset += block.length;
  }
  return result;
}
