import { createServer } from 'node:http';
import { runFixture } from '../scripts/test-desktop-electron.mjs';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import electron from 'electron';
import assert from 'node:assert/strict';
const temporary = mkdtempSync(join(tmpdir(), 'lyk-google-restart-'));
let held;
let safeToRemove = true;
const server = createServer((req, res) => {
  if (req.url === '/control') {
    res.writeHead(held ? 200 : 404);
    return res.end();
  }
  if (req.url === '/auth/desktop/google/start') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    return res.end(JSON.stringify({ ticket: 'T'.repeat(43) }));
  }
  if (req.url?.startsWith('/auth/google?desktop_ticket=')) {
    res.writeHead(200);
    return res.end('Return to app');
  }
  if (req.url === '/auth/desktop/google/exchange') {
    held = res;
    return;
  }
  if (req.url === '/api/me') {
    res.writeHead(401, { 'Content-Type': 'application/json' });
    return res.end('{"error":"unauthorized"}');
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end('[]');
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
mkdirSync(join(temporary, '.lyk'), { recursive: true });
writeFileSync(
  join(temporary, '.lyk', 'desktop.json'),
  JSON.stringify({ schemaVersion: 1, apiUrl: origin }),
);
const marker = join(
  temporary,
  'appdata',
  'Lykeion Preview',
  'verification-intents',
  createHash('sha256').update(origin).digest('hex'),
);
async function run(phase) {
  const env = {
    ...process.env,
    HOME: temporary,
    LYK_GOOGLE_FIXTURE_HOME: temporary,
    LYK_GOOGLE_FIXTURE_ORIGIN: origin,
    LYK_GOOGLE_FIXTURE_PHASE: phase,
  };
  delete env.ELECTRON_RUN_AS_NODE;
  const result = await runFixture(
    electron,
    ['tests/desktop-google-restart-child.mjs'],
    env,
    30_000,
  );
  if (result.treeUncertain) safeToRemove = false;
  assert.equal(result.timedOut, false, `child ${phase} timed out`);
  assert.equal(result.treeUncertain, false, `child ${phase} process tree was not cleaned up`);
  assert.equal(result.code, 0, `child ${phase} failed: ${result.signal || result.code}`);
}
try {
  await run('first');
  assert.equal(existsSync(marker), true, 'interrupted verification must remain durable');
  held.writeHead(200, { 'Content-Type': 'application/json' });
  held.end(JSON.stringify({ token: 'late.jwt.token', expires_in: 3600 }));
  await run('second');
  assert.equal(existsSync(marker), false, 'recovery should clear the verification marker');
  console.log('production main quit/restart recovery passed');
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  if (safeToRemove) rmSync(temporary, { recursive: true, force: true });
}
