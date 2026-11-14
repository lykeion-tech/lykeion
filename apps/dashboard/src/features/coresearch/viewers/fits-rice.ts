/** Bounded FITS RICE_1 integer tile decoding, with MSB-first bitstream. */
export function decodeFitsRice(
  input: Uint8Array,
  pixels: number,
  bytesPerPixel: number,
  blockSize: number,
): Uint8Array | undefined {
  try {
    if (
      ![1, 2, 4].includes(bytesPerPixel) ||
      blockSize !== 32 ||
      !Number.isSafeInteger(pixels) ||
      pixels < 1 ||
      pixels * bytesPerPixel > 8 * 1024 * 1024 ||
      input.length > 8 * 1024 * 1024
    )
      throw Error();
    const bits = bytesPerPixel * 8,
      fsbits = bytesPerPixel === 1 ? 3 : bytesPerPixel === 2 ? 4 : 5,
      fsmax = bytesPerPixel === 1 ? 6 : bytesPerPixel === 2 ? 14 : 25,
      mod = 2 ** bits;
    let bit = 0;
    const read = (n: number) => {
      if (n < 0 || n > 32 || bit + n > input.length * 8) throw Error();
      let value = 0;
      for (let i = 0; i < n; i++, bit++)
        value = value * 2 + ((input[bit >> 3]! >> (7 - (bit & 7))) & 1);
      return value;
    };
    let last = read(bits);
    const output = new Uint8Array(pixels * bytesPerPixel),
      out = new DataView(output.buffer);
    let at = 0;
    while (at < pixels) {
      const code = read(fsbits),
        n = Math.min(blockSize, pixels - at);
      if (code > fsmax + 1) throw Error();
      for (let i = 0; i < n; i++) {
        let mapped = 0;
        if (code === fsmax + 1) mapped = read(bits);
        else if (code !== 0) {
          const fs = code - 1,
            unit = 2 ** fs;
          let q = 0;
          while (read(1) === 0) {
            q++;
            if (q > Math.floor((mod - 1) / unit)) throw Error();
          }
          mapped = q * unit + read(fs);
          if (mapped >= mod) throw Error();
        }
        if (at === 0 && mapped !== 0) throw Error();
        const delta = mapped % 2 === 0 ? mapped / 2 : -(Math.floor(mapped / 2) + 1);
        last = (last + delta + mod) % mod;
        if (bytesPerPixel === 1) out.setUint8(at, last);
        else if (bytesPerPixel === 2) out.setUint16(at * 2, last, false);
        else out.setUint32(at * 4, last, false);
        at++;
      }
    }
    if (input.length * 8 - bit >= 8) throw Error();
    while (bit < input.length * 8) if (read(1) !== 0) throw Error();
    return output;
  } catch {
    return;
  }
}
