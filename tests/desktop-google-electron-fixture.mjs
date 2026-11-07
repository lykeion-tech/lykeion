import { app, BrowserWindow, session } from 'electron';
import { createServer } from 'node:http';
import { createHash, randomBytes } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import assert from 'node:assert/strict';
const profile = mkdtempSync(join(tmpdir(), 'lyk-google-fixture-'));
app.setPath('userData', profile);
app
  .whenReady()
  .then(async () => {
    const { DesktopAuthGate } = await import('../apps/desktop/out/desktop-auth-gate.js');
    const { DesktopGoogleLogin } = await import('../apps/desktop/out/desktop-google-login.js');
    const { prepareGoogleExchange } =
      await import('../apps/desktop/out/desktop-google-transport.js');
    const { installDesktopRenderer, desktopEntryURL } =
      await import('../apps/desktop/out/renderer-origin.js');
    let challenge,
      code,
      exchanges = 0;
    const server = createServer(async (req, res) => {
      const u = new URL(req.url, 'http://localhost');
      if (u.pathname === '/auth/google') {
        challenge = u.searchParams.get('desktop_challenge');
        code = randomBytes(32).toString('base64url');
        const fragment = new URLSearchParams({ code, state: u.searchParams.get('desktop_state') });
        res.writeHead(302, {
          Location: `http://127.0.0.1:${u.searchParams.get('desktop_port')}/callback#${fragment}`,
        });
        res.end();
      } else if (u.pathname === '/auth/desktop/exchange') {
        let body = '';
        for await (const chunk of req) body += chunk;
        const input = JSON.parse(body);
        if (
          !code ||
          input.code !== code ||
          createHash('sha256').update(input.verifier).digest('base64url') !== challenge
        ) {
          res.writeHead(401);
          res.end();
          return;
        }
        code = null;
        exchanges++;
        res.writeHead(204, {
          'Set-Cookie': 'lyk_session=fixture-session; Path=/; HttpOnly; SameSite=Lax',
        });
        res.end();
      } else if (u.pathname === '/auth/logout') {
        res.writeHead(204);
        res.end();
      } else {
        res.writeHead(404);
        res.end();
      }
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const target = session.fromPartition('google-target');
    const other = session.fromPartition('google-other');
    const login = new DesktopGoogleLogin(origin, new DesktopAuthGate(), {
      open: async (url) => {
        const auth = await fetch(url, { redirect: 'manual' });
        const callback = new URL(auth.headers.get('location'));
        const params = new URLSearchParams(callback.hash.slice(1));
        callback.hash = '';
        assert.equal((await fetch(callback)).status, 200);
        const result = await fetch(callback, {
          method: 'POST',
          headers: { origin: callback.origin, 'content-type': 'application/json' },
          body: JSON.stringify({ state: params.get('state'), code: params.get('code') }),
        });
        assert.equal(result.status, 200);
      },
      exchange: (code, verifier, signal) =>
        prepareGoogleExchange(origin, target, code, verifier, signal),
    });
    try {
      await login.start();
      assert.equal(exchanges, 1);
      const cookies = await target.cookies.get({ url: origin, name: 'lyk_session' });
      assert.equal(cookies.length, 1);
      assert.equal(cookies[0].value, 'fixture-session');
      assert.equal(cookies[0].httpOnly, true);
      assert.equal((await other.cookies.get({ url: origin, name: 'lyk_session' })).length, 0);
      let starts = 0,
        finishes = 0;
      writeFileSync(join(profile, 'index.html'), '<!doctype html><title>fixture</title>');
      installDesktopRenderer(target, origin, profile, {
        start: async () => {
          starts++;
        },
        finish: () => {
          finishes++;
        },
      });
      const window = new BrowserWindow({
        show: false,
        webPreferences: { session: target, sandbox: true },
      });
      try {
        await window.loadURL(desktopEntryURL(origin));
        assert.equal(
          await window.webContents.executeJavaScript(
            "fetch('/auth/logout', {method:'POST'}).then(r => r.status)",
          ),
          204,
        );
        await new Promise((resolve) => setTimeout(resolve, 100));
        assert.equal(starts, 1);
        assert.ok(finishes >= 1);
      } finally {
        window.destroy();
      }
      console.log('desktop Google Electron exchange fixture passed');
    } finally {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
      await target.clearStorageData();
      await other.clearStorageData();
      app.quit();
      rmSync(profile, { recursive: true, force: true });
    }
  })
  .catch((error) => {
    console.error(error);
    app.exit(1);
  });
