import initRDKitModule from '@rdkit/rdkit';
import wasmAsset from '@rdkit/rdkit/RDKit_minimal.wasm?url&inline';
import { capRdkitWasm, decodeEmbeddedRdkit } from './capped-rdkit';
export async function loadCappedRdkit() {
  const bytes = decodeEmbeddedRdkit(wasmAsset);
  const module = await WebAssembly.compile(await capRdkitWasm(bytes));
  return initRDKitModule({
    instantiateWasm(
      imports: WebAssembly.Imports,
      receive: (instance: WebAssembly.Instance) => void,
    ) {
      receive(new WebAssembly.Instance(module, imports));
    },
  });
}
