import { loadCappedRdkit } from './capped-rdkit-loader';
import { depictSmiles, smilesRecord } from './smiles';
self.onmessage = async (event: MessageEvent<string>) => {
  try {
    const input = smilesRecord(event.data);
    if (!input) {
      self.postMessage(null);
      return;
    }
    const kit = await loadCappedRdkit();
    self.postMessage(depictSmiles(kit, input) ?? null);
  } catch {
    self.postMessage(null);
  }
};
