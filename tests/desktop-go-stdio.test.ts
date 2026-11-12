import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { describe, expect, it } from 'vitest';

describe.skipIf(process.platform === 'win32')('Node to Go inherited pipes', () => {
  it('uses pollable owned descriptors for framing, deadlines and cancellation', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lyk-go-stdio-'));
    try {
      const binary = path.join(dir, 'probe');
      execFileSync('go', ['test', '-c', '-o', binary, './internal/desktopupdate'], {
        cwd: path.resolve('server'),
        env: { ...process.env, GOCACHE: path.join(tmpdir(), 'lykeion-desktop-go-cache') }, timeout: 30000,
      });
      for (const mode of ['normal', 'read-timeout', 'write-timeout', 'cancel']) {
        const child = spawn(binary, ['-test.run=^TestNodePipeChild$'], {
          env: { ...process.env, LYK_TEST_NODE_PIPE_CHILD: mode }, stdio: ['pipe', 'pipe', 'pipe'],
        });
        const chunks: Buffer[] = [];
        let sent = false;
        if (mode !== 'write-timeout') child.stdout.on('data', (chunk) => {
          chunks.push(Buffer.from(chunk));
          if (!sent && mode === 'normal' && Buffer.concat(chunks).includes(Buffer.from('READY\n'))) {
            sent = true; child.stdin.end('STREAM\n');
          }
        });
        child.stderr.resume();
        const closed = new Promise<void>((resolve) => child.once('close', () => resolve()));
        const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
        try {
          const code = await new Promise<number | null>((resolve, reject) => {
            child.once('error', reject);
            child.once('exit', (code) => { if (mode === 'write-timeout') child.stdout.resume(); resolve(code); });
          });
          await closed;
          expect(code, mode).toBe(0);
          if (mode === 'normal') expect(Buffer.concat(chunks).toString()).toBe('READY\nDONE\n');
        } finally { clearTimeout(timer); if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); }
      }
    } finally { rmSync(dir, { recursive: true, force: true }); }
  }, 45000);
});
