export function dictionaryIndexes(
  data: Uint8Array,
  count: number,
  dictionarySize: number,
): number[] | undefined {
  try {
    if (
      !Number.isInteger(count) ||
      count < 0 ||
      count > 10000 ||
      !Number.isInteger(dictionarySize) ||
      dictionarySize < 0 ||
      dictionarySize > 10000 ||
      data.length > 100000
    )
      throw Error();
    if (!count && (data.length === 0 || (data.length === 1 && data[0] === 0))) return [];
    if (!data.length || !dictionarySize) throw Error();
    const width = data[0]!;
    if (width > 32) throw Error();
    let p = 1;
    const out: number[] = [];
    while (out.length < count) {
      let header = 0,
        done = false;
      for (let i = 0; i < 5; i++) {
        if (p >= data.length) throw Error();
        const b = data[p++]!;
        header += (b & 127) * 2 ** (7 * i);
        if (!(b & 128)) {
          done = true;
          break;
        }
      }
      if (!done || !Number.isSafeInteger(header) || !header) throw Error();
      const run = Math.floor(header / 2);
      if (!run) throw Error();
      if (header % 2 === 0) {
        if (run > count - out.length) throw Error();
        const size = Math.ceil(width / 8);
        if (p + size > data.length) throw Error();
        let value = 0;
        for (let i = 0; i < size; i++) value += data[p++]! * 2 ** (8 * i);
        if (value >= 2 ** width || value >= dictionarySize) throw Error();
        for (let i = 0; i < run; i++) out.push(value);
      } else {
        if (run > Math.ceil((count - out.length) / 8)) throw Error();
        const length = run * width;
        if (p + length > data.length) throw Error();
        const n = Math.min(run * 8, count - out.length);
        for (let i = 0; i < run * 8; i++) {
          let value = 0;
          for (let b = 0; b < width; b++) {
            const offset = i * width + b;
            if (data[p + (offset >> 3)]! & (1 << (offset & 7))) value += 2 ** b;
          }
          if (i < n) {
            if (value >= dictionarySize) throw Error();
            out.push(value);
          } else if (value !== 0) throw Error();
        }
        p += length;
      }
    }
    if (p !== data.length) throw Error();
    return out;
  } catch {
    return undefined;
  }
}
