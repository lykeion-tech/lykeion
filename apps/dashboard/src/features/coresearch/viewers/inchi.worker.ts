import { loadCappedRdkit } from './capped-rdkit-loader';
import { molfileFromInchi } from './vendor/inchi/inchi-js.js';
import { inchiRecord, depictInchi } from './inchi';
self.onmessage = async (event: MessageEvent<string>) => {
  try {
    const input = inchiRecord(event.data);
    if (!input) return self.postMessage(null);
    const result = await molfileFromInchi(input);
    if (result.returnCode !== 0 || !result.molfile || result.molfile.length > 262144)
      return self.postMessage(null);
    const kit = await loadCappedRdkit();
    self.postMessage(depictInchi(kit, result.molfile, input) ?? null);
  } catch {
    self.postMessage(null);
  }
};
