import { app, BrowserWindow, ipcMain, session } from 'electron';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
const root = process.cwd();
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'lyk-links-'));
app.setPath('userData', path.join(temp, 'profile'));
app.whenReady().then(async () => {
  const origin = 'http://127.0.0.1:39001';
  const route = await import('file://' + root + '/apps/desktop/out/renderer-origin.js');
  fs.writeFileSync(
    path.join(temp, 'index.html'),
    `<body>
  <a id="citation" href="https://example.org/paper" target="_blank" style="position:absolute;left:10px;top:10px;width:200px;height:40px">Paper</a>
  <a id="mail" href="mailto:researcher@example.org" target="_blank" style="position:absolute;left:10px;top:60px;width:200px;height:40px">Email</a>
  <a id="same" href="${origin}/documentation" target="_blank" style="position:absolute;left:10px;top:110px;width:200px;height:40px">Same origin</a>
  <a id="internal" href="#/studies" style="position:absolute;left:10px;top:160px;width:200px;height:40px">Internal</a>
  </body>`,
  );
  const cancellation = route.installDesktopRenderer(session.defaultSession, origin, temp);
  const messages = [];
  ipcMain.on('lykeion:open-external', (_e, url) => messages.push(url));
  const w = new BrowserWindow({
    show: false,
    width: 400,
    height: 350,
    webPreferences: {
      preload: path.join(root, 'apps/desktop/out/preload.cjs'),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  ipcMain.on('lykeion:document-start', (event) => {
    event.returnValue = route.trustedDesktopFrame(event, w.webContents, origin)
      ? cancellation.beginDocument()
      : null;
  });
  w.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  w.webContents.on('will-navigate', (event) => event.preventDefault());
  await w.loadURL(route.desktopEntryURL(origin));
  await w.webContents.executeJavaScript("document.querySelector('#citation').click()");
  await new Promise((r) => setTimeout(r, 100));
  const synthetic = messages.length;
  for (const y of [25, 75, 125, 175]) {
    w.webContents.sendInputEvent({ type: 'mouseDown', x: 50, y, button: 'left', clickCount: 1 });
    w.webContents.sendInputEvent({ type: 'mouseUp', x: 50, y, button: 'left', clickCount: 1 });
    await new Promise((r) => setTimeout(r, 150));
  }
  const hash = await w.webContents.executeJavaScript('location.hash');
  console.log(JSON.stringify({ synthetic, messages, hash }));
  const ok =
    synthetic === 0 &&
    messages.length === 3 &&
    messages[0] === 'https://example.org/paper' &&
    messages[1] === 'mailto:researcher@example.org' &&
    messages[2] === origin + '/documentation' &&
    hash === '#/studies';
  w.destroy();
  fs.rmSync(temp, { recursive: true, force: true });
  app.exit(ok ? 0 : 1);
});
