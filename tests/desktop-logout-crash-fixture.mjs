import { app, session } from 'electron';
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

const origin = 'https://lab.example';
const [, , phase, profile] = process.argv;
if (profile) app.setPath('userData', profile);

async function runChild(mode, directory) {
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), mode, directory], {
    stdio: 'inherit',
    env: process.env,
  });
  return new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', (code, signal) => resolve({ code, signal }));
  });
}

app.whenReady().then(async () => {
  try {
    if (process.platform === 'win32') {
      app.quit();
      return;
    }
    if (!phase) {
      const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-restart-'));
      try {
        assert.equal((await runChild('write', directory)).signal, 'SIGKILL');
        assert.equal((await runChild('recover', directory)).code, 0);
      } finally {
        rmSync(directory, { recursive: true, force: true });
      }
    } else {
      const { LogoutIntent } = await import('../apps/desktop/out/desktop-logout-intent.js');
      const { recoverLogoutIntent } = await import('../apps/desktop/out/desktop-auth-logout.js');
      const target = session.fromPartition('persist:logout-crash-fixture');
      const intent = new LogoutIntent(profile, origin);
      if (phase === 'write') {
        await target.cookies.set({
          url: origin,
          name: 'lyk_session',
          value: 'fixture',
          secure: true,
          expirationDate: Math.floor(Date.now() / 1000) + 3600,
        });
        await target.cookies.flushStore();
        intent.mark();
        process.kill(process.pid, 'SIGKILL');
      }
      assert.equal((await target.cookies.get({ name: 'lyk_session' })).length, 1);
      await recoverLogoutIntent(intent, target, origin);
      assert.equal((await target.cookies.get({ name: 'lyk_session' })).length, 0);
      assert.equal(intent.exists(), false);
    }
    app.quit();
  } catch (error) {
    console.error(error);
    app.exit(1);
  }
});
