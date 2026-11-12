import assert from 'node:assert/strict';
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { tmpdir } from 'node:os';
import * as asar from '@electron/asar';
import ts from 'typescript';
import { runFixture } from '../scripts/test-desktop-electron.mjs';

if (process.platform !== 'darwin') throw Error('Packaged held-stream fixture requires macOS');
const temp = mkdtempSync(path.join(tmpdir(), 'lyk-held-packaged-'));
let safeToRemove = true;
try {
  const root = process.cwd(), input = path.join(temp, 'source'), privateDir = path.join(temp, 'private');
  mkdirSync(input, { mode: 0o700 }); mkdirSync(privateDir, { mode: 0o700 });
  writeFileSync(path.join(input, 'package.json'), JSON.stringify({ name: 'held-fixture', version: '1.0.0', main: 'main.cjs' }));
  cpSync(path.join(root, 'tests/fixtures/desktop-production-held-packaged.cjs'), path.join(input, 'main.cjs'));
  const source = readFileSync(path.join(root, 'apps/desktop/src/desktop-held-stream.ts'), 'utf8');
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  writeFileSync(path.join(input, 'desktop-held-stream.cjs'), compiled);
  const verifierSource = readFileSync(path.join(root, 'apps/desktop/src/packaged-daemon.ts'), 'utf8');
  writeFileSync(path.join(input, 'packaged-daemon.js'), ts.transpileModule(verifierSource, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText);
  const fake = readFileSync(path.join(root, 'tests/fixtures/desktop-fake-stream-helper.cjs'), 'utf8');
  for (const mode of ['good', 'bad']) {
    const bin = path.join(privateDir, mode, 'bin'); mkdirSync(bin, { recursive: true, mode: 0o700 });
    const name = path.join(bin, 'lyk');
    const code = fake.replace("const mode = process.argv[2];", `const mode = ${JSON.stringify(mode)}; require('node:fs').fstatSync(3);`);
    writeFileSync(name, `#!${process.execPath}\n${code}`); chmodSync(name, 0o700);
  }
  const bundle = path.join(temp, 'Electron.app');
  cpSync(path.join(root, 'node_modules/electron/dist/Electron.app'), bundle, { recursive: true, verbatimSymlinks: true });
  await asar.createPackage(input, path.join(bundle, 'Contents/Resources/app.asar'));
  const executable = path.join(bundle, 'Contents/MacOS/LykeionHeldFixture');
  renameSync(path.join(bundle, 'Contents/MacOS/Electron'), executable);
  execFileSync('/usr/bin/plutil', ['-replace', 'CFBundleExecutable', '-string', 'LykeionHeldFixture', path.join(bundle, 'Contents/Info.plist')]);
  execFileSync('/usr/bin/codesign', ['--force', '--deep', '--sign', '-', bundle], { stdio: 'ignore' });
  const report = path.join(temp, 'report.json');
  const result = await runFixture(executable, [], { PATH: '/usr/bin:/bin', HOME: privateDir, TMPDIR: temp, LYK_TEST_PRIVATE: privateDir, LYK_TEST_REPORT: report }, 30000);
  safeToRemove = !result.treeUncertain;
  assert.equal(result.timedOut, false); assert.ok(existsSync(report));
  const observed = JSON.parse(readFileSync(report, 'utf8'));
  assert.equal(result.code, 0, `${JSON.stringify(result)} ${JSON.stringify(observed)}`);
  assert.deepEqual(observed, { packaged: true, good: true, badRejected: true, disabledVerifier: true });
  console.log('Packaged production held-stream fixture passed');
} finally { if (safeToRemove) rmSync(temp, { recursive: true, force: true }); }
