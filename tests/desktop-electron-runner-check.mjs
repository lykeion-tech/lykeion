import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import electron from 'electron';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { runFixture } from '../scripts/test-desktop-electron.mjs';

const directory = mkdtempSync(path.join(tmpdir(), 'lyk-runner-check-'));
try {
  const marker = path.join(directory, 'pids.json');
  const env = {
    ...process.env,
    TMPDIR: directory,
    TMP: directory,
    TEMP: directory,
    LYK_HANG_FIXTURE_PIDS: marker,
  };
  delete env.ELECTRON_RUN_AS_NODE;
  const result = await runFixture(
    electron,
    ['tests/desktop-electron-hang-fixture.mjs'],
    env,
    10000,
  );
  assert.equal(result.timedOut, true);
  assert.ok(existsSync(marker), 'hung Electron fixture must actually start');
  const pids = JSON.parse(readFileSync(marker, 'utf8'));
  for (const pid of pids) {
    let alive = true;
    for (let i = 0; i < 50 && alive; i++) {
      try {
        process.kill(pid, 0);
        await new Promise((resolve) => setTimeout(resolve, 20));
      } catch (error) {
        if (error.code !== 'ESRCH') throw error;
        alive = false;
      }
    }
    assert.equal(alive, false, `fixture process ${pid} survived timeout`);
  }
  console.log('hung Electron fixture and child terminated');
} finally {
  rmSync(directory, { recursive: true, force: true });
}

for (const signal of process.platform === 'win32' ? [] : ['SIGINT', 'SIGTERM']) {
  const root = mkdtempSync(path.join(tmpdir(), 'lyk-runner-interrupt-'));
  const marker = path.join(root, 'pids.json');
  const runner = spawn(process.execPath, ['tests/desktop-electron-interrupt-check-child.mjs'], {
    stdio: 'inherit',
    env: { ...process.env, LYK_INTERRUPT_ROOT: root, LYK_HANG_FIXTURE_PIDS: marker },
  });
  const exited = once(runner, 'exit');
  try {
    for (let i = 0; i < 250 && !existsSync(marker); i++) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    assert.ok(existsSync(marker), 'interrupt fixture must actually start');
    const pids = JSON.parse(readFileSync(marker, 'utf8'));
    runner.kill(signal);
    const [code] = await exited;
    assert.equal(code, signal === 'SIGINT' ? 130 : 143);
    assert.equal(existsSync(path.join(root, 'fixture-profile')), false);
    for (const pid of pids) {
      assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' });
    }
    console.log('interrupted Electron fixture and child terminated; temporary profile removed');
  } finally {
    if (runner.exitCode === null) runner.kill('SIGKILL');
    rmSync(root, { recursive: true, force: true });
  }
}
