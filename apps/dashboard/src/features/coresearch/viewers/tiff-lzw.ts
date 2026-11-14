/** TIFF 6 LZW: MSB-first codes, clear/EOI, and early code-width changes. */
export function decodeTiffLzw(input: Uint8Array, expected: number): Uint8Array | undefined {
  try {
    if (
      !Number.isSafeInteger(expected) ||
      expected < 1 ||
      expected > 6 * 1024 * 1024 ||
      input.length > 32 * 1024 * 1024
    )
      return;
    const output = new Uint8Array(expected),
      prefix = new Uint16Array(4096),
      suffix = new Uint8Array(4096),
      stack = new Uint8Array(4096);
    let bit = 0,
      width = 9,
      next = 258,
      old = -1,
      written = 0,
      firstCode = true;
    const read = () => {
      if (bit + width > input.length * 8) throw Error();
      let v = 0;
      for (let i = 0; i < width; i++, bit++)
        v = (v << 1) | ((input[bit >> 3]! >> (7 - (bit & 7))) & 1);
      return v;
    };
    const decode = (code: number) => {
      let n = 0,
        c = code;
      while (c >= 258) {
        if (c >= next || n >= 4095) throw Error();
        stack[n++] = suffix[c]!;
        c = prefix[c]!;
      }
      if (c >= 256 || n >= 4096) throw Error();
      stack[n++] = c;
      return n;
    };
    while (true) {
      const code = read();
      if (firstCode) {
        if (code !== 256) throw Error();
        firstCode = false;
      }
      if (code === 256) {
        width = 9;
        next = 258;
        old = -1;
        continue;
      }
      if (code === 257) {
        if (written !== expected || input.length * 8 - bit >= 8) throw Error();
        return output;
      }
      if (old < 0) {
        if (code >= 256 || written >= expected) throw Error();
        output[written++] = code;
        old = code;
        continue;
      }
      if (code > next || code >= 4096) throw Error();
      const special = code === next;
      if (special && next >= 4096) throw Error();
      const n = decode(special ? old : code),
        first = stack[n - 1]!;
      if (written + n + (special ? 1 : 0) > expected) throw Error();
      for (let i = n - 1; i >= 0; i--) output[written++] = stack[i]!;
      if (special) output[written++] = first;
      if (next < 4096) {
        prefix[next] = old;
        suffix[next] = first;
        next++;
        if (width < 12 && next === (1 << width) - 1) width++;
      }
      old = code;
    }
  } catch {
    return;
  }
}
