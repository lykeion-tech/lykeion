// V1 definition levels use a four-byte length followed by one-bit hybrid runs.
export function optionalLevels(
  bytes: Uint8Array,
  count: number,
): { levels: boolean[]; bytes: number } | undefined {
  try {
    if (!Number.isInteger(count) || count < 1 || count > 10000 || bytes.length < 4) throw Error();
    const length = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(
      0,
      true,
    );
    if (length > bytes.length - 4 || length > 50000) throw Error();
    const end = 4 + length,
      levels: boolean[] = [];
    let p = 4;
    while (levels.length < count) {
      let header = 0,
        shift = 0,
        done = false;
      for (let i = 0; i < 5; i++) {
        if (p >= end) throw Error();
        const b = bytes[p++]!;
        header += (b & 127) * 2 ** shift;
        if (!(b & 128)) {
          done = true;
          break;
        }
        shift += 7;
      }
      if (!done || !Number.isSafeInteger(header) || !header) throw Error();
      const run = Math.floor(header / 2);
      if (!run) throw Error();
      if (header % 2 === 0) {
        if (run > count - levels.length || p >= end) throw Error();
        const bit = bytes[p++]!;
        if (bit > 1) throw Error();
        for (let i = 0; i < run; i++) levels.push(bit === 1);
      } else {
        if (run > Math.ceil((count - levels.length) / 8) || p + run > end) throw Error();
        const n = Math.min(run * 8, count - levels.length);
        for (let i = 0; i < n; i++) levels.push(!!(bytes[p + (i >> 3)]! & (1 << (i & 7))));
        p += run;
      }
    }
    if (p !== end) throw Error();
    return { levels, bytes: end };
  } catch {
    return undefined;
  }
}
