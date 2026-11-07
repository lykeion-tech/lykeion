import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import electron from 'electron';
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
