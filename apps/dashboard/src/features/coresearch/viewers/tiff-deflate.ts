/** Decode exactly one zlib-wrapped TIFF Deflate strip within its expected byte size. */
export async function inflateTiffStrip(
  input: Uint8Array,
  expected: number,
): Promise<Uint8Array | undefined> {
  if (
    !Number.isSafeInteger(expected) ||
    expected < 1 ||
    expected > 6 * 1024 * 1024 ||
    input.length > 32 * 1024 * 1024
  )
    return;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    reader = new Blob([new Uint8Array(input)])
      .stream()
      .pipeThrough(new DecompressionStream('deflate'))
      .getReader();
    const output = new Uint8Array(expected);
    let offset = 0;
    while (true) {
      const part = await reader.read();
      if (part.done) break;
      if (part.value.length > expected - offset) {
        await reader.cancel();
        return;
      }
      output.set(part.value, offset);
      offset += part.value.length;
    }
    return offset === expected ? output : undefined;
  } catch {
    await reader?.cancel().catch(() => {});
    return;
  }
}
