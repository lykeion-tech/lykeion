import { app, BrowserWindow, session } from 'electron';
import https from 'node:https';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

const root = process.cwd();
const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'lyk-tls-'));
app.setPath('userData', path.join(directory, 'profile'));
const key = path.join(directory, 'key.pem');
const cert = path.join(directory, 'cert.pem');
const made = spawnSync(
  'openssl',
  [
    'req',
    '-x509',
    '-newkey',
    'rsa:2048',
    '-nodes',
    '-keyout',
    key,
    '-out',
    cert,
    '-subj',
    '/CN=localhost',
    '-days',
    '1',
  ],
  { stdio: 'ignore' },
);
if (made.status !== 0) {
  fs.rmSync(directory, { recursive: true, force: true });
  app.exit(1);
}
app.whenReady().then(async () => {
  const route = await import('file://' + root + '/apps/desktop/out/renderer-origin.js');
  let reached = 0;
  const server = https.createServer(
    { key: fs.readFileSync(key), cert: fs.readFileSync(cert) },
    (_q, s) => {
      reached++;
      s.end('unexpected');
    },
  );
  server.listen(0, '127.0.0.1', async () => {
    const origin = 'https://127.0.0.1:' + server.address().port;
    route.installDesktopRenderer(session.defaultSession, origin, root + '/dist/dashboard');
    const window = new BrowserWindow({
      show: false,
      webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false },
    });
    let rejected = false;
    try {
      await window.loadURL(route.desktopEntryURL(origin));
      rejected = await window.webContents.executeJavaScript(
        "fetch('/api/file').then(()=>false,()=>true)",
      );
      console.log(JSON.stringify({ rejected, reached }));
    } catch (error) {
      console.error(error);
    }
    window.destroy();
    server.close();
    fs.rmSync(directory, { recursive: true, force: true });
    app.exit(rejected && reached === 0 ? 0 : 1);
  });
});
