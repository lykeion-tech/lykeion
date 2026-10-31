// UTF-8 decoding and text head limit ported from L2 viewers/format-text.ts.
export const FORMAT_TEXT_HEAD_BYTES = 1024 * 1024;
export async function readFormatText(blob: Blob, maxBytes = FORMAT_TEXT_HEAD_BYTES) {
  const bytes = new Uint8Array(await blob.slice(0, maxBytes).arrayBuffer());
  let text: string | undefined;
  try {
    const decoded = new TextDecoder('utf-8', { fatal: true }).decode(bytes, {
      stream: blob.size > maxBytes,
    });
    if (!bytes.some((byte) => byte <= 8 || (byte >= 14 && byte <= 31))) text = decoded;
  } catch {
    /* Binary content has no raw text preview. */
  }
  return { text, partial: blob.size > maxBytes };
}
