import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import electron from 'electron';

const fixtures = [
  'desktop-electron-fixture.mjs',
  'desktop-links-fixture.mjs',
  'desktop-auth-shutdown-electron-fixture.mjs',
  'desktop-tls-fixture.mjs',
  'desktop-lifetime-fixture.mjs',
];

export async function runFixture(executable, args, env, timeoutMs = 120_000) {
  const child = spawn(executable, args, {
    stdio: 'inherit',
    env,
    detached: process.platform !== 'win32',
    windowsHide: true,
  });
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    if (child.pid === undefined) return;
    if (process.platform === 'win32') {
      const killer = spawnSync('taskkill', ['/pid', String(child.pid), '/t', '/f'], {
        stdio: 'ignore',
        windowsHide: true,
        timeout: 5000,
      });
      if (killer.error || killer.status !== 0) child.kill('SIGKILL');
    } else {
      try {
        process.kill(-child.pid, 'SIGKILL');
      } catch (error) {
        if (error.code !== 'ESRCH') child.kill('SIGKILL');
      }
    }
  }, timeoutMs);
  try {
    return await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('close', (code, signal) => resolve({ code, signal, timedOut }));
    });
  } finally {
    clearTimeout(timer);
  }
}

async function main() {
  for (const fixture of fixtures) {
    const temporary = mkdtempSync(path.join(tmpdir(), 'lyk-electron-test-'));
    try {
      const env = { ...process.env, TMPDIR: temporary, TMP: temporary, TEMP: temporary };
      delete env.ELECTRON_RUN_AS_NODE;
      const result = await runFixture(electron, [`tests/${fixture}`], env);
      if (result.timedOut) console.error(`${fixture} timed out`);
      if (result.timedOut || result.code !== 0) {
        process.exitCode = result.code || 1;
        break;
      }
    } finally {
      rmSync(temporary, { recursive: true, force: true });
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
