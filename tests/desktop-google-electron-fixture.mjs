import { app, session } from 'electron';
import { createServer } from 'node:http';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import assert from 'node:assert/strict';
const profile = mkdtempSync(join(tmpdir(), 'lyk-google-test-'));
app.setPath('userData', profile);
app
  .whenReady()
  .then(async () => {
    const { DesktopAuthGate } = await import('../apps/desktop/out/desktop-auth-gate.js');
    const { DesktopVerification } = await import('../apps/desktop/out/desktop-verification.js');
    const { DesktopGoogleAuth } = await import('../apps/desktop/out/desktop-google-auth.js');
    let resultToken = 'same';
    const server = createServer((req, res) => {
      if (req.url === '/api/me') {
        const id = req.headers.cookie?.includes('lyk_session=other') ? 'account-b' : 'account-a';
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ id }));
      }
      if (req.url === '/auth/desktop/google/start') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ ticket: 'T'.repeat(43) }));
      }
      if (req.url === '/auth/desktop/google/exchange') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(
          JSON.stringify({ token: resultToken, expires_in: 3600, email: 'user@example.org' }),
        );
      }
      res.writeHead(404);
      res.end();
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const jar = session.fromPartition('google-fixture');
    let documentID = 'old';
    const gate = new DesktopAuthGate();
    const proof = new DesktopVerification(
      origin,
      jar,
      () => documentID,
      () => gate.canFinishVerification(),
    );
    jar.cookies.on('changed', (_event, cookie) => {
      if (cookie.name === 'lyk_session') proof.cookieChanged();
    });
    const owner = { current: (id) => id === 'account-a', verify: async (id) => id === 'account-a' };
    async function attempt(tokenValue) {
      resultToken = tokenValue;
      documentID = 'old';
      await jar.cookies.set({
        url: origin,
        name: 'lyk_session',
        value: 'old',
        path: '/',
        httpOnly: true,
      });
      const reservation = gate.reserve('/auth/verify-code');
      assert.ok(reservation);
      proof.reserve(reservation, documentID, owner);
      assert.equal(gate.continueMutation(-1, reservation, '/auth/verify-code'), true);
      const flow = new DesktopGoogleAuth(origin, {
        request: fetch,
        open: async () => {},
        confirm: async () => true,
        set: (cookie) => jar.cookies.set(cookie),
        flush: () => jar.cookies.flushStore(),
        current: () => documentID === 'old',
        capture: async () => {
          const cookies = await jar.cookies.get({ url: origin + '/api/me', name: 'lyk_session' });
          await proof.capture(reservation, { Cookie: 'lyk_session=' + cookies[0].value });
        },
      });
      try {
        const pending = flow.run();
        for (let i = 0; i < 100; i++) {
          if (await flow.submitCode('A'.repeat(43))) break;
          if (i === 99) throw new Error('Timed out waiting to submit code');
          await new Promise((resolve) => setTimeout(resolve, 10));
        }
        await pending;
      } finally {
        gate.finishMutation(-1);
      }
      documentID = 'new';
      const read = await proof.read(documentID);
      return { read, retained: await proof.prepare(documentID, read.user.id) };
    }
    try {
      const same = await attempt('same');
      assert.equal(same.read.user.id, 'account-a');
      assert.equal(same.retained, true);
      assert.equal(proof.canFinish(documentID, 'account-a'), true);
      const changed = await attempt('other');
      assert.equal(changed.read.user.id, 'account-b');
      assert.equal(changed.retained, false);
      assert.equal(proof.canFinish(documentID, 'account-b'), false);
      console.log('desktop Google Electron cookie and owner proof passed');
    } finally {
      await jar.clearStorageData();
      await new Promise((resolve) => server.close(resolve));
      rmSync(profile, { recursive: true, force: true });
    }
  })
  .then(() => app.exit(0))
  .catch((err) => {
    console.error(err);
    app.exit(1);
  });
