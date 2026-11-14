export interface NiftiPreview {
  width: number;
  height: number;
  anatomical: boolean;
  plane: string;
  sliceCount: number;
  aspect: number;
  spacingApplied: boolean;
  rgba: Uint8ClampedArray<ArrayBuffer>;
}
type Orientation = { axes: number[]; signs: number[]; sizes: number[] };
function orientation(matrix: number[][]): Orientation | undefined {
  const axes: number[] = [],
    signs: number[] = [],
    sizes: number[] = [];
  for (const row of matrix) {
    if (row.length !== 3 || row.some((n) => !Number.isFinite(n))) return;
    const magnitude = Math.max(...row.map(Math.abs));
    if (!magnitude) return;
    const axis = row.findIndex((n) => Math.abs(n) === magnitude);
    if (axes.includes(axis) || row.some((n, i) => i !== axis && n !== 0)) return;
    axes.push(axis);
    signs.push(Math.sign(row[axis]!));
    sizes.push(magnitude);
  }
  return { axes, signs, sizes };
}
function qMatrix(view: DataView, little: boolean, spacing: number[]): number[][] | undefined {
  const b = view.getFloat32(256, little),
    c = view.getFloat32(260, little),
    d = view.getFloat32(264, little);
  const offsets = [268, 272, 276].map((at) => view.getFloat32(at, little)),
    rawQfac = view.getFloat32(76, little);
  const sum = b * b + c * c + d * d;
  if (
    ![b, c, d, sum, ...spacing, ...offsets].every(Number.isFinite) ||
    ![-1, 0, 1].includes(rawQfac) ||
    sum > 1 ||
    spacing.some((x) => x <= 0)
  )
    return;
  const a = Math.sqrt(1 - sum),
    qfac = rawQfac < 0 ? -1 : 1;
  const matrix = [
    [a * a + b * b - c * c - d * d, 2 * (b * c - a * d), 2 * (b * d + a * c)],
    [2 * (b * c + a * d), a * a + c * c - b * b - d * d, 2 * (c * d - a * b)],
    [2 * (b * d - a * c), 2 * (c * d + a * b), a * a + d * d - c * c - b * b],
  ];
  return matrix.map((row) => row.map((n, i) => n * spacing[i]! * (i === 2 ? qfac : 1)));
}
export function parseNifti(
  bytes: Uint8Array,
  axis: number,
  index: number,
): NiftiPreview | undefined {
  try {
    if (
      bytes.length < 352 ||
      bytes.length > 32 * 1024 * 1024 ||
      !Number.isInteger(axis) ||
      axis < 0 ||
      axis > 2 ||
      !Number.isSafeInteger(index) ||
      index < 0
    )
      return;
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const little =
      view.getInt32(0, true) === 348 ? true : view.getInt32(0, false) === 348 ? false : undefined;
    if (
      little === undefined ||
      bytes[344] !== 110 ||
      bytes[345] !== 43 ||
      bytes[346] !== 49 ||
      bytes[347] !== 0
    )
      return;
    const rank = view.getInt16(40, little);
    if (rank < 3 || rank > 7) return;
    const dims = [1, 2, 3].map((i) => view.getInt16(40 + i * 2, little));
    if (
      dims.some((n) => n < 1) ||
      Array.from({ length: rank - 3 }, (_, i) => view.getInt16(48 + i * 2, little)).some(
        (n) => n !== 1,
      )
    )
      return;
    const datatype = view.getInt16(70, little),
      bits = view.getInt16(72, little);
    const sizes: Record<number, number> = {
      2: 1,
      4: 2,
      8: 4,
      16: 4,
      64: 8,
      256: 1,
      512: 2,
      768: 4,
    };
    const size = sizes[datatype];
    if (!size || bits !== size * 8) return;
    const offset = view.getFloat32(108, little),
      total = dims[0]! * dims[1]! * dims[2]!;
    if (
      !Number.isSafeInteger(offset) ||
      offset < 352 ||
      offset % 16 !== 0 ||
      !Number.isSafeInteger(total) ||
      total > 4 * 1024 * 1024 ||
      offset + total * size > bytes.length
    )
      return;
    let slope = view.getFloat32(112, little),
      intercept = view.getFloat32(116, little);
    if (slope === 0) {
      slope = 1;
      intercept = 0;
    }
    if (!Number.isFinite(slope) || !Number.isFinite(intercept)) return;
    const spacing = [1, 2, 3].map((i) => view.getFloat32(76 + i * 4, little));
    let transform: Orientation | undefined;
    const sform = view.getInt16(254, little),
      qform = view.getInt16(252, little);
    if (sform > 0 && sform <= 5) {
      const rows = [0, 1, 2].map((i) =>
        Array.from({ length: 4 }, (_, j) => view.getFloat32(280 + i * 16 + j * 4, little)),
      );
      if (rows.every((row) => row.every(Number.isFinite)))
        transform = orientation(rows.map((row) => row.slice(0, 3)));
    } else if (sform === 0 && qform > 0 && qform <= 5) {
      const matrix = qMatrix(view, little, spacing);
      if (matrix) transform = orientation(matrix);
    }
    const anatomical = !!transform;
    const voxelAxis = transform?.axes[axis] ?? axis;
    const sliceCount = dims[voxelAxis]!;
    if (index >= sliceCount) return;
    const planes = [
      [1, 2],
      [0, 2],
      [0, 1],
    ] as const;
    const [horizontal, vertical] = planes[axis]!;
    const xAxis = transform?.axes[horizontal] ?? horizontal,
      yAxis = transform?.axes[vertical] ?? vertical;
    const width = dims[xAxis]!,
      height = dims[yAxis]!;
    if (width * height > 2 * 1024 * 1024) return;
    const spacingApplied = transform ? true : spacing.every((n) => Number.isFinite(n) && n > 0);
    const xSpacing = transform?.sizes[horizontal] ?? (spacingApplied ? spacing[xAxis]! : 1);
    const ySpacing = transform?.sizes[vertical] ?? (spacingApplied ? spacing[yAxis]! : 1);
    const aspect = (width * xSpacing) / (height * ySpacing);
    if (!Number.isFinite(aspect) || aspect < 0.001 || aspect > 1000) return;
    const values = new Float64Array(width * height),
      valid = new Uint8Array(width * height);
    let min = Infinity,
      max = -Infinity;
    for (let y = 0; y < height; y++)
      for (let x = 0; x < width; x++) {
        const coords = [0, 0, 0];
        coords[voxelAxis] = index;
        coords[xAxis] = transform && transform.signs[horizontal]! < 0 ? width - 1 - x : x;
        coords[yAxis] = transform && transform.signs[vertical]! > 0 ? height - 1 - y : y;
        const pos = offset + (coords[0]! + dims[0]! * (coords[1]! + dims[1]! * coords[2]!)) * size;
        const raw =
          datatype === 2
            ? view.getUint8(pos)
            : datatype === 256
              ? view.getInt8(pos)
              : datatype === 4
                ? view.getInt16(pos, little)
                : datatype === 512
                  ? view.getUint16(pos, little)
                  : datatype === 8
                    ? view.getInt32(pos, little)
                    : datatype === 768
                      ? view.getUint32(pos, little)
                      : datatype === 16
                        ? view.getFloat32(pos, little)
                        : view.getFloat64(pos, little);
        const value = raw * slope + intercept,
          i = y * width + x;
        if (Number.isFinite(value)) {
          valid[i] = 1;
          values[i] = value;
          min = Math.min(min, value);
          max = Math.max(max, value);
        }
      }
    if (!Number.isFinite(min) || !Number.isFinite(max)) return;
    const divisor = Math.max(Math.abs(min), Math.abs(max)) || 1,
      lo = min / divisor,
      range = max / divisor - lo;
    const rgba = new Uint8ClampedArray(width * height * 4);
    for (let i = 0; i < valid.length; i++) {
      if (!valid[i]) continue;
      const intensity = range === 0 ? 128 : Math.round(((values[i]! / divisor - lo) * 255) / range);
      rgba[i * 4] = intensity;
      rgba[i * 4 + 1] = intensity;
      rgba[i * 4 + 2] = intensity;
      rgba[i * 4 + 3] = 255;
    }
    return {
      width,
      height,
      anatomical,
      plane: anatomical ? ['Sagittal', 'Coronal', 'Axial'][axis]! : `Native voxel axis ${axis + 1}`,
      sliceCount,
      aspect,
      spacingApplied,
      rgba,
    };
  } catch {
    return;
  }
}
