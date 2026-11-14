const HARD_OUTPUT_LIMIT = 8 * 1024 * 1024;
const HARD_INPUT_LIMIT = 16 * 1024 * 1024;
/** Decode a Snappy raw block (not the framed format). Returns undefined for unsupported or malformed data. */
export function decompressSnappyRaw(
  input: Uint8Array,
  expectedSize: number,
  maxOutput: number,
): Uint8Array | undefined {
  if (
    !Number.isSafeInteger(expectedSize) ||
    expectedSize < 0 ||
    !Number.isSafeInteger(maxOutput) ||
    maxOutput < 0 ||
    expectedSize > maxOutput ||
    expectedSize > HARD_OUTPUT_LIMIT ||
    input.length > HARD_INPUT_LIMIT
  )
    return undefined;
  let cursor = 0,
    declared = 0,
    scale = 1,
    ended = false;
  for (let i = 0; i < 5; i++) {
    if (cursor >= input.length) return undefined;
    const byte = input[cursor++]!;
    if (i === 4 && byte & 0xf0) return undefined;
    declared += (byte & 0x7f) * scale;
    if (!(byte & 0x80)) {
      if (i > 0 && byte === 0) return undefined;
      ended = true;
      break;
    }
    scale *= 128;
  }
  if (!ended || declared !== expectedSize) return undefined;
  const output = new Uint8Array(expectedSize);
  let written = 0;
  while (cursor < input.length) {
    const tag = input[cursor++]!,
      type = tag & 3;
    if (type === 0) {
      const code = tag >>> 2;
      let length = code + 1;
      if (code >= 60) {
        const count = code - 59;
        if (cursor + count > input.length) return undefined;
        let value = 0,
          factor = 1;
        for (let i = 0; i < count; i++) {
          value += input[cursor++]! * factor;
          factor *= 256;
        }
        length = value + 1;
      }
      if (cursor + length > input.length || written + length > expectedSize) return undefined;
      output.set(input.subarray(cursor, cursor + length), written);
      cursor += length;
      written += length;
    } else {
      let length: number, offset: number;
      if (type === 1) {
        if (cursor >= input.length) return undefined;
        length = 4 + ((tag >>> 2) & 7);
        offset = ((tag & 0xe0) << 3) | input[cursor++]!;
      } else {
        const count = type === 2 ? 2 : 4;
        if (cursor + count > input.length) return undefined;
        length = 1 + (tag >>> 2);
        offset = 0;
        let factor = 1;
        for (let i = 0; i < count; i++) {
          offset += input[cursor++]! * factor;
          factor *= 256;
        }
      }
      if (!offset || offset > written || written + length > expectedSize) return undefined;
      for (let i = 0; i < length; i++) output[written + i] = output[written + i - offset]!;
      written += length;
    }
  }
  return written === expectedSize ? output : undefined;
}
