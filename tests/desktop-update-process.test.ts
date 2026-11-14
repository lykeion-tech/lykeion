import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { runDesktopUpdateHelper } from '../apps/desktop/src/desktop-update-process';

describe.skipIf(process.platform === 'win32')('desktop update helper process', () => {
  it('accepts a bounded fixed-command response', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lyk-update-process-'));
    try {
      const file = path.join(dir, 'helper');
      writeFileSync(file, '#!/bin/sh\n[ "$1" = desktop-update-check ] || exit 2\nprintf \'{"state":"current","version":"1.2.3"}\\n\'\n');
      chmodSync(file, 0o700);
      await expect(runDesktopUpdateHelper(file, new AbortController().signal)).resolves.toEqual({ state: 'current', version: '1.2.3' });
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('terminates and waits for a helper that ignores TERM', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lyk-update-process-'));
    try {
      const file = path.join(dir, 'helper');
      writeFileSync(file, '#!/bin/sh\ntrap "" TERM\nwhile :; do :; done\n');
      chmodSync(file, 0o700);
      const controller = new AbortController();
      const pending = runDesktopUpdateHelper(file, controller.signal);
      await new Promise((resolve) => setTimeout(resolve, 100));
      controller.abort();
      await expect(pending).rejects.toThrow('Update check unavailable');
    } finally { rmSync(dir, { recursive: true, force: true }); }
  }, 5000);
});
