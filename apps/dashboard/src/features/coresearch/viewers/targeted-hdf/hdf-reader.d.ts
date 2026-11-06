interface HdfModule {
  FS: { writeFile(path: string, data: Uint8Array): void; unlink(path: string): void };
  UTF8ToString(pointer: number): string;
  _inspect(mode: number): number;
  _item_count(): number;
  _is_partial(): number;
  _item_name(i: number): number;
  _item_type(i: number): number;
  _item_rank(i: number): number;
  _item_dim(i: number, j: number): number;
  _item_length(i: number): number;
  _item_value(i: number, j: number): number;
}
declare function createModule(options?: {
  wasmBinary?: Uint8Array;
  locateFile?: (path: string) => string;
}): Promise<HdfModule>;
export default createModule;
