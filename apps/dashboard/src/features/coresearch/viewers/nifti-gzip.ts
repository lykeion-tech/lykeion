const MAX_BYTES = 32 * 1024 * 1024;
export async function gunzipNifti(blob: Blob): Promise<Blob | undefined> {
  if (blob.size > MAX_BYTES) return;
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    reader = blob.stream().pipeThrough(new DecompressionStream('gzip')).getReader();
    const chunks: Uint8Array<ArrayBuffer>[] = [];
    let total = 0;
    while (true) {
      const item = await reader.read();
      if (item.done) break;
      total += item.value.byteLength;
      if (total > MAX_BYTES) {
        await reader.cancel();
        return;
      }
      chunks.push(new Uint8Array(item.value));
    }
    return new Blob(chunks);
  } catch {
    await reader?.cancel().catch(() => {});
    return;
  }
}
