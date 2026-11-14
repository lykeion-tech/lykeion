import { app, BrowserWindow, dialog, shell } from 'electron';
import { createServer } from 'node:http';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';

const root = process.env.LYK_MANUAL_TEST_ROOT;
// The parent removes this profile only after the whole Electron process tree exits.
const temporary = join(root, 'profile');
mkdirSync(temporary, { recursive: true });
process.env.HOME = temporary;
delete process.env.LYKEION_RENDERER_URL;
app.setPath('appData', join(temporary, 'appdata'));
let opened = 0,
  exchanges = 0,
  confirmed = 0;
const attackerTicket = 'A'.repeat(43);
const victimTicket = 'A'.repeat(42) + 'E';
const attackerCode = 'A'.repeat(42) + 'I';
const victimCode = 'A'.repeat(42) + 'M';
const challenges = new Map();
const server = createServer(async (req, res) => {
  if (req.url === '/auth/desktop/google/start') {
    let input = '';
    for await (const chunk of req) input += chunk;
    const ticket = challenges.size === 0 ? attackerTicket : victimTicket;
    challenges.set(ticket, JSON.parse(input).challenge);
    res.writeHead(200, { 'Content-Type': 'application/json' });
    return res.end(JSON.stringify({ ticket }));
  }
  if (req.url?.startsWith('/auth/google?desktop_ticket=')) {
    opened++;
    res.writeHead(200);
    return res.end('Browser only code');
  }
  if (req.url === '/auth/desktop/google/exchange') {
    let input = '';
    for await (const chunk of req) input += chunk;
    const data = JSON.parse(input);
    assert.match(data.verifier, /^[A-Za-z0-9_-]{43}$/);
    exchanges++;
    const challenge = createHash('sha256').update(data.verifier).digest('base64url');
    const correct = data.ticket === attackerTicket ? attackerCode : victimCode;
    if (challenges.get(data.ticket) !== challenge || data.code !== correct) {
      res.writeHead(400);
      return res.end();
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    return res.end(
      JSON.stringify({ token: 'fixture.jwt.token', expires_in: 3600, email: 'user@example.org' }),
    );
  }
  if (req.url === '/api/me') {
    const ok = req.headers.cookie?.includes('lyk_session=fixture.jwt.token');
    res.writeHead(ok ? 200 : 401, { 'Content-Type': 'application/json' });
    return res.end(
      JSON.stringify(ok ? { id: 'user-a', email: 'user@example.org' } : { error: 'unauthorized' }),
    );
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end('[]');
});
async function until(fn, message) {
  for (let i = 0; i < 300; i++) {
    if (await fn()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error('Timed out: ' + message);
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
  dialog.showMessageBox = async (_owner, options) => {
    assert.ok(options.detail.includes('user@example.org'));
    assert.ok(options.detail.includes(origin));
    assert.equal(options.defaultId, 0);
    assert.equal(options.cancelId, 0);
    confirmed++;
    return { response: 1, checkboxChecked: false };
  };
  dialog.showErrorBox = () => {
    throw new Error('unexpected sign-in error');
  };
  try {
    const { DesktopGoogleAuth } = await import(
      pathToFileURL(join(root, 'apps/desktop/out/desktop-google-auth.js')).href
    );
    let attackerCookies = 0;
    const attacker = new DesktopGoogleAuth(origin, {
      request: fetch,
      open: async (url) => {
        await fetch(url);
      },
      confirm: async () => true,
      set: async () => {
        attackerCookies++;
      },
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const attackerRun = attacker.run().catch(() => {});
    await until(() => opened === 1, 'attacker link opened in victim browser');
    await import(pathToFileURL(join(root, 'apps/desktop/out/main.js')).href);
    await until(() => BrowserWindow.getAllWindows().length > 0, 'window');
    const win = BrowserWindow.getAllWindows()[0];
    await until(
      () =>
        win.webContents
          .executeJavaScript('Boolean(window.lykeionDesktop?.requestDocument)')
          .catch(() => false),
      'preload',
    );
    assert.equal(
      await win.webContents.executeJavaScript(
        "window.lykeionDesktop.submitGoogleCode('" + victimCode + "')",
      ),
      false,
    );
    const before = await win.webContents.executeJavaScript('window.lykeionDesktop.requestDocument');
    await win.webContents.executeJavaScript(
      'window.googleDone = false; window.googleError = false; window.lykeionDesktop.googleSignIn().then(() => window.googleDone = true, () => window.googleError = true); true',
    );
    await until(() => opened === 2, 'victim browser opened');
    // An attacker cannot exchange their own ticket with the victim app's code.
    assert.equal(await attacker.submitCode(victimCode), false);
    assert.equal(attackerCookies, 0);
    let wrong;
    await until(async () => {
      wrong = await win.webContents.executeJavaScript(
        "window.lykeionDesktop.submitGoogleCode('" + attackerCode + "')",
      );
      return exchanges > 1;
    }, 'attacker-link code rejected by victim app');
    assert.equal(wrong, false);
    assert.equal(exchanges, 2);
    assert.equal((await win.webContents.session.cookies.get({ name: 'lyk_session' })).length, 0);
    const accepted = await win.webContents.executeJavaScript(
      "window.lykeionDesktop.submitGoogleCode('" + victimCode + "')",
    );
    assert.equal(accepted, true);
    await until(
      () => win.webContents.executeJavaScript('window.googleDone || window.googleError'),
      'native flow settled',
    );
    assert.equal(await win.webContents.executeJavaScript('window.googleDone'), true);
    assert.equal(confirmed, 1);
    assert.equal(exchanges, 3);
    attacker.cancel();
    await attackerRun;
    assert.equal(attackerCookies, 0);
    const cookies = await win.webContents.session.cookies.get({ name: 'lyk_session' });
    assert.equal(cookies.length, 1);
    assert.equal(cookies[0].value, 'fixture.jwt.token');
    assert.equal(cookies[0].httpOnly, true);
    assert.equal(
      await win.webContents.executeJavaScript('window.lykeionDesktop.reloadAfterAuthentication()'),
      true,
    );
    await until(async () => {
      const id = await win.webContents
        .executeJavaScript('window.lykeionDesktop?.requestDocument')
        .catch(() => null);
      return id && id !== before;
    }, 'fresh document');
    const verification = await win.webContents.executeJavaScript(
      'window.lykeionDesktop.readVerification()',
    );
    assert.equal(verification.user.id, 'user-a');
    assert.equal(
      await win.webContents.executeJavaScript(
        `window.lykeionDesktop.finishVerification('user-a', ${JSON.stringify(verification.receipt)}, false)`,
      ),
      true,
    );
    console.log(
      'two native flows reject cross-ticket codes; account confirmation, cookie and trusted reload passed',
    );
    win.destroy();
    await new Promise((resolve) => server.close(resolve));
    app.quit();
  } catch (error) {
    console.error(error);
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    app.exit(1);
  }
});
