import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';
import { depictSmiles } from './smiles';
const require = createRequire(import.meta.url);
const init = require('@rdkit/rdkit') as () => Promise<Parameters<typeof depictSmiles>[0]>;
describe('packaged RDKit SMILES depiction', () => {
  it('depicts real aliphatic, aromatic and chiral structures and rejects invalid input', async () => {
    const kit = await init();
    for (const input of ['CCO', 'c1ccccc1', 'N[C@@H](C)C(=O)O']) {
      expect(depictSmiles(kit, input)).toContain('<svg');
    }
    expect(depictSmiles(kit, 'C1CC')).toBeUndefined();
  });
});
