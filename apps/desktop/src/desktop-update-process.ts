import { spawn } from 'node:child_process';
import type { DesktopUpdateResult } from './desktop-update.js';

// The binary path is selected by main, never by the renderer.
export function runDesktopUpdateHelper(binary: string, signal: AbortSignal): Promise<DesktopUpdateResult> {
  return new Promise((resolve, reject) => {
    const fail = () => new Error('Update check unavailable');
    if (signal.aborted) { reject(fail()); return; }
    const child = spawn(binary, ['desktop-update-check'], {
      env: {},
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    const output: Buffer[] = [];
    let length = 0;
    let stopping = false;
    let failed = false;
    let force: NodeJS.Timeout | undefined;
    const stop = () => {
      if (stopping) return;
      stopping = true;
      failed = true;
      child.kill('SIGTERM');
      force = setTimeout(() => child.kill('SIGKILL'), 2000);
    };
    const timeout = setTimeout(stop, 30 * 60 * 1000);
    signal.addEventListener('abort', stop, { once: true });
    if (signal.aborted) stop();
    child.stdout.on('data', (chunk: Buffer) => {
      length += chunk.length;
      if (length > 4096) { stop(); return; }
      output.push(chunk);
    });
    child.on('error', () => { failed = true; });
    child.on('close', (code) => {
      clearTimeout(timeout);
      if (force) clearTimeout(force);
      signal.removeEventListener('abort', stop);
      if (failed || code !== 0 || signal.aborted) { reject(fail()); return; }
      try { resolve(JSON.parse(Buffer.concat(output).toString('utf8')) as DesktopUpdateResult); }
      catch { reject(fail()); }
    });
  });
}
