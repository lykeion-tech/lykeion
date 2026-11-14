// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, mkdir, readFile, rm, writeFile, chmod } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { DaemonManager } from '../apps/desktop/src/daemon-manager.js';
import { DaemonRuntime } from '../apps/desktop/src/daemon-runtime.js';

const roots: string[] = [];
afterEach(async () => {
  await Promise.all(roots.splice(0).map((p) => rm(p, { recursive: true, force: true })));
});
const origin = 'http://127.0.0.1:8080';
const session = { apiOrigin: origin, userID: 'alice', sessionToken: 'jwt-alice' };
const oldRevision = 'b'.repeat(40);
const newRevision = 'a'.repeat(40);

async function bundle(dir: string, content: string) {
  await mkdir(dir, { recursive: true });
  const file = path.join(dir, 'lyk');
  await writeFile(file, content, { mode: 0o755 });
  return { file, sha256: createHash('sha256').update(content).digest('hex') };
}

let legacy = '';
async function machine(fromLegacy = false) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'desktop-runtime-'));
  roots.push(root);
  const runtimes = path.join(root, 'runtime');
  const old = await bundle(path.join(root, 'old-app'), 'old lyk');
  const next = await bundle(path.join(root, 'new-app'), 'new lyk');
  const revisionOf = new Map([
    [old.sha256, oldRevision],
    [next.sha256, newRevision],
  ]);
  const daemon = { running: false, revision: '', activeWork: 0 as number | undefined, refuseIdle: false };
  const failing = new Set<string>();
  const calls: { args: string; file: string; stop?: string }[] = [];
  const execute = vi.fn(
    async (file: string, args: string[], options: { env: NodeJS.ProcessEnv; timeout: number }) => {
      calls.push({ args: args.join(' '), file, stop: options.env.LYK_DESKTOP_SAFE_STOP });
      const config = path.dirname(options.env.LYK_CONFIG!);
      if (args[1] === 'start') {
        const sha = path.basename(path.dirname(file));
        if (file === legacy) {
          Object.assign(daemon, { running: true, revision: '' });
          await mkdir(path.join(config, 'daemon'), { recursive: true });
          await writeFile(
            path.join(config, 'daemon/daemon.json'),
            JSON.stringify({ daemon_id: 'daemon-alice', server: origin }),
            { mode: 0o600 },
          );
          return '';
        }
        if (failing.has(sha)) throw new Error('start failed');
        Object.assign(daemon, { running: true, revision: revisionOf.get(sha) ?? '' });
        await mkdir(path.join(config, 'daemon'), { recursive: true });
        await writeFile(
          path.join(config, 'daemon/daemon.json'),
          JSON.stringify({ daemon_id: 'daemon-alice', server: origin }),
          { mode: 0o600 },
        );
        return '';
      }
      if (args[1] === 'stop') {
        if (options.env.LYK_DESKTOP_SAFE_STOP === 'idle' && (daemon.refuseIdle || daemon.activeWork !== 0))
          throw new Error('the daemon has active work');
        daemon.running = false;
        return '';
      }
      return JSON.stringify(
        daemon.running
          ? {
              status: 'running',
              pid: 123,
              daemon_id: 'daemon-alice',
              profile: options.env.LYK_DAEMON_PROFILE,
              server_url: origin,
              workspace_count: 1,
              revision: daemon.revision,
              ...(daemon.activeWork === undefined ? {} : { active_work: daemon.activeWork }),
            }
          : { status: 'stopped', profile: options.env.LYK_DAEMON_PROFILE },
      );
    },
  );
  const app = (b: { file: string; sha256: string }, revision: string, copies = true) =>
    new DaemonManager({
      configRoot: path.join(root, 'config'),
      apiOrigin: origin,
      cliPath: b.file,
      supported: true,
      packaged: true,
      execute,
      request: async (_url: string, options: { method: string }) =>
        options.method === 'POST' ? { token: 'lyk_alice' } : { id: 'alice' },
      cliAvailable: async () => true,
      authorizeBrowser: () => async () => () => true,
      ...(copies ? { runtime: new DaemonRuntime(runtimes, b.file, b.sha256), revision } : {}),
    });
  legacy = path.join(root, 'legacy-app', 'lyk');
  if (fromLegacy) {
    // An app from before daemon copies starts lyk from inside its bundle and
    // its daemon reports neither revision nor work.
    await app({ file: legacy, sha256: old.sha256 }, oldRevision, false).configureSession(session);
    daemon.activeWork = undefined;
  } else {
    // The old app starts the daemon from its own copy, then is replaced.
    await app(old, oldRevision).configureSession(session);
  }
  const updated = app(next, newRevision);
  await updated.configureSession(session);
  calls.length = 0;
  return { root, runtimes, old, next, daemon, failing, calls, updated };
}

describe('desktop daemon runtime', () => {
  it('runs the daemon from a verified copy and replaces a tampered one', async () => {
    const m = await machine();
    const copy = path.join(m.runtimes, m.old.sha256, 'lyk');
    expect(await readFile(copy, 'utf8')).toBe('old lyk');
    const runtime = new DaemonRuntime(m.runtimes, m.old.file, m.old.sha256);
    await chmod(copy, 0o755);
    await writeFile(copy, 'planted');
    expect(await runtime.verified(m.old.sha256)).toBeNull();
    expect(await runtime.install()).toBe(copy);
    expect(await readFile(copy, 'utf8')).toBe('old lyk');
    await writeFile(m.old.file, 'swapped bundle');
    await rm(copy);
    await expect(runtime.install()).rejects.toThrow('Reinstall');
  });

  it('keeps an updated app off a daemon with work and swaps it once idle', async () => {
    const m = await machine();
    expect(m.daemon.revision).toBe(oldRevision);
    for (const work of [2, undefined]) {
      m.daemon.activeWork = work;
      await m.updated.refresh();
      expect(m.calls.some((c) => c.args.startsWith('daemon stop'))).toBe(false);
      expect(m.daemon.revision).toBe(oldRevision);
    }
    m.daemon.activeWork = 0;
    m.daemon.refuseIdle = true;
    await m.updated.refresh();
    expect(m.calls.filter((c) => c.args === 'daemon stop').map((c) => c.stop)).toEqual(['idle']);
    expect(m.daemon.running && m.daemon.revision).toBe(oldRevision);
    m.daemon.refuseIdle = false;
    m.calls.length = 0;
    await m.updated.refresh();
    expect(m.calls.filter((c) => c.args === 'daemon stop').map((c) => c.stop)).toEqual(['idle']);
    const start = m.calls.find((c) => c.args === 'daemon start');
    expect(start?.file).toBe(path.join(m.runtimes, m.next.sha256, 'lyk'));
    expect(m.daemon.running && m.daemon.revision).toBe(newRevision);
    m.calls.length = 0;
    await m.updated.refresh();
    expect(m.calls.some((c) => c.args !== 'daemon status --json')).toBe(false);
  });

  it('runs the previous daemon again when the updated one fails to start', async () => {
    const m = await machine();
    m.daemon.activeWork = 0;
    m.failing.add(m.next.sha256);
    await m.updated.refresh();
    const starts = m.calls.filter((c) => c.args === 'daemon start').map((c) => c.file);
    expect(starts).toEqual([
      path.join(m.runtimes, m.next.sha256, 'lyk'),
      path.join(m.runtimes, m.old.sha256, 'lyk'),
    ]);
    expect(m.daemon.running && m.daemon.revision).toBe(oldRevision);
    expect((await m.updated.getSnapshot()).error).toContain('previous version is running');
    m.calls.length = 0;
    await m.updated.refresh();
    expect(m.calls.some((c) => c.args.startsWith('daemon stop'))).toBe(false);
  });

  it('asks the person to restart a daemon from before runtime copies', async () => {
    const m = await machine(true);
    expect(m.daemon.running && m.daemon.revision).toBe('');
    await m.updated.refresh();
    expect(m.calls.some((c) => c.args.startsWith('daemon stop'))).toBe(false);
    expect((await m.updated.getSnapshot()).error).toContain('Restart it here');
    await m.updated.restart();
    expect(m.calls.find((c) => c.args === 'daemon start')?.file).toBe(
      path.join(m.runtimes, m.next.sha256, 'lyk'),
    );
    expect(m.daemon.running && m.daemon.revision).toBe(newRevision);
    expect((await m.updated.getSnapshot()).error).toBeNull();
  });
});
