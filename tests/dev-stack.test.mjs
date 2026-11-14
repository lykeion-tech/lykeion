import { test } from 'node:test';
import { strict as assert } from 'node:assert';
import { mkdtempSync, writeFileSync, chmodSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';

test('starts all three processes and stops when one fails', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'lyk-dev-test-'));
  const log = join(dir, 'started');
  try {
    for (const name of ['go', 'npm']) {
      const file = join(dir, name);
      writeFileSync(
        file,
        '#!/bin/sh\necho "' +
          name +
          ' $*" >> "$TEST_LOG"\nif [ "$2" = web ]; then sleep 1; exit 7; fi\nexec sleep 30\n',
      );
      chmodSync(file, 0o755);
    }
    const child = spawn(process.execPath, ['scripts/dev-stack.mjs'], {
      env: { ...process.env, PATH: `${dir}:/usr/bin:/bin`, TEST_LOG: log },
      stdio: 'ignore',
    });
    const code = await new Promise((resolve, reject) => {
      child.on('error', reject);
      child.on('exit', resolve);
    });
    assert.equal(code, 7);
    const started = readFileSync(log, 'utf8');
    assert.match(started, /go run -p 1 \.\/cmd\/server/);
    assert.match(started, /npm run web:app/);
    assert.match(started, /npm run web\n/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
