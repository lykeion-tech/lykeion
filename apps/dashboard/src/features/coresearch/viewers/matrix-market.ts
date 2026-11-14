const MAX_ENTRIES = 50000;
const MAX_DIMENSION = 1_000_000_000;
const MAX_TEXT = 1_048_576;
export interface MatrixMarketPreview {
  shape: [number, number];
  rows: number[];
  columns: number[];
  values: number[];
  entries: number;
  sampled: boolean;
}
export function parseMatrixMarket(text: string, partial: boolean): MatrixMarketPreview | undefined {
  if (partial || text.length > MAX_TEXT) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const header = lines
    .shift()
    ?.trim()
    .match(
      /^%%MatrixMarket\s+matrix\s+(coordinate|array)\s+(real|integer|pattern)\s+(general|symmetric|skew-symmetric)$/i,
    );
  if (!header) return undefined;
  const format = header[1]!.toLowerCase(),
    field = header[2]!.toLowerCase(),
    symmetry = header[3]!.toLowerCase();
  if (format === 'array' && (field === 'pattern' || symmetry !== 'general')) return undefined;
  const data = lines.map((line) => line.trim()).filter((line) => line && !line.startsWith('%'));
  const dimensions = data.shift()?.split(/\s+/);
  if (
    !dimensions ||
    dimensions.length !== (format === 'coordinate' ? 3 : 2) ||
    dimensions.some((s) => !/^\d+$/.test(s))
  )
    return undefined;
  const [height, width] = dimensions.map(Number) as [number, number];
  if (
    !height ||
    !width ||
    height > MAX_DIMENSION ||
    width > MAX_DIMENSION ||
    (symmetry !== 'general' && height !== width)
  )
    return undefined;
  const count = format === 'coordinate' ? Number(dimensions[2]) : height * width;
  if (!Number.isSafeInteger(count) || count < 0 || count > MAX_ENTRIES || count !== data.length)
    return undefined;
  const axis = (n: number) =>
    Array.from({ length: Math.min(10, n) }, (_, i) =>
      n === 1 ? 1 : 1 + Math.floor((i * (n - 1)) / (Math.min(10, n) - 1)),
    );
  const rows = axis(height),
    columns = axis(width);
  const rowIndex = new Map(rows.map((n, i) => [n, i])),
    colIndex = new Map(columns.map((n, i) => [n, i]));
  const values = Array<number>(rows.length * columns.length).fill(0);
  const add = (row: number, col: number, value: number) => {
    const r = rowIndex.get(row),
      c = colIndex.get(col);
    if (r === undefined || c === undefined) return true;
    const index = r * columns.length + c,
      sum = values[index]! + value;
    if (!Number.isFinite(sum) || (field === 'integer' && !Number.isSafeInteger(sum))) return false;
    values[index] = sum;
    return true;
  };
  const numberPattern = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/;
  let triangle: 'upper' | 'lower' | undefined;
  for (let i = 0; i < data.length; i++) {
    const parts = data[i]!.split(/\s+/);
    let row: number, col: number, raw: string;
    if (format === 'coordinate') {
      if (
        parts.length !== (field === 'pattern' ? 2 : 3) ||
        !/^\d+$/.test(parts[0]!) ||
        !/^\d+$/.test(parts[1]!)
      )
        return undefined;
      row = Number(parts[0]);
      col = Number(parts[1]);
      raw = field === 'pattern' ? '1' : parts[2]!;
    } else {
      if (parts.length !== 1) return undefined;
      row = (i % height) + 1;
      col = Math.floor(i / height) + 1;
      raw = parts[0]!;
    }
    if (
      !Number.isSafeInteger(row) ||
      !Number.isSafeInteger(col) ||
      row < 1 ||
      row > height ||
      col < 1 ||
      col > width ||
      !numberPattern.test(raw)
    )
      return undefined;
    const value = Number(raw);
    if (
      !Number.isFinite(value) ||
      (field === 'integer' && (!/^[+-]?\d+$/.test(raw) || !Number.isSafeInteger(value)))
    )
      return undefined;
    if (symmetry !== 'general' && row !== col) {
      const current = row < col ? 'upper' : 'lower';
      if (triangle && triangle !== current) return undefined;
      triangle = current;
    }
    if (symmetry === 'skew-symmetric' && row === col && value !== 0) return undefined;
    if (!add(row, col, value)) return undefined;
    if (
      symmetry !== 'general' &&
      row !== col &&
      !add(col, row, symmetry === 'skew-symmetric' ? -value : value)
    )
      return undefined;
  }
  return {
    shape: [height, width],
    rows,
    columns,
    values,
    entries: count,
    sampled: rows.length < height || columns.length < width,
  };
}
