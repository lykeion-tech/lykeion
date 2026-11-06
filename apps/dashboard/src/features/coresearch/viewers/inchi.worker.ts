import { loadCappedRdkit } from './capped-rdkit-loader';
import { molfileFromInchi } from './vendor/inchi/inchi-js.js';
import { inchiRecord, depictInchi } from './inchi';
self.onmessage = async (event: MessageEvent<string>) => {
  try {
    const input = inchiRecord(event.data);
    if (!input) return self.postMessage(null);
    if (/\/b\d+-\d+[+-]$/.test(input)) {
      const [{ geometricMolfileCandidates }, kit] = await Promise.all([
        import('./inchi-stereo'),
        loadCappedRdkit(),
      ]);
      const candidates = await geometricMolfileCandidates(input);
      const matches = candidates
        .map((molfile) => depictInchi(kit, molfile, input, true))
        .filter((svg): svg is string => !!svg);
      return self.postMessage(matches.length === 1 ? matches[0] : null);
    }
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
