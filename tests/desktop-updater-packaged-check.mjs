import assert from 'node:assert/strict';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import * as asar from '@electron/asar';
import ts from 'typescript';
import { buildDesktopUpdater } from '../scripts/desktop-updater-bundle.mjs';
import { runFixture } from '../scripts/test-desktop-electron.mjs';

if (process.platform !== 'darwin') throw new Error('Packaged updater fixture requires macOS');
const temp = mkdtempSync(path.join(tmpdir(), 'lyk-packaged-updater-'));
const downloadProbe = process.argv.includes('--download-probe');
let safeToRemove = true;
try {
  const root = process.cwd();
  const input = path.join(temp, 'source');
  const privateDir = path.join(temp, 'private');
  mkdirSync(input, { mode: 0o700 });
  mkdirSync(privateDir, { mode: 0o700 });
  writeFileSync(path.join(input, 'package.json'), JSON.stringify({ name: 'lykeion-updater-fixture', version: '1.0.0', main: 'main.cjs' }));
  cpSync(path.join(root, 'tests/fixtures', downloadProbe ? 'desktop-updater-download-probe.cjs' : 'desktop-updater-packaged.cjs'), path.join(input, 'main.cjs'));
  if (downloadProbe) {
    cpSync(path.join(root, 'tests/fixtures/desktop-held-stream.cjs'), path.join(input, 'desktop-held-stream.cjs'));
    cpSync(path.join(root, 'tests/fixtures/desktop-fake-stream-helper.cjs'), path.join(privateDir, 'fake-helper.cjs'));
    const compilerOptions = { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true };
    for (const [source, target] of [['desktop-held-stream.ts', 'production-held.cjs'], ['packaged-daemon.ts', 'packaged-daemon.js']]) {
      const code = readFileSync(path.join(root, 'apps/desktop/src', source), 'utf8');
      writeFileSync(path.join(input, target), ts.transpileModule(code, { compilerOptions }).outputText);
    }
    execFileSync('go', ['test', '-c', '-o', path.join(privateDir, 'go-stream-probe'), './internal/desktopupdate'], {
      cwd: path.join(root, 'server'), env: { ...process.env, GOFLAGS: '', GOWORK: 'off', GOCACHE: '/tmp/lykeion-desktop-go-cache' }, timeout: 30000, stdio: 'ignore',
    });
  }
  await buildDesktopUpdater(root, path.join(input, 'electron-updater.cjs'));
  const app = path.join(temp, 'Electron.app');
  cpSync(path.join(root, 'node_modules/electron/dist/Electron.app'), app, { recursive: true, verbatimSymlinks: true });
  await asar.createPackage(input, path.join(app, 'Contents/Resources/app.asar'));
  const executable = path.join(app, 'Contents/MacOS/LykeionUpdaterFixture');
  renameSync(path.join(app, 'Contents/MacOS/Electron'), executable);
  execFileSync('/usr/bin/plutil', ['-replace', 'CFBundleExecutable', '-string', 'LykeionUpdaterFixture', path.join(app, 'Contents/Info.plist')]);
  execFileSync('/usr/bin/codesign', ['--force', '--deep', '--sign', '-', app], { stdio: 'ignore' });
  const report = path.join(temp, 'result.json');
  const env = { PATH: '/usr/bin:/bin:/usr/sbin:/sbin', HOME: privateDir, LYK_TEST_REPORT: report, LYK_TEST_PRIVATE: privateDir, LYK_TEST_NODE: process.execPath, TMPDIR: temp, TMP: temp, TEMP: temp };
  const result = await runFixture(executable, [], env, 30_000);
  safeToRemove = !result.treeUncertain;
  assert.equal(result.timedOut, false, `fixture timed out: ${existsSync(report) ? readFileSync(report, 'utf8') : 'no report'}`);
  assert.ok(existsSync(report));
  const observed = JSON.parse(readFileSync(report, 'utf8'));
  assert.equal(result.code, 0, `fixture terminated: ${JSON.stringify(result)}, result: ${JSON.stringify(observed)}`);
  assert.deepEqual(observed, downloadProbe ? {
    badSignatureRejected: true, signatureSelfCheck: true, badBearerRejected: true,
    remoteBlocked: true, guards: true, redirectBlocked: true, singleFlight: true, canceled: true, badStreamsNeverComplete: true,
    realGoInvalidRejected: true, realGoLivenessClosed: true, realModuleMetadataCanceled: true, realGoMutationRejected: true, realGoCancelReaped: true, exactBytes: true,
    tamperNotNotified: true, eventBeforeRecheck: true, noNativeInstall: true, exactRequests: true, noSecretLogs: true, packaged: true, cleanupDone: true, failed: false,
  } : {
    packaged: true, adapter: true, executor: true, autoDownload: false,
    autoInstallOnAppQuit: false, allowDowngrade: false, cache: true,
  });
  console.log('Packaged Electron updater fixture passed');
} finally {
  if (safeToRemove) rmSync(temp, { recursive: true, force: true });
}
