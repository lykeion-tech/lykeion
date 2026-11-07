import { mkdirSync, rmSync } from 'node:fs';
import path from 'node:path';
import electron from 'electron';
import { runFixture } from '../scripts/test-desktop-electron.mjs';

const directory = path.join(process.env.LYK_INTERRUPT_ROOT, 'fixture-profile');
mkdirSync(directory);
try {
  const env = { ...process.env, TMPDIR: directory, TMP: directory, TEMP: directory };
  delete env.ELECTRON_RUN_AS_NODE;
  const result = await runFixture(electron, ['tests/desktop-electron-hang-fixture.mjs'], env);
  if (result.treeUncertain) throw new Error('Could not terminate fixture process tree');
  process.exitCode =
    result.interrupted === 'SIGTERM' ? 143 : result.interrupted === 'SIGINT' ? 130 : 1;
} finally {
  rmSync(directory, { recursive: true, force: true });
}
