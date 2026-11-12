import { execFileSync, spawn } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, realpathSync, rmSync } from 'node:fs';
import { once } from 'node:events';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { describe, expect, it, vi } from 'vitest';

describe.skipIf(process.platform !== 'darwin')('update helper parent liveness', () => {
  it('exits and releases its lock when its Node parent dies in each phase', async () => {
    const root = mkdtempSync(path.join(tmpdir(), 'lyk-liveness-test-'));
    try {
      const binary = path.join(root, 'probe');
      execFileSync('go', ['test', '-c', '-o', binary, './internal/desktopupdate'], {
        cwd: path.resolve('server'), env: { ...process.env, GOCACHE: path.join(tmpdir(), 'lykeion-desktop-go-cache') }, timeout: 30000,
      });
      for (const mode of ['lock', 'metadata', 'download', 'stream']) {
        const home = realpathSync(mkdtempSync(path.join(root, `${mode}-home-`)));
        const ready = path.join(root, `${mode}.ready`), finished = path.join(root, `${mode}.finished`), pidfile = path.join(root, `${mode}.pid`);
        const parent = spawn(process.execPath, [path.resolve('tests/fixtures/desktop-update-liveness-parent.cjs'), binary, mode, home, ready, finished, pidfile], { stdio: ['ignore', 'ignore', 'ignore'], detached: true });
        const parentExit = once(parent, 'exit');
        let helperPid: number | undefined;
        try {
          await vi.waitFor(() => expect(existsSync(ready), `${mode} did not reach blocked phase`).toBe(true), { timeout: 10000 });
          helperPid = Number(readFileSync(pidfile, 'utf8'));
          expect(Number.isSafeInteger(helperPid) && helperPid > 1).toBe(true);
          parent.kill('SIGKILL');
          await parentExit;
          await vi.waitFor(() => expect(existsSync(finished), `${mode} helper did not cancel`).toBe(true), { timeout: 5000 });
          await vi.waitFor(() => {
            let alive = true;
            try { process.kill(helperPid!, 0); } catch (error: any) { if (error.code === 'ESRCH') alive = false; else throw error; }
            expect(alive, `${mode} helper did not exit`).toBe(false);
          }, { timeout: 5000 });
          execFileSync(binary, ['-test.run=^TestLivenessProbeRecovery$'], {
            env: { ...process.env, LYK_TEST_LIVENESS_RECOVER: '1', LYK_TEST_LIVENESS_HOME: home }, timeout: 5000,
          });
        } finally {
          if (parent.exitCode === null && parent.signalCode === null) { parent.kill('SIGKILL'); await parentExit; }
          if (parent.pid) { try { process.kill(-parent.pid, 'SIGKILL'); } catch (error: any) { if (error.code !== 'ESRCH') throw error; } }
        }
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  }, 80000);
});
