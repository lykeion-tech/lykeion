import { app, BrowserWindow, shell } from 'electron';
import { createServer } from 'node:http';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
const temporary = mkdtempSync(join(tmpdir(), 'lyk-google-main-'));
process.env.HOME = temporary;
delete process.env.LYKEION_RENDERER_URL;
app.setPath('appData', join(temporary, 'appdata'));
let started = false,
  exchanges = 0,
  holdExchange = false,
  heldResponse;
const user = {
  id: 'a',
  name: 'A',
  email: 'a@example.org',
  timezone: 'UTC',
  avatar_url: '',
  created_at: '2026-01-01',
  google_sub: 'subject',
  onboarded_at: null,
  career_stage: '',
};
const server = createServer((req, res) => {
  if (req.url === '/auth/desktop/google/start') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    return res.end(JSON.stringify({ ticket: 'T'.repeat(43) }));
  }
  if (req.url?.startsWith('/auth/google?desktop_ticket=')) {
    started = true;
    res.writeHead(200);
    return res.end('Return to app');
  }
  if (req.url === '/auth/desktop/google/exchange') {
    exchanges++;
    if (holdExchange) {
      heldResponse = res;
      return;
    }
    res.writeHead(started ? 200 : 202, { 'Content-Type': 'application/json' });
    return res.end(started ? JSON.stringify({ token: 'fixture.jwt.token', expires_in: 3600 }) : '');
  }
  if (req.url === '/api/me') {
    res.writeHead(req.headers.cookie?.includes('lyk_session=fixture.jwt.token') ? 200 : 401, {
      'Content-Type': 'application/json',
    });
    return res.end(
      JSON.stringify(
        req.headers.cookie?.includes('lyk_session=fixture.jwt.token')
          ? user
          : { error: 'unauthorized' },
      ),
    );
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end('[]');
});
async function until(fn, msg) {
  for (let i = 0; i < 200; i++) {
    if (await fn()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error('Timed out: ' + msg);
}
server.listen(0, '127.0.0.1', async () => {
  const origin = `http://127.0.0.1:${server.address().port}`;
  mkdirSync(join(temporary, '.lyk'), { recursive: true });
  writeFileSync(
    join(temporary, '.lyk', 'desktop.json'),
    JSON.stringify({ schemaVersion: 1, apiUrl: origin }),
  );
  shell.openExternal = async (url) => {
    await fetch(url);
  };
  try {
    await import('../apps/desktop/out/main.js');
    await until(() => BrowserWindow.getAllWindows().length > 0, 'window');
    const win = BrowserWindow.getAllWindows()[0];
    await until(
      async () =>
        win.webContents
          .executeJavaScript(
            "[...document.querySelectorAll('button')].some(x=>x.textContent.includes('Continue with Google'))",
          )
          .catch(() => false),
      'Google button',
    );
    const before = await win.webContents.executeJavaScript('window.lykeionDesktop.requestDocument');
    await win.webContents.executeJavaScript(
      "[...document.querySelectorAll('button')].find(x=>x.textContent.includes('Continue with Google')).click()",
    );
    await until(() => started && exchanges > 0, 'Google exchange');
    await until(
      async () =>
        (await win.webContents.session.cookies.get({ name: 'lyk_session' })).some(
          (c) => c.value === 'fixture.jwt.token',
        ),
      'session cookie',
    );
    await until(
      async () =>
        win.webContents
          .executeJavaScript('window.lykeionDesktop.requestDocument')
          .then((d) => d && d !== before)
          .catch(() => false),
      'dashboard document reload',
    );
    await until(
      async () =>
        win.webContents
          .executeJavaScript(
            '!document.querySelector(\'dialog[aria-label="Verify your account"]\')',
          )
          .catch(() => false),
      'verification unlock',
    );
    assert.equal(
      await win.webContents.executeJavaScript(
        "JSON.stringify(window.lykeionDesktop).includes('fixture.jwt.token')",
      ),
      false,
    );
    holdExchange = true;
    const priorDocument = await win.webContents.executeJavaScript(
      'window.lykeionDesktop.requestDocument',
    );
    await win.webContents.executeJavaScript(
      'window.pendingGoogle = window.lykeionDesktop.googleSignIn().catch(() => {}); true',
    );
    await until(() => !!heldResponse, 'held second exchange');
    win.webContents.reload();
    await until(
      async () =>
        win.webContents
          .executeJavaScript('window.lykeionDesktop.requestDocument')
          .then((d) => d && d !== priorDocument)
          .catch(() => false),
      'interrupted document replacement',
    );
    heldResponse.writeHead(200, { 'Content-Type': 'application/json' });
    heldResponse.end(JSON.stringify({ token: 'unexpected.other.account', expires_in: 3600 }));
    await new Promise((resolve) => setTimeout(resolve, 500));
    const cookies = await win.webContents.session.cookies.get({ name: 'lyk_session' });
    assert.equal(cookies.length, 1);
    assert.equal(cookies[0].value, 'fixture.jwt.token');

    console.log(
      'production main Google IPC, session write, reload and interrupted exchange passed',
    );
    win.destroy();
    await new Promise((resolve) => server.close(resolve));
    rmSync(temporary, { recursive: true, force: true });
    app.exit(0);
  } catch (error) {
    console.error(error);
    server.close();
    rmSync(temporary, { recursive: true, force: true });
    app.exit(1);
  }
});
