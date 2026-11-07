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
    const { clearAbandonedLogout } = await import('../apps/desktop/out/desktop-auth-logout.js');
    const { installDesktopRenderer, desktopEntryURL } =
      await import('../apps/desktop/out/renderer-origin.js');
    let entered = false,
      release,
      meCookie = 'not-called',
      staleReceived = false,
      logoutStatus = 204,
      logoutDeletes = true,
      stallLogout = false,
      stalledClosed,
      holdVerification = false,
      verificationEntered,
      releaseVerification;
    const server = createServer(async (req, res) => {
      if (req.url === '/api/me') {
        meCookie = req.headers.cookie || '';
        res.writeHead(200);
        res.end('ok');
        return;
      }
      if (req.url === '/api/stale') {
        staleReceived = true;
        res.writeHead(200);
        res.end('unexpected');
        return;
      }
      if (req.url === '/auth/verify-code' && holdVerification) {
        verificationEntered = true;
        await new Promise((resolve) => {
          releaseVerification = resolve;
        });
        res.writeHead(204, { 'Set-Cookie': 'lyk_session=late; Path=/; HttpOnly; SameSite=Lax' });
        res.end();
        return;
      }
      if (!['/auth/logout', '/auth/verify-code'].includes(req.url)) {
        res.writeHead(404);
        res.end();
        return;
      }
      entered = true;
      assert.equal(req.headers['x-lykeion-desktop-auth-reservation'], undefined);
      assert.equal(req.headers['x-lykeion-desktop-native-auth'], undefined);
      if (stallLogout) {
        res.on('close', () => {
          stalledClosed = true;
        });
        res.writeHead(500);
        res.write(Buffer.alloc(65536));
        return;
      }
      await new Promise((resolve) => {
        release = resolve;
      });
      res.writeHead(
        logoutStatus,
        logoutDeletes
          ? { 'Set-Cookie': 'lyk_session=; Path=/; HttpOnly; Max-Age=0; SameSite=Lax' }
          : {},
      );
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
      networkReady: () => gate.networkReady(),
      networkBlocked: () => gate.networkBlocked(),
      networkEpoch: () => gate.networkEpoch(),
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
      const abandonedSession = session.fromPartition('abandoned-auth');
      installDesktopRenderer(abandonedSession, origin, profile, {
        finish: (id) => abandonedGate.finishMutation(id),
        continue: (id, token, path) => abandonedGate.continueMutation(id, token, path),
        networkReady: () => abandonedGate.networkReady(),
        networkBlocked: () => abandonedGate.networkBlocked(),
        networkEpoch: () => abandonedGate.networkEpoch(),
      });
      ipcMain.removeAllListeners('lykeion:reserve-auth');
      ipcMain.on('lykeion:reserve-auth', (event, path) => {
        event.returnValue = abandonedGate.reserve(path);
      });
      await abandonedSession.cookies.set({
        url: origin,
        name: 'lyk_session',
        value: 'old',
        httpOnly: true,
      });
      const abandonedWindow = new BrowserWindow({
        show: false,
        webPreferences: {
          session: abandonedSession,
          sandbox: true,
          preload: join(process.cwd(), 'apps/desktop/out/preload.cjs'),
        },
      });
      let releaseCleanup;
      abandonedWindow.webContents.on('destroyed', () => {
        abandonedGate.abandonUnclaimed();
        void abandonedGate.recoverAbandonedLogout(async () => {
          await new Promise((resolve) => {
            releaseCleanup = resolve;
          });
          await clearAbandonedLogout(abandonedSession, origin);
        });
      });
      await abandonedWindow.loadURL(desktopEntryURL(origin));
      const stale = await abandonedWindow.webContents.executeJavaScript(
        "window.lykeionDesktop.reserveAuth('/auth/logout')",
      );
      assert.ok(stale);
      const destroyed = new Promise((resolve) =>
        abandonedWindow.webContents.once('destroyed', resolve),
      );
      abandonedWindow.destroy();
      await destroyed;
      assert.equal(abandonedGate.continueMutation(-1, stale, '/auth/logout'), false);
      const reopened = new BrowserWindow({
        show: false,
        webPreferences: {
          session: abandonedSession,
          sandbox: true,
          preload: join(process.cwd(), 'apps/desktop/out/preload.cjs'),
        },
      });
      let opened = false;
      const opening = reopened.loadURL(desktopEntryURL(origin)).then(() => {
        opened = true;
      });
      await new Promise((resolve) => setTimeout(resolve, 100));
      assert.equal(opened, false);
      assert.equal(meCookie, 'not-called');
      releaseCleanup();
      await opening;
      assert.equal(
        await reopened.webContents.executeJavaScript("fetch('/api/me').then(r => r.status)"),
        200,
      );
      assert.equal(meCookie, '');
      const abandoned = await abandonedGate.shutdown(
        async () => {},
        50,
        () => clearAbandonedLogout(abandonedSession, origin),
      );
      assert.equal(abandoned[0].status, 'fulfilled');
      assert.equal(
        (await abandonedSession.cookies.get({ url: origin, name: 'lyk_session' })).length,
        0,
      );
      reopened.destroy();
      await abandonedSession.clearStorageData();
      const reloadGate = new DesktopAuthGate();
      ipcMain.removeAllListeners('lykeion:reserve-auth');
      ipcMain.on('lykeion:reserve-auth', (event, path) => {
        event.returnValue = reloadGate.reserve(path);
      });
      ipcMain.removeAllListeners('lykeion:document-start');
      ipcMain.on('lykeion:document-start', (event) => {
        reloadGate.abandonUnclaimed();
        event.returnValue = 'new-document';
      });
      await target.cookies.set({ url: origin, name: 'lyk_session', value: 'old', httpOnly: true });
      const reloadWindow = new BrowserWindow({
        show: false,
        webPreferences: {
          session: target,
          sandbox: true,
          preload: join(process.cwd(), 'apps/desktop/out/preload.cjs'),
        },
      });
      await reloadWindow.loadURL(desktopEntryURL(origin));
      const reloadedToken = await reloadWindow.webContents.executeJavaScript(
        "window.lykeionDesktop.reserveAuth('/auth/logout')",
      );
      await reloadWindow.loadURL(desktopEntryURL(origin));
      assert.equal(reloadGate.continueMutation(-1, reloadedToken, '/auth/logout'), false);
      assert.equal(
        (
          await reloadGate.shutdown(
            async () => {},
            50,
            () => clearAbandonedLogout(target, origin),
          )
        )[0].status,
        'fulfilled',
      );
      assert.equal((await target.cookies.get({ url: origin, name: 'lyk_session' })).length, 0);
      reloadWindow.destroy();
      const interleavedSession = session.fromPartition('interleaved-auth');
      const interleavedGate = new DesktopAuthGate();
      await interleavedSession.cookies.set({
        url: origin,
        name: 'lyk_session',
        value: 'old',
        httpOnly: true,
      });
      let interleavedRecovery;
      installDesktopRenderer(interleavedSession, origin, profile, {
        finish: (id) => interleavedGate.finishMutation(id),
        continue: (id, token, path) => {
          const claimed = interleavedGate.continueMutation(id, token, path);
          if (claimed) {
            interleavedGate.abandonUnclaimed();
            interleavedRecovery = interleavedGate.recoverAbandonedLogout(() =>
              clearAbandonedLogout(interleavedSession, origin),
            );
          }
          return claimed;
        },
        networkReady: () => interleavedGate.networkReady(),
        networkBlocked: () => interleavedGate.networkBlocked(),
        networkEpoch: () => interleavedGate.networkEpoch(),
      });
      const interleavedWindow = new BrowserWindow({
        show: false,
        webPreferences: { session: interleavedSession, sandbox: true },
      });
      await interleavedWindow.loadURL(desktopEntryURL(origin));
      const admitted = interleavedGate.reserve('/auth/verify-code');
      interleavedGate.reserve('/auth/logout');
      entered = false;
      await interleavedWindow.webContents.executeJavaScript(
        `void fetch('/auth/verify-code',{method:'POST',headers:{'X-Lykeion-Desktop-Auth-Reservation':${JSON.stringify(admitted)}}}).catch(()=>{}); true`,
      );
      for (let i = 0; i < 100 && !entered; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(entered, true);
      release();
      await interleavedRecovery;
      assert.equal((await interleavedSession.cookies.get({ name: 'lyk_session' })).length, 0);
      interleavedWindow.destroy();
      await interleavedSession.clearStorageData();
      const epochSession = session.fromPartition('epoch-auth');
      const epochGate = new DesktopAuthGate();
      let armed = false,
        readyCalls = 0,
        holdAt = 3,
        captured = false,
        releaseHeaders,
        capturedCookie = false;
      await epochSession.cookies.set({
        url: origin,
        name: 'lyk_session',
        value: 'old',
        httpOnly: true,
      });
      installDesktopRenderer(epochSession, origin, profile, {
        finish: (id) => epochGate.finishMutation(id),
        continue: (id, token, path) => epochGate.continueMutation(id, token, path),
        networkReady: () => {
          if (armed && ++readyCalls === holdAt) {
            captured = true;
            return new Promise((resolve) => {
              releaseHeaders = resolve;
            });
          }
          return epochGate.networkReady();
        },
        networkBlocked: () => epochGate.networkBlocked(),
        networkEpoch: (hasSessionCookie) => {
          if (armed && hasSessionCookie) capturedCookie = true;
          return epochGate.networkEpoch();
        },
      });
      const epochWindow = new BrowserWindow({
        show: false,
        webPreferences: { session: epochSession, sandbox: true },
      });
      await epochWindow.loadURL(desktopEntryURL(origin));
      armed = true;
      await epochWindow.webContents.executeJavaScript(
        "void fetch('/api/stale').catch(()=>{}); true",
      );
      for (let i = 0; i < 100 && !captured; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(captured, true);
      assert.equal(capturedCookie, true);
      epochGate.reserve('/auth/logout');
      epochGate.abandonUnclaimed();
      await epochGate.recoverAbandonedLogout(() => clearAbandonedLogout(epochSession, origin));
      releaseHeaders();
      await new Promise((resolve) => setTimeout(resolve, 100));
      assert.equal(staleReceived, false);
      await epochSession.cookies.set({
        url: origin,
        name: 'lyk_session',
        value: 'old',
        httpOnly: true,
      });
      readyCalls = 0;
      captured = false;
      holdAt = 2;
      await epochWindow.webContents.executeJavaScript(
        "void fetch('/api/stale').catch(()=>{}); true",
      );
      for (let i = 0; i < 100 && !captured; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(captured, true);
      epochGate.reserve('/auth/logout');
      epochGate.abandonUnclaimed();
      await epochGate.recoverAbandonedLogout(() => clearAbandonedLogout(epochSession, origin));
      releaseHeaders();
      await new Promise((resolve) => setTimeout(resolve, 100));
      assert.equal(staleReceived, false);
      epochWindow.destroy();
      await epochSession.clearStorageData();
      const variants = session.fromPartition('auth-cookie-variants');
      await variants.cookies.set({
        url: 'http://lykeion.test/api',
        name: 'lyk_session',
        value: 'valid-looking',
        path: '/api',
        httpOnly: true,
      });
      await assert.rejects(clearAbandonedLogout(variants, 'http://lykeion.test'));
      await variants.clearStorageData();
      await variants.cookies.set({
        url: 'http://lykeion.test/',
        name: 'lyk_session',
        value: 'valid-looking',
        domain: '.lykeion.test',
        path: '/',
        httpOnly: true,
      });
      const domainCookies = await variants.cookies.get({ name: 'lyk_session' });
      assert.equal(domainCookies[0].hostOnly, false);
      await assert.rejects(clearAbandonedLogout(variants, 'http://lykeion.test'));
      await variants.clearStorageData();
      const verifiedSession = session.fromPartition('verified-logout');
      const verifiedGate = new DesktopAuthGate();
      installDesktopRenderer(verifiedSession, origin, profile, {
        finish: (id) => verifiedGate.finishMutation(id),
        continue: (id, token, path) => verifiedGate.continueMutation(id, token, path),
        networkReady: () => verifiedGate.networkReady(),
        networkBlocked: () => verifiedGate.networkBlocked(),
        networkEpoch: () => verifiedGate.networkEpoch(),
        waitForLogoutTurn: (id) => verifiedGate.waitForLogoutTurn(id),
        completeLogout: async () => {
          verifiedGate.beginLogoutRecovery();
          await verifiedGate.recoverAbandonedLogout(() =>
            clearAbandonedLogout(verifiedSession, origin),
          );
        },
        logoutTimeoutMs: 500,
      });
      const verifiedWindow = new BrowserWindow({
        show: false,
        webPreferences: { session: verifiedSession, sandbox: true },
      });
      await verifiedWindow.loadURL(desktopEntryURL(origin));
      const runVerifiedLogout = async (status, cookiePath) => {
        await verifiedSession.clearStorageData();
        await verifiedSession.cookies.set({
          url: `${origin}${cookiePath}`,
          name: 'lyk_session',
          value: 'old',
          path: cookiePath,
        });
        entered = false;
        logoutStatus = status;
        logoutDeletes = false;
        const token = verifiedGate.reserve('/auth/logout');
        await verifiedWindow.webContents.executeJavaScript(
          `window.logoutResult = fetch('/auth/logout', {method:'POST', headers:{'X-Lykeion-Desktop-Auth-Reservation':${JSON.stringify(token)}}}).then(r=>({status:r.status,local:r.headers.get('X-Lykeion-Local-Logout')})).catch(()=>null); true`,
        );
        for (let i = 0; i < 100 && !entered; i++)
          await new Promise((resolve) => setTimeout(resolve, 10));
        assert.equal(entered, true);
        release();
        return verifiedWindow.webContents.executeJavaScript('window.logoutResult');
      };
      assert.deepEqual(await runVerifiedLogout(204, '/'), { status: 204, local: null });
      assert.equal((await verifiedSession.cookies.get({ name: 'lyk_session' })).length, 0);
      assert.deepEqual(await runVerifiedLogout(500, '/'), { status: 204, local: '1' });
      assert.equal((await verifiedSession.cookies.get({ name: 'lyk_session' })).length, 0);
      holdVerification = true;
      verificationEntered = false;
      entered = false;
      const verification = verifiedGate.reserve('/auth/verify-code');
      await verifiedWindow.webContents.executeJavaScript(
        `window.verifyResult = fetch('/auth/verify-code', {method:'POST', headers:{'X-Lykeion-Desktop-Auth-Reservation':${JSON.stringify(verification)}}}).then(r=>r.status); true`,
      );
      for (let i = 0; i < 100 && !verificationEntered; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(verificationEntered, true);
      const concurrentLogout = verifiedGate.reserve('/auth/logout');
      await verifiedWindow.webContents.executeJavaScript(
        `window.concurrentLogout = fetch('/auth/logout', {method:'POST', headers:{'X-Lykeion-Desktop-Auth-Reservation':${JSON.stringify(concurrentLogout)}}}).then(r=>r.status); true`,
      );
      await new Promise((resolve) => setTimeout(resolve, 50));
      assert.equal(entered, false);
      releaseVerification();
      assert.equal(await verifiedWindow.webContents.executeJavaScript('window.verifyResult'), 204);
      for (let i = 0; i < 100 && !entered; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(entered, true);
      release();
      assert.equal(
        await verifiedWindow.webContents.executeJavaScript('window.concurrentLogout'),
        204,
      );
      assert.equal((await verifiedSession.cookies.get({ name: 'lyk_session' })).length, 0);
      const nextSignIn = verifiedGate.reserve('/auth/verify-code');
      assert.ok(nextSignIn);
      verifiedGate.release(nextSignIn);
      holdVerification = false;
      await verifiedSession.cookies.set({ url: origin, name: 'lyk_session', value: 'old' });
      entered = false;
      stalledClosed = false;
      stallLogout = true;
      const stalledToken = verifiedGate.reserve('/auth/logout');
      const stalledResult = verifiedWindow.webContents.executeJavaScript(
        `fetch('/auth/logout',{method:'POST',headers:{'X-Lykeion-Desktop-Auth-Reservation':${JSON.stringify(stalledToken)}}}).then(r=>({status:r.status,local:r.headers.get('X-Lykeion-Local-Logout')}))`,
      );
      assert.deepEqual(await stalledResult, { status: 204, local: '1' });
      for (let i = 0; i < 100 && !stalledClosed; i++)
        await new Promise((resolve) => setTimeout(resolve, 10));
      assert.equal(stalledClosed, true);
      assert.equal((await verifiedSession.cookies.get({ name: 'lyk_session' })).length, 0);
      stallLogout = false;
      assert.equal(await runVerifiedLogout(204, '/api'), null);
      assert.equal(verifiedGate.networkBlocked(), true);
      assert.equal((await verifiedSession.cookies.get({ name: 'lyk_session' })).length, 1);
      verifiedWindow.destroy();
      await verifiedSession.clearStorageData();
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
