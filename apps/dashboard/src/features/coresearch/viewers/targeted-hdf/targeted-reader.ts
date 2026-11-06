import createModule from './hdf-reader.js';
import wasmUrl from './hdf-reader.wasm?url&inline';
export interface TargetedHdfPreview {
  items: { name: string; type: string; shape: number[]; values: string[] }[];
  partial: boolean;
}
const signature = [137, 72, 68, 70, 13, 10, 26, 10];
export async function readTargetedHdf(
  input: Uint8Array,
  mode: 'netcdf' | 'matlab',
  wasmBinary?: Uint8Array,
): Promise<TargetedHdfPreview | undefined> {
  if (input.length > 32 * 1024 * 1024) return;
  const offset = mode === 'matlab' ? 512 : 0;
  if (input.length < offset + 8 || !signature.every((b, i) => input[offset + i] === b)) return;
  if (mode === 'matlab') {
    if (new TextDecoder().decode(input.subarray(0, 19)) !== 'MATLAB 7.3 MAT-file') return;
    if (!(
      (input[124] === 0 && input[125] === 2 && input[126] === 73 && input[127] === 77) ||
      (input[124] === 2 && input[125] === 0 && input[126] === 77 && input[127] === 73)
    ))
      return;
  }
  try {
    const mod = await createModule({ wasmBinary, locateFile: () => wasmUrl });
    mod.FS.writeFile('/input', input);
    try {
      if (mod._inspect(mode === 'matlab' ? 1 : 0) !== 1) return;
      const n = mod._item_count();
      if (n > 64) return;
      const items = [];
      for (let i = 0; i < n; i++) {
        const rank = mod._item_rank(i),
          count = mod._item_length(i);
        if (rank > 4 || count > 200) return;
        const shape = Array.from({ length: rank }, (_, j) => mod._item_dim(i, j));
        if (mode === 'matlab') shape.reverse();
        items.push({
          name: mod.UTF8ToString(mod._item_name(i)),
          type: mod.UTF8ToString(mod._item_type(i)),
          shape,
          values: Array.from({ length: count }, (_, j) => mod.UTF8ToString(mod._item_value(i, j))),
        });
      }
      return { items, partial: !!mod._is_partial() };
    } finally {
      mod.FS.unlink('/input');
    }
  } catch {
    return;
  }
}
