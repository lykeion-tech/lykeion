import { app, BrowserWindow, session } from 'electron';
import http from 'node:http';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { once } from 'node:events';
const root = process.cwd();
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'lyk-lifetime-'));
app.setPath('userData', path.join(temp, 'profile'));
app.on('window-all-closed', () => {});
async function until(fn) {
  for (let i = 0; i < 40; i++) {
    if (fn()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error('Timed out waiting for fixture state');
}
app.whenReady().then(async () => {
  const route = await import('file://' + root + '/apps/desktop/out/renderer-origin.js');
  const state = {
    oldStarted: false,
    oldClosed: false,
    newStarted: false,
    newClosed: false,
    afterStarted: false,
    afterClosed: false,
  };
  const server = http.createServer((req, res) => {
    if (req.url === '/api/old') {
      state.oldStarted = true;
      req.on('close', () => {
        state.oldClosed = true;
      });
    } else if (req.url === '/api/new') {
      state.newStarted = true;
      req.on('close', () => {
        state.newClosed = true;
      });
    } else if (req.url === '/api/after') {
      state.afterStarted = true;
      req.on('close', () => {
        state.afterClosed = true;
      });
    } else {
      res.writeHead(401, { 'Content-Type': 'application/json' });
      res.end('{"error":"unauthorized"}');
    }
  });
  server.listen(0, '127.0.0.1', async () => {
    const origin = 'http://127.0.0.1:' + server.address().port;
    const desktop = session.fromPartition(route.desktopPartition(origin));
    const cancellation = route.installDesktopRenderer(desktop, origin, root + '/dist/dashboard');
    let current = null;
    const make = () => {
      const w = new BrowserWindow({
        show: false,
        webPreferences: {
          session: desktop,
          sandbox: true,
          contextIsolation: true,
          nodeIntegration: false,
        },
      });
      current = w;
      const drain = route.bindDesktopRequestLifetime(
        w.webContents,
        cancellation,
        () => current === w,
      );
      w.on('closed', () => {
        drain();
        if (current === w) current = null;
      });
      return { w, drain };
    };
    try {
      const old = make();
      await old.w.loadURL(route.desktopEntryURL(origin));
      await old.w.webContents.executeJavaScript(
        `fetch('/api/old',{headers:{'X-Lykeion-Desktop-Request':crypto.randomUUID()}}).catch(()=>{});true`,
      );
      await until(() => state.oldStarted);
      old.w.close();
      await until(() => state.oldClosed);
      const next = make();
      await next.w.loadURL(route.desktopEntryURL(origin));
      await next.w.webContents.executeJavaScript(
        `fetch('/api/new',{headers:{'X-Lykeion-Desktop-Request':crypto.randomUUID()}}).catch(()=>{});true`,
      );
      await until(() => state.newStarted);
      const loaded = once(next.w.webContents, 'did-finish-load');
      next.w.webContents.reload();
      await loaded;
      await until(() => state.newClosed);
      await next.w.webContents.executeJavaScript(
        `fetch('/api/after',{headers:{'X-Lykeion-Desktop-Request':crypto.randomUUID()}}).catch(()=>{});true`,
      );
      await until(() => state.afterStarted);
      old.drain();
      await new Promise((r) => setTimeout(r, 250));
      const survived = !state.afterClosed;
      next.w.close();
      await until(() => state.afterClosed);
      console.log(JSON.stringify({ ...state, survived }));
      server.close();
      fs.rmSync(temp, { recursive: true, force: true });
      app.exit(survived ? 0 : 1);
    } catch (error) {
      console.error(error);
      if (current) current.destroy();
      server.close();
      fs.rmSync(temp, { recursive: true, force: true });
      app.exit(1);
    }
  });
});
