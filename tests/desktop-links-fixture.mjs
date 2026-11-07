import { app, BrowserWindow, ipcMain } from 'electron';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
const root = process.cwd();
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'lyk-links-'));
app.setPath('userData', temp);
app.whenReady().then(async () => {
  const file = path.join(temp, 'index.html');
  fs.writeFileSync(
    file,
    '<body><a id="citation" href="https://example.org/paper" target="_blank" style="position:absolute;left:10px;top:10px;width:200px;height:50px">Paper</a></body>',
  );
  const messages = [];
  ipcMain.on('lykeion:open-external', (_e, url) => messages.push(url));
  const w = new BrowserWindow({
    show: false,
    width: 400,
    height: 200,
    webPreferences: {
      preload: path.join(root, 'apps/desktop/out/preload.cjs'),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  w.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  await w.loadFile(file);
  await w.webContents.executeJavaScript("document.querySelector('#citation').click()");
  await new Promise((r) => setTimeout(r, 200));
  const synthetic = messages.length;
  w.webContents.sendInputEvent({ type: 'mouseDown', x: 50, y: 25, button: 'left', clickCount: 1 });
  w.webContents.sendInputEvent({ type: 'mouseUp', x: 50, y: 25, button: 'left', clickCount: 1 });
  await new Promise((r) => setTimeout(r, 500));
  console.log(JSON.stringify({ synthetic, messages }));
  const ok =
    synthetic === 0 && messages.length === 1 && messages[0] === 'https://example.org/paper';
  w.destroy();
  fs.rmSync(temp, { recursive: true, force: true });
  app.exit(ok ? 0 : 1);
});
