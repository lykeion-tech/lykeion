import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const fixtures = [
  'desktop-electron-fixture.mjs',
  'desktop-links-fixture.mjs',
  'desktop-auth-shutdown-electron-fixture.mjs',
  'desktop-tls-fixture.mjs',
  'desktop-lifetime-fixture.mjs',
];

for (const fixture of fixtures) {
  const temporary = mkdtempSync(path.join(tmpdir(), 'lyk-electron-test-'));
  try {
    const env = { ...process.env, TMPDIR: temporary, TMP: temporary, TEMP: temporary };
    delete env.ELECTRON_RUN_AS_NODE;
    const result = spawnSync(
      process.execPath,
      ['node_modules/electron/cli.js', `tests/${fixture}`],
      {
        stdio: 'inherit',
        env,
      },
    );
    if (result.error) throw result.error;
    if (result.status !== 0) process.exitCode = result.status ?? 1;
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
  if (process.exitCode) break;
}
