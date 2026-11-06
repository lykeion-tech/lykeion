// RDKit 2026.9.2: change its verified defined memory max from 4 GiB to 128 MiB.
export const RDKIT_WASM_SIZE = 7394153;
const ORIGINAL = 'caf7eb9f09733cfd3e1b038eb5e05f9eeafc05f58e5780561c583aba7e5d8fd4';
const CAPPED = '12886a9a636994fd329df818c36c16bd5177a01f6081a0f1234d955ac70b59e9';
async function hash(bytes: Uint8Array<ArrayBuffer>): Promise<string> {
  return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), (b) =>
    b.toString(16).padStart(2, '0'),
  ).join('');
}
export async function capRdkitWasm(input: Uint8Array): Promise<Uint8Array<ArrayBuffer>> {
  if (input.length !== RDKIT_WASM_SIZE) throw new Error('Unexpected RDKit WASM size');
  const bytes = new Uint8Array(input);
  if (
    (await hash(bytes)) !== ORIGINAL ||
    bytes[9078] !== 128 ||
    bytes[9079] !== 128 ||
    bytes[9080] !== 4
  )
    throw new Error('Unexpected RDKit WASM');
  bytes.set([128, 144, 0], 9078);
  if ((await hash(bytes)) !== CAPPED) throw new Error('Invalid capped RDKit WASM');
  return bytes;
}

/** Decode only the exact-size, build-time inlined asset; no URL or filesystem access. */
export function decodeEmbeddedRdkit(asset: string): Uint8Array<ArrayBuffer> {
  const prefix = 'data:application/wasm;base64,';
  if (
    !asset.startsWith(prefix) ||
    asset.length !== prefix.length + 4 * Math.ceil(RDKIT_WASM_SIZE / 3)
  )
    throw new Error('Unexpected embedded RDKit asset');
  const binary = atob(asset.slice(prefix.length));
  if (binary.length !== RDKIT_WASM_SIZE) throw new Error('Invalid embedded RDKit asset');
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
