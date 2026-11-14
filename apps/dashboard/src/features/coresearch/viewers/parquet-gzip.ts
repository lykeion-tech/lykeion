export async function gunzipPage(
  bytes: Uint8Array,
  expected: number,
): Promise<Uint8Array | undefined> {
  if (
    !Number.isSafeInteger(expected) ||
    expected < 0 ||
    expected > 8 * 1024 * 1024 ||
    bytes.length > 16 * 1024 * 1024
  )
    return;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    reader = new Blob([new Uint8Array(bytes)])
      .stream()
      .pipeThrough(new DecompressionStream('gzip'))
      .getReader();
    const output = new Uint8Array(expected);
    let offset = 0;
    while (true) {
      const next = await reader.read();
      if (next.done) break;
      if (next.value.length > expected - offset) {
        await reader.cancel();
        return;
      }
      output.set(next.value, offset);
      offset += next.value.length;
    }
    return offset === expected ? output : undefined;
  } catch {
    await reader?.cancel().catch(() => {});
    return;
  }
}
