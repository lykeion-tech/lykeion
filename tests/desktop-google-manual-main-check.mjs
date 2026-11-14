import {
  cpSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
import electron from 'electron';
import { runFixture } from '../scripts/test-desktop-electron.mjs';
const root = mkdtempSync(join(tmpdir(), 'lyk-manual-main-copy-'));
const sourceFiles = ['apps/desktop/out/main.js', 'apps/desktop/out/preload.cjs'];
const sourceHashes = sourceFiles.map((file) =>
  createHash('sha256').update(readFileSync(file)).digest('hex'),
);
let safe = true;
try {
  mkdirSync(join(root, 'tests'), { recursive: true });
  cpSync(resolve('tests/desktop-google-manual-main-fixture.mjs'), join(root, 'tests/test.mjs'));
  mkdirSync(join(root, 'apps/desktop'), { recursive: true });
  cpSync(resolve('apps/desktop/out'), join(root, 'apps/desktop/out'), { recursive: true });
  mkdirSync(join(root, 'dist/dashboard'), { recursive: true });
  writeFileSync(
    join(root, 'dist/dashboard/index.html'),
    '<!doctype html><title>Manual test</title>',
  );
  writeFileSync(join(root, 'package.json'), '{"type":"module"}');
  symlinkSync(resolve('node_modules'), join(root, 'node_modules'), 'dir');
  const main = join(root, 'apps/desktop/out/main.js');
  let content = readFileSync(main, 'utf8');
  const old = 'function desktopGoogleReleaseReady() {\n    return false;\n}';
  if (content.split(old).length !== 2) throw new Error('Expected exact disabled main guard');
  writeFileSync(
    main,
    content.replace(old, 'function desktopGoogleReleaseReady() {\n    return true;\n}'),
  );
  const preload = join(root, 'apps/desktop/out/preload.cjs');
  content = readFileSync(preload, 'utf8');
  const flag = 'const manualGoogleEnabled = false;';
  if (content.split(flag).length !== 2) throw new Error('Expected exact disabled preload guard');
  writeFileSync(preload, content.replace(flag, 'const manualGoogleEnabled = true;'));
  const env = { ...process.env, LYK_MANUAL_TEST_ROOT: root };
  delete env.ELECTRON_RUN_AS_NODE;
  const result = await runFixture(electron, [join(root, 'tests/test.mjs')], env, 60_000);
  if (result.treeUncertain) safe = false;
  if (result.code !== 0 || result.treeUncertain) throw new Error(JSON.stringify(result));
  for (const [index, file] of sourceFiles.entries()) {
    const current = createHash('sha256').update(readFileSync(file)).digest('hex');
    if (current !== sourceHashes[index])
      throw new Error('Source output changed by manual-code test');
  }
} finally {
  if (safe) rmSync(root, { recursive: true, force: true });
}
