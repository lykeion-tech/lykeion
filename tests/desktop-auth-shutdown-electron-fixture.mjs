import { app, BrowserWindow, session } from 'electron';
import { createServer } from 'node:http';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';
const profile = mkdtempSync(join(tmpdir(), 'lyk-auth-quit-'));
app.setPath('userData', profile);
app
  .whenReady()
  .then(async () => {
    const { DesktopAuthGate } = await import('../apps/desktop/out/desktop-auth-gate.js');
    const { installDesktopRenderer, desktopEntryURL } =
      await import('../apps/desktop/out/renderer-origin.js');
    let entered = false,
      release;
    const server = createServer(async (req, res) => {
      if (req.url !== '/auth/logout') {
        res.writeHead(404);
        res.end();
        return;
      }
      entered = true;
      await new Promise((resolve) => {
        release = resolve;
      });
      res.writeHead(204, {
        'Set-Cookie': 'lyk_session=; Path=/; HttpOnly; Max-Age=0; SameSite=Lax',
      });
      res.end();
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const target = session.fromPartition('auth-quit');
    const gate = new DesktopAuthGate();
    writeFileSync(join(profile, 'index.html'), '<!doctype html><title>fixture</title>');
    installDesktopRenderer(target, origin, profile, {
      start: (id) => gate.mutation(id),
      finish: (id) => gate.finishMutation(id),
    });
    const window = new BrowserWindow({
      show: false,
      webPreferences: { session: target, sandbox: true },
    });
    try {
      await target.cookies.set({ url: origin, name: 'lyk_session', value: 'old', httpOnly: true });
      await window.loadURL(desktopEntryURL(origin));
      await window.webContents.executeJavaScript(
        "void fetch('/auth/logout',{method:'POST'}).catch(()=>{}); true",
      );
      for (let i = 0; i < 100 && !entered; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(entered, true);
      await window.loadURL(desktopEntryURL(origin));
      let settled = false;
      const shutdown = gate
        .shutdown(async () => {
          throw new Error('daemon failed');
        })
        .then((result) => {
          settled = true;
          return result;
        });
      await new Promise((resolve) => setTimeout(resolve, 100));
      assert.equal(settled, false);
      await assert.rejects(gate.mutation(999));
      release();
      const result = await shutdown;
      assert.equal(result[0].status, 'fulfilled');
      assert.equal(result[1].status, 'rejected');
      await target.cookies.flushStore();
      assert.equal((await target.cookies.get({ url: origin, name: 'lyk_session' })).length, 0);
      console.log('desktop authentication shutdown fixture passed');
    } finally {
      window.destroy();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
      await target.clearStorageData();
      app.quit();
      rmSync(profile, { recursive: true, force: true });
    }
  })
  .catch((error) => {
    console.error(error);
    app.exit(1);
  });
