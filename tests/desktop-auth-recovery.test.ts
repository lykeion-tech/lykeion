// @vitest-environment node
import { mkdtemp, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, it, vi } from 'vitest';
import { DesktopAuthRecovery } from '../apps/desktop/src/desktop-auth-recovery';

it('persists per-origin intent across restart and clears untrusted cookies before releasing quarantine', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'lyk-auth-recovery-test-'));
  try {
    const a = new DesktopAuthRecovery(dir, 'https://one.example');
    const b = new DesktopAuthRecovery(dir, 'https://two.example');
    await a.mark();
    expect(await readFile(a.path, 'utf8')).not.toContain('cookie');
    const fresh = new DesktopAuthRecovery(dir, 'https://one.example');
    const clear = vi.fn(async () => {}),
      verify = vi.fn(async () => false);
    await b.recover(clear, verify);
    expect(clear).not.toHaveBeenCalled();
    await expect(fresh.recover(clear, verify)).rejects.toThrow();
    expect(clear).toHaveBeenCalledOnce();
    verify.mockResolvedValue(true);
    await fresh.recover(clear, verify);
    await fresh.recover(clear, verify);
    expect(clear).toHaveBeenCalledTimes(2);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
