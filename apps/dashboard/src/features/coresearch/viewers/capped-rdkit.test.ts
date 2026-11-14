import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { describe, expect, it } from 'vitest';
import { capRdkitWasm, decodeEmbeddedRdkit } from './capped-rdkit';

describe('RDKit memory limit', () => {
  it('only accepts an embedded local WASM asset', async () => {
    const source = await readFile('node_modules/@rdkit/rdkit/dist/RDKit_minimal.wasm');
    expect(
      Buffer.compare(
        Buffer.from(
          decodeEmbeddedRdkit('data:application/wasm;base64,' + source.toString('base64')),
        ),
        source,
      ),
    ).toBe(0);
    expect(() => decodeEmbeddedRdkit('https://example.com/rdkit.wasm')).toThrow();
    expect(() => decodeEmbeddedRdkit('data:application/wasm;base64,AA==')).toThrow();
  });
  it('caps the verified binary and rejects modified assets', async () => {
    const wasm = new Uint8Array(
      await readFile('node_modules/@rdkit/rdkit/dist/RDKit_minimal.wasm'),
    );
    const capped = await capRdkitWasm(wasm);
    expect(capped.slice(9078, 9081)).toEqual(new Uint8Array([128, 144, 0]));
    const altered = wasm.slice();
    altered[0] = 1;
    await expect(capRdkitWasm(altered)).rejects.toThrow();
    const module = await WebAssembly.compile(capped);
    expect(WebAssembly.Module.exports(module).some((x) => x.kind === 'memory')).toBe(true);
    const init = createRequire(import.meta.url)('@rdkit/rdkit');
    let capEnforced = false;
    const kit = await init({
      instantiateWasm(
        imports: WebAssembly.Imports,
        receive: (instance: WebAssembly.Instance) => void,
      ) {
        const instance = new WebAssembly.Instance(module, imports);
        const memory = Object.values(instance.exports).find(
          (x) => x instanceof WebAssembly.Memory,
        ) as WebAssembly.Memory;
        try {
          memory.grow(2048);
        } catch (error) {
          capEnforced = error instanceof RangeError;
        }
        receive(instance);
      },
    });
    expect(capEnforced).toBe(true);
    const mol = kit.get_mol('CCO');
    expect(mol.get_smiles()).toBe('CCO');
    mol.delete();
  });
});
