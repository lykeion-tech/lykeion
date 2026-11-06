declare function init(options: {
  wasmBinary: Uint8Array;
  print: () => void;
  printErr: () => void;
}): Promise<{
  HEAPU8: Uint8Array;
  _malloc(size: number): number;
  _free(ptr: number): void;
  _preview_decode(ptr: number, size: number): number;
  _preview_release(): void;
  _preview_width(): number;
  _preview_height(): number;
  _preview_pixels(): number;
}>;
export default init;
