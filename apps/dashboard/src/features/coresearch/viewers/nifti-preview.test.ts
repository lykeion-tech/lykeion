import { expect, it } from 'vitest';
import { parseNifti } from './nifti-preview';
function file(
  opts: {
    little?: boolean;
    affine?: number[][];
    slope?: number;
    intercept?: number;
    qform?: boolean;
    description?: string;
  } = {},
) {
  const little = opts.little ?? true,
    bytes = new Uint8Array(360),
    v = new DataView(bytes.buffer);
  v.setInt32(0, 348, little);
  v.setInt16(40, 3, little);
  for (let i = 1; i <= 3; i++) v.setInt16(40 + i * 2, 2, little);
  v.setInt16(70, 2, little);
  v.setInt16(72, 8, little);
  for (let i = 0; i < 4; i++) v.setFloat32(76 + 4 * i, 1, little);
  v.setFloat32(108, 352, little);
  v.setFloat32(112, opts.slope ?? 0, little);
  v.setFloat32(116, opts.intercept ?? 0, little);
  if (opts.affine) {
    v.setInt16(254, 1, little);
    opts.affine.forEach((row, i) =>
      row.forEach((value, j) => v.setFloat32(280 + i * 16 + j * 4, value, little)),
    );
  }
  if (opts.qform) v.setInt16(252, 1, little);
  bytes.set(new TextEncoder().encode(opts.description ?? ''), 148);
  bytes.set([110, 43, 49, 0], 344);
  bytes.set([0, 1, 2, 3, 4, 5, 6, 7], 352);
  return bytes;
}
const identity = [
  [1, 0, 0, 0],
  [0, 1, 0, 0],
  [0, 0, 1, 0],
];
it('renders an axis-aligned axial slice and hides free-text headers', () => {
  const result = parseNifti(
    file({ affine: identity, description: 'PRIVATE PATIENT SENTINEL' }),
    2,
    0,
  );
  expect(result).toMatchObject({ width: 2, height: 2, anatomical: true, plane: 'Axial' });
  expect([...result!.rgba].filter((_, i) => i % 4 === 0)).toEqual([170, 255, 0, 85]);
  expect(JSON.stringify(result)).not.toContain('PRIVATE PATIENT SENTINEL');
});
it('honors flipped/permuted sform, endian order, and scaling', () => {
  const data = file({
    little: false,
    affine: [
      [0, 0, 1, 0],
      [0, 1, 0, 0],
      [-1, 0, 0, 0],
    ],
    slope: -2,
    intercept: 7,
  });
  const result = parseNifti(data, 2, 0);
  expect(result?.anatomical).toBe(true);
  expect([...result!.rgba].filter((_, i) => i % 4 === 0)).toEqual([170, 0, 255, 85]);
});
it('labels oblique or absent geometry as native and validates slice/header bounds', () => {
  expect(parseNifti(file(), 2, 0)?.anatomical).toBe(false);
  expect(
    parseNifti(
      file({
        affine: [
          [1, 0.2, 0, 0],
          [0, 1, 0, 0],
          [0, 0, 1, 0],
        ],
      }),
      2,
      0,
    )?.anatomical,
  ).toBe(false);
  expect(
    parseNifti(
      file({
        affine: [
          [1, 0.0000001, 0, 0],
          [0, 1, 0, 0],
          [0, 0, 1, 0],
        ],
      }),
      2,
      0,
    )?.anatomical,
  ).toBe(false);
  expect(parseNifti(file({ qform: true }), 2, 0)?.anatomical).toBe(true);
  expect(parseNifti(file(), 2, 2)).toBeUndefined();
  expect(parseNifti(file().slice(0, 359), 2, 0)).toBeUndefined();
});
it('does not claim anatomical orientation for invalid quaternion metadata', () => {
  const data = file({ qform: true });
  new DataView(data.buffer).setFloat32(268, Number.NaN, true);
  expect(parseNifti(data, 2, 0)?.anatomical).toBe(false);
});
it('supports signed floating pixels and hides non-finite samples', () => {
  const data = new Uint8Array(384);
  data.set(file().slice(0, 352));
  const v = new DataView(data.buffer);
  v.setInt16(70, 16, true);
  v.setInt16(72, 32, true);
  [-1, 1, Number.NaN, 0, 0, 0, 0, 0].forEach((n, i) => v.setFloat32(352 + 4 * i, n, true));
  const preview = parseNifti(data, 2, 0)!;
  expect([...preview.rgba].filter((_, i) => i % 4 === 3)).toEqual([255, 255, 0, 255]);
});
it('reads signed big-endian 16-bit values', () => {
  const data = new Uint8Array(368);
  data.set(file({ little: false }).slice(0, 352));
  const view = new DataView(data.buffer);
  view.setInt16(70, 4, false);
  view.setInt16(72, 16, false);
  [-4, 0, 4, 8, 0, 0, 0, 0].forEach((n, i) => view.setInt16(352 + i * 2, n, false));
  const result = parseNifti(data, 2, 0)!;
  expect([...result.rgba].filter((_, i) => i % 4 === 0)).toEqual([0, 85, 170, 255]);
});
