import { app, BrowserWindow, ipcMain } from 'electron';
import { createServer } from 'node:http';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';

const temporary = mkdtempSync(join(tmpdir(), 'lyk-google-disabled-'));
process.env.HOME = temporary;
delete process.env.LYKEION_RENDERER_URL;
app.setPath('appData', join(temporary, 'appdata'));
let desktopGoogleRequests = 0;
const server = createServer((req, res) => {
  if (req.url?.startsWith('/auth/desktop/google/')) desktopGoogleRequests++;
  res.writeHead(req.url === '/api/me' ? 401 : 200, { 'Content-Type': 'application/json' });
  res.end(req.url === '/api/me' ? '{"error":"unauthorized"}' : '[]');
});
async function until(fn) {
  for (let i = 0; i < 200; i++) {
    if (await fn()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error('Timed out waiting for desktop sign-in');
}
server.listen(0, '127.0.0.1', async () => {
  try {
    const origin = `http://127.0.0.1:${server.address().port}`;
    mkdirSync(join(temporary, '.lyk'), { recursive: true });
    writeFileSync(
      join(temporary, '.lyk', 'desktop.json'),
      JSON.stringify({ schemaVersion: 1, apiUrl: origin }),
    );
    await import('../apps/desktop/out/main.js');
    await until(() => BrowserWindow.getAllWindows().length > 0);
    const win = BrowserWindow.getAllWindows()[0];
    await until(() =>
      win.webContents
        .executeJavaScript('Boolean(window.lykeionDesktop?.requestDocument)')
        .catch(() => false),
    );
    await until(() =>
      win.webContents
        .executeJavaScript(
          "[...document.querySelectorAll('button')].some(x => x.textContent.includes('Continue with email'))",
        )
        .catch(() => false),
    );
    assert.equal(
      await win.webContents.executeJavaScript('typeof window.lykeionDesktop.googleSignIn'),
      'undefined',
    );
    assert.equal(
      await win.webContents.executeJavaScript(
        "[...document.querySelectorAll('button')].some(x => x.textContent.includes('Continue with Google'))",
      ),
      false,
    );
    // Duplicate registration would throw if production main had left the native handler available.
    ipcMain.handle('lykeion:google-sign-in', () => {});
    ipcMain.removeHandler('lykeion:google-sign-in');
    assert.equal(desktopGoogleRequests, 0);
    win.destroy();
    await new Promise((resolve) => server.close(resolve));
    rmSync(temporary, { recursive: true, force: true });
    console.log('desktop Google handoff unavailable in main, preload and dashboard');
    app.exit(0);
  } catch (error) {
    console.error(error);
    server.close();
    rmSync(temporary, { recursive: true, force: true });
    app.exit(1);
  }
});
