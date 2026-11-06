import initRDKitModule from '@rdkit/rdkit';
import wasmUrl from '@rdkit/rdkit/RDKit_minimal.wasm?url';
import { depictSmiles, smilesRecord } from './smiles';
self.onmessage = async (event: MessageEvent<string>) => {
  try {
    const input = smilesRecord(event.data);
    if (!input) {
      self.postMessage(null);
      return;
    }
    const kit = await initRDKitModule({ locateFile: () => wasmUrl });
    self.postMessage(depictSmiles(kit, input) ?? null);
  } catch {
    self.postMessage(null);
  }
};
