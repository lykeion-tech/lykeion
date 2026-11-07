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
    const { DesktopGoogleLogin, bindGoogleDocumentLifetime } =
      await import('../apps/desktop/out/desktop-google-login.js');
    const { prepareGoogleExchange } =
      await import('../apps/desktop/out/desktop-google-transport.js');
    const { installDesktopRenderer, desktopEntryURL } =
      await import('../apps/desktop/out/renderer-origin.js');
    let challenge,
      code,
      exchanges = 0,
      slowLogout = false,
      logoutEntered = false,
      releaseLogout;
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
        assert.equal(req.headers.origin, undefined);
        assert.notEqual(req.headers['sec-fetch-mode'], 'navigate');
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
        if (slowLogout) {
          logoutEntered = true;
          await new Promise((resolve) => {
            releaseLogout = resolve;
          });
        }
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
    const gate = new DesktopAuthGate();
    const login = new DesktopGoogleLogin(origin, gate, {
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
        start: async (id) => {
          starts++;
          login.cancel();
          await gate.mutation(id);
        },
        finish: (id) => {
          finishes++;
          gate.finishMutation(id);
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
        assert.equal(starts, 2);
        assert.ok(finishes >= 1);
        slowLogout = true;
        await window.webContents.executeJavaScript(
          "void fetch('/auth/logout', {method:'POST'}).catch(() => {}); true",
        );
        for (let i = 0; i < 100 && !logoutEntered; i++)
          await new Promise((resolve) => setTimeout(resolve, 10));
        assert.equal(logoutEntered, true);
        await window.loadURL(desktopEntryURL(origin));
        await new Promise((resolve) => setTimeout(resolve, 100));
        assert.throws(() => gate.begin());
        releaseLogout();
        for (let i = 0; i < 100; i++) {
          try {
            gate.begin();
            break;
          } catch {
            await new Promise((resolve) => setTimeout(resolve, 10));
          }
        }
        assert.doesNotThrow(() => gate.begin());
        let callbackURL = '';
        const crashingLogin = new DesktopGoogleLogin(origin, gate, {
          open: async (url) => {
            const u = new URL(url);
            callbackURL = `http://127.0.0.1:${u.searchParams.get('desktop_port')}/callback`;
          },
          exchange: async () => {
            throw new Error('crashed renderer must not exchange');
          },
        });
        bindGoogleDocumentLifetime(
          window.webContents,
          () => true,
          () => crashingLogin.cancel(),
        );
        const pending = crashingLogin.start().then(
          () => false,
          () => true,
        );
        for (let i = 0; i < 100 && !callbackURL; i++)
          await new Promise((resolve) => setTimeout(resolve, 10));
        assert.notEqual(callbackURL, '');
        const crashed = new Promise((resolve) =>
          window.webContents.once('render-process-gone', resolve),
        );
        window.webContents.forcefullyCrashRenderer();
        await crashed;
        assert.equal(await pending, true);
        await assert.rejects(fetch(callbackURL));
        assert.equal(
          (await target.cookies.get({ url: origin, name: 'lyk_session' }))[0].value,
          'fixture-session',
        );
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
