export function createSmilesWorker() {
  return new Worker(new URL('./smiles.worker.ts', import.meta.url), { type: 'module' });
}
