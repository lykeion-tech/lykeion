import { app, BrowserWindow, ipcMain, session } from 'electron';
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
      assert.equal(req.headers['x-lykeion-desktop-auth-reservation'], undefined);
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
    ipcMain.on('lykeion:document-start', (event) => {
      event.returnValue = 'fixture-document';
    });
    ipcMain.on('lykeion:reserve-auth', (event, path) => {
      event.returnValue = gate.reserve(path);
    });
    ipcMain.on('lykeion:release-auth', (_event, token) => gate.release(token));
    writeFileSync(join(profile, 'index.html'), '<!doctype html><title>fixture</title>');
    installDesktopRenderer(target, origin, profile, {
      finish: (id) => gate.finishMutation(id),
      continue: (id, token, path) => gate.continueMutation(id, token, path),
    });
    const window = new BrowserWindow({
      show: false,
      webPreferences: {
        session: target,
        sandbox: true,
        preload: join(process.cwd(), 'apps/desktop/out/preload.cjs'),
      },
    });
    try {
      await target.cookies.set({ url: origin, name: 'lyk_session', value: 'old', httpOnly: true });
      await window.loadURL(desktopEntryURL(origin));
      const reservation = await window.webContents.executeJavaScript(
        "window.lykeionDesktop.reserveAuth('/auth/logout')",
      );
      assert.ok(reservation);
      assert.equal(entered, false);
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
      assert.equal(
        await window.webContents.executeJavaScript(
          "window.lykeionDesktop.reserveAuth('/auth/logout')",
        ),
        null,
      );
      await window.webContents.executeJavaScript(
        `void fetch('/auth/logout',{method:'POST',headers:{'X-Lykeion-Desktop-Auth-Reservation':${JSON.stringify(reservation)}}}).catch(()=>{}); true`,
      );
      for (let i = 0; i < 100 && !entered; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(entered, true);
      release();
      const result = await shutdown;
      assert.equal(result[0].status, 'fulfilled');
      assert.equal(result[1].status, 'rejected');
      await target.cookies.flushStore();
      assert.equal((await target.cookies.get({ url: origin, name: 'lyk_session' })).length, 0);
      const abandonedGate = new DesktopAuthGate();
      ipcMain.removeAllListeners('lykeion:reserve-auth');
      ipcMain.on('lykeion:reserve-auth', (event, path) => {
        event.returnValue = abandonedGate.reserve(path);
      });
      await target.cookies.set({ url: origin, name: 'lyk_session', value: 'old', httpOnly: true });
      const abandonedWindow = new BrowserWindow({
        show: false,
        webPreferences: {
          session: target,
          sandbox: true,
          preload: join(process.cwd(), 'apps/desktop/out/preload.cjs'),
        },
      });
      await abandonedWindow.loadURL(desktopEntryURL(origin));
      assert.ok(
        await abandonedWindow.webContents.executeJavaScript(
          "window.lykeionDesktop.reserveAuth('/auth/logout')",
        ),
      );
      abandonedWindow.destroy();
      const abandoned = await abandonedGate.shutdown(async () => {}, 50);
      assert.equal(abandoned[0].status, 'rejected');
      assert.equal(
        (await target.cookies.get({ url: origin, name: 'lyk_session' }))[0].value,
        'old',
      );
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
