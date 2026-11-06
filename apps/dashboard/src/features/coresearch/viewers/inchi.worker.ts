import { loadCappedRdkit } from './capped-rdkit-loader';
import { molfileFromInchi } from './vendor/inchi/inchi-js.js';
import { inchiRecord, depictInchi } from './inchi';
self.onmessage = async (event: MessageEvent<string>) => {
  try {
    const input = inchiRecord(event.data);
    if (!input) return self.postMessage(null);
    let molfile: string | undefined;
    if (/\/t[^/]+\/m[01]\/s1$/.test(input)) {
      const { tetrahedralMolfile } = await import('./inchi-stereo');
      molfile = await tetrahedralMolfile(input);
    } else {
      const result = await molfileFromInchi(input);
      if (result.returnCode === 0 && result.molfile?.length <= 262144) molfile = result.molfile;
    }
    if (!molfile) return self.postMessage(null);
    const kit = await loadCappedRdkit();
    self.postMessage(depictInchi(kit, molfile, input) ?? null);
  } catch {
    self.postMessage(null);
  }
};
