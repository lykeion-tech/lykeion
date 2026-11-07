import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import electron from 'electron';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { runFixture } from '../scripts/test-desktop-electron.mjs';

function stopped(pid) {
  try {
    process.kill(pid, 0);
  } catch (error) {
    if (error.code === 'ESRCH') return true;
    throw error;
  }
  if (process.platform === 'linux') {
    try {
      const stat = readFileSync(`/proc/${pid}/stat`, 'utf8');
      return stat.slice(stat.lastIndexOf(')') + 2).startsWith('Z ');
    } catch (error) {
      if (error.code === 'ENOENT') return true;
      throw error;
    }
  }
  return false;
}

async function expectStopped(pid) {
  for (let i = 0; i < 100; i++) {
    if (stopped(pid)) return;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  assert.fail(`fixture process ${pid} survived termination`);
}

assert.equal(stopped(process.pid), false, 'a live process must not be treated as stopped');

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
  for (const pid of pids) await expectStopped(pid);
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
    for (const pid of pids) await expectStopped(pid);
    console.log('interrupted Electron fixture and child terminated; temporary profile removed');
  } finally {
    if (runner.exitCode === null) runner.kill('SIGKILL');
    rmSync(root, { recursive: true, force: true });
  }
}
