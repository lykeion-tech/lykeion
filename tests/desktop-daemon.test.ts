// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mkdtemp, mkdir, readFile, rm, writeFile, stat, symlink } from 'node:fs/promises';
import os from 'node:os';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import path from 'node:path';
import {
  DaemonManager,
  requestDaemonAPI,
  desktopDaemonOptions,
} from '../apps/desktop/src/daemon-manager.js';
import { createDaemonSession } from '../apps/dashboard/src/lib/daemon-session';
import {
  installDaemonIPC,
  validateDaemonRequest,
  validDaemonSender,
} from '../apps/desktop/src/daemon-ipc.js';

const roots: string[] = [];
afterEach(async () => {
  await Promise.all(roots.splice(0).map((p) => rm(p, { recursive: true, force: true })));
});
const origin = 'http://127.0.0.1:8080';
const session = { apiOrigin: origin, userID: 'alice', sessionToken: 'jwt-alice' };
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
async function fixture() {
  const root = await mkdtemp(path.join(os.tmpdir(), 'desktop-daemon-'));
  roots.push(root);
  let running = false;
  let daemonID = 'daemon-alice';
  const calls: string[] = [];
  const request = vi.fn(async (_url: string, options: { token: string; method: string }) =>
    options.method === 'POST'
      ? { token: options.token.includes('bob') ? 'lyk_bob' : 'lyk_alice' }
      : { id: options.token.includes('bob') ? 'bob' : 'alice' },
  );
  const execute = vi.fn(
    async (_file: string, args: string[], options: { env: NodeJS.ProcessEnv; timeout: number }) => {
      calls.push(args.join(' '));
      if (args[1] === 'start') {
        running = true;
        daemonID =
          JSON.parse(await readFile(options.env.LYK_CONFIG!, 'utf8')).token === 'lyk_bob'
            ? 'daemon-bob'
            : 'daemon-alice';
        await mkdir(path.join(path.dirname(options.env.LYK_CONFIG!), 'daemon'), {
          recursive: true,
        });
        await writeFile(
          path.join(path.dirname(options.env.LYK_CONFIG!), 'daemon/daemon.json'),
          JSON.stringify({ daemon_id: daemonID, server: origin }),
          { mode: 0o600 },
        );
        return '';
      }
      if (args[1] === 'stop') {
        running = false;
        return '';
      }
      return JSON.stringify(
        running
          ? {
              status: 'running',
              pid: 123,
              uptime: 17,
              daemon_id: daemonID,
              profile: options.env.LYK_DAEMON_PROFILE,
              server_url: origin,
              device_name: 'Ada',
              workspace_count: 2,
              os: 'darwin',
            }
          : { status: 'stopped', profile: options.env.LYK_DAEMON_PROFILE },
      );
    },
  );
  const options = {
    configRoot: root,
    apiOrigin: origin,
    cliPath: '/app/out/lyk',
    supported: true,
    execute,
    request,
    cliAvailable: async () => true,
  };
  const manager = new DaemonManager(options);
  return { root, manager, options, execute, request, calls };
}
describe('desktop daemon manager', () => {
  it('retains a verified running owner only until the next native identity mutation', async () => {
    const f = await fixture();
    const running = await f.manager.configureSession(session);
    const owner = f.manager.verificationOwner();
    expect(await owner.verify('alice')).toBe(true);
    expect(await owner.verify('bob')).toBe(false);
    expect(f.calls).not.toContain('daemon stop');
    expect(f.calls.filter((action) => action === 'daemon start')).toHaveLength(1);
    expect(await f.manager.getSnapshot()).toMatchObject({
      pid: running.pid,
      daemonID: running.daemonID,
      status: 'running',
    });
    const clearing = f.manager.clearSession();
    expect(owner.current('alice')).toBe(false);
    await clearing;
  });
  it('rejects an owner whose cached token now belongs to another account', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    f.request.mockResolvedValueOnce({ id: 'bob' });
    expect(await f.manager.verificationOwner().verify('alice')).toBe(false);
  });
  it('stops the verified running profile before clearing even if its config has disappeared', async () => {
    const f = await fixture();
    const snapshot = await f.manager.configureSession(session);
    expect(snapshot.status).toBe('running');
    await rm(path.join(f.root, 'profiles', snapshot.profile!, 'config.json'));
    const cleared = await f.manager.clearSession();
    expect(f.calls).toContain('daemon stop');
    expect(cleared.status).toBe('stopped');
    expect(cleared.authenticatedUserID).toBeNull();
  });
  it('makes standard agent installation locations available to GUI-launched daemon starts', async () => {
    const f = await fixture();
    const oldPath = process.env.PATH;
    process.env.PATH = '/usr/bin:/bin';
    try {
      await f.manager.configureSession(session);
      const start = f.execute.mock.calls.find((c) => c[1][1] === 'start')!;
      const paths = start[2].env.PATH!.split(path.delimiter);
      expect(paths.slice(0, 2)).toEqual(['/usr/bin', '/bin']);
      const localBin = path.join(os.homedir(), '.local', 'bin');
      expect(paths).toContain(localBin);
      expect(paths).toContain('/usr/local/bin');
      if (process.platform === 'darwin') expect(paths).toContain('/opt/homebrew/bin');
      await f.manager.restart();
      const starts = f.execute.mock.calls.filter((c) => c[1][1] === 'start');
      expect(starts).toHaveLength(2);
      expect(starts[1]![2].env.PATH!.split(path.delimiter)).toContain(localBin);
    } finally {
      process.env.PATH = oldPath;
    }
  });
  it('keeps packaged daemons disabled until their executable is verified', () => {
    expect(
      desktopDaemonOptions('darwin', true, true, '/preview/out', '/app/resources', 'http://evil'),
    ).toEqual({ supported: false, cliPath: '/app/resources/bin/lyk' });
    expect(desktopDaemonOptions('linux', true, true, '/preview/out', '/app/resources')).toEqual({
      supported: false,
      cliPath: '/app/resources/bin/lyk',
    });
    expect(desktopDaemonOptions('win32', true, true, '/preview/out', '/app/resources')).toEqual({
      supported: false,
      cliPath: '/app/resources/bin/lyk',
    });
    expect(
      desktopDaemonOptions(
        'darwin',
        false,
        false,
        '/preview/out',
        '/app/resources',
        'http://127.0.0.1:1430',
      ),
    ).toEqual({ supported: false, cliPath: '/preview/out/lyk' });
    expect(
      desktopDaemonOptions(
        'linux',
        true,
        false,
        '/preview/out',
        '/app/resources',
        'http://127.0.0.1:1430',
      ),
    ).toEqual({ supported: true, cliPath: '/preview/out/lyk' });
  });
  it('refuses a symlink or directory as the bundled executable', async () => {
    const f = await fixture();
    const target = path.join(f.root, 'target');
    const link = path.join(f.root, 'lyk');
    await writeFile(target, '#!/bin/sh\n', { mode: 0o700 });
    await symlink(target, link);
    const options = { ...f.options, cliPath: link, cliAvailable: undefined };
    expect((await new DaemonManager(options).getSnapshot()).cliAvailable).toBe(false);
    await rm(link);
    await mkdir(link);
    expect((await new DaemonManager(options).getSnapshot()).cliAvailable).toBe(false);
    await rm(link, { recursive: true });
    await writeFile(link, '#!/bin/sh\n', { mode: 0o700 });
    expect((await new DaemonManager(options).getSnapshot()).cliAvailable).toBe(true);
  });
  it('reports an actionable packaged-runtime error without development instructions', async () => {
    const f = await fixture();
    const manager = new DaemonManager({
      ...f.options,
      packaged: true,
      cliAvailable: async () => false,
    });
    expect((await manager.getSnapshot()).error).toBe(
      'The bundled Lykeion runtime is missing or invalid. Reinstall the app.',
    );
    await expect(manager.configureSession(session)).rejects.toThrow('Reinstall the app');
  });
  it('keeps profiles and tokens distinct across ports of the same server hostname', async () => {
    const f = await fixture();
    await f.manager.setPreferences({ autoStart: false, autoStop: false });
    const first = await f.manager.configureSession(session);
    const secondOrigin = 'http://127.0.0.1:9090';
    const other = new DaemonManager({ ...f.options, apiOrigin: secondOrigin });
    const second = await other.configureSession({ ...session, apiOrigin: secondOrigin });
    expect(first.profile).not.toBe(second.profile);
    expect(
      await readFile(path.join(f.root, 'profiles', first.profile!, 'config.json'), 'utf8'),
    ).toContain(origin);
    expect(
      await readFile(path.join(f.root, 'profiles', second.profile!, 'config.json'), 'utf8'),
    ).toContain(secondOrigin);
    expect(
      f.request.mock.calls.every(([url]) => url.startsWith(origin) || url.startsWith(secondOrigin)),
    ).toBe(true);
  });
  it('requires the CLI desktop-safe shutdown path for desktop stop', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.stop();
    const stop = f.execute.mock.calls.find((call) => call[1][1] === 'stop');
    expect(stop?.[2].env.LYK_DESKTOP_SAFE_STOP).toBe('1');
    expect(
      f.execute.mock.calls
        .filter((call) => call[1][1] !== 'stop')
        .every((call) => call[2].env.LYK_DESKTOP_SAFE_STOP === ''),
    ).toBe(true);
  });
  it('persists private default preferences and preserves the last saved value on failure', async () => {
    const { root, manager, options, calls } = await fixture();
    expect(await manager.getPreferences()).toEqual({ autoStart: true, autoStop: false });
    await manager.setPreferences({ autoStart: false, autoStop: true });
    expect(await new DaemonManager(options).getPreferences()).toEqual({
      autoStart: false,
      autoStop: true,
    });
    expect((await stat(path.join(root, 'desktop-preferences.json'))).mode & 0o777).toBe(0o600);
    await rm(path.join(root, 'desktop-preferences.json'));
    await mkdir(path.join(root, 'desktop-preferences.json'));
    await expect(manager.setPreferences({ autoStart: true, autoStop: false })).rejects.toThrow();
    expect(await manager.getPreferences()).toEqual({ autoStart: false, autoStop: true });
    expect(calls).toEqual([]);
  });
  it('falls back to launch defaults when preference JSON is damaged', async () => {
    const f = await fixture();
    await writeFile(path.join(f.root, 'desktop-preferences.json'), '{broken', { mode: 0o600 });
    expect(await f.manager.getPreferences()).toEqual({ autoStart: true, autoStop: false });
  });
  it('authenticates before automatic start, isolates config, reports diagnostics and retains stopped ID', async () => {
    const f = await fixture();
    expect((await f.manager.getSnapshot()).status).toBe('stopped');
    expect(f.calls).not.toContain('daemon start');
    const snapshot = await f.manager.configureSession(session);
    expect(snapshot).toMatchObject({
      status: 'running',
      daemonID: 'daemon-alice',
      pid: 123,
      uptime: 17,
      workspaceCount: 2,
      cliAvailable: true,
      availability: 'available',
    });
    const command = f.execute.mock.calls.find((c) => c[1][1] === 'start')!;
    expect(command[2].env.LYK_CONFIG).toContain('/profiles/desktop-');
    expect(command[2].env.LYK_DAEMON_PROFILE).toMatch(/^desktop-/);
    const saved = JSON.parse(await readFile(command[2].env.LYK_CONFIG!, 'utf8'));
    expect(saved).toEqual({ server: origin, token: 'lyk_alice', workspace: '' });
    expect(JSON.stringify(snapshot)).not.toContain('lyk_alice');
    expect(await f.manager.stop()).toMatchObject({
      status: 'stopped',
      daemonID: 'daemon-alice',
      pid: null,
      uptime: null,
    });
    expect((await new DaemonManager(f.options).getSnapshot()).daemonID).toBe('daemon-alice');
  });
  it('exposes only the verified account for recovery after a stopped account switch', async () => {
    const f = await fixture();
    expect((await f.manager.getSnapshot()).authenticatedUserID).toBeNull();
    await f.manager.configureSession(session);
    await f.manager.setPreferences({ autoStart: false, autoStop: false });
    const switched = await f.manager.configureSession({
      ...session,
      userID: 'bob',
      sessionToken: 'jwt-bob',
    });
    expect(switched).toMatchObject({
      authenticatedUserID: 'bob',
      status: 'stopped',
      daemonID: null,
    });
    expect(f.calls.filter((call) => call === 'daemon start')).toHaveLength(1);
    expect(await new DaemonManager(f.options).getSnapshot()).toMatchObject({
      authenticatedUserID: null,
      daemonID: null,
    });
    expect(await f.manager.start()).toMatchObject({
      authenticatedUserID: 'bob',
      daemonID: 'daemon-bob',
      status: 'running',
    });
    expect((await f.manager.clearSession()).authenticatedUserID).toBeNull();
  });
  it('revokes verified ownership synchronously while a status read is pending', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const gate = deferred<void>();
    const execute = f.execute.getMockImplementation()!;
    let reading = false;
    f.execute.mockImplementationOnce(async (...args) => {
      reading = true;
      await gate.promise;
      return execute(...args);
    });
    const pending = f.manager.getSnapshot();
    await vi.waitFor(() => expect(reading).toBe(true));
    const clear = f.manager.clearSession();
    gate.resolve();
    expect((await pending).authenticatedUserID).toBeNull();
    expect((await clear).authenticatedUserID).toBeNull();
  });
  it('reuses only a cached token whose account was independently verified', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const second = new DaemonManager(f.options);
    await second.configureSession(session);
    expect(f.request.mock.calls.filter((c) => c[1].method === 'POST')).toHaveLength(1);
    expect(f.request.mock.calls.filter((c) => c[1].token === 'lyk_alice')).toHaveLength(1);
  });
  it('reconnects to the same owned daemon after quit without another start', async () => {
    const f = await fixture();
    const first = await f.manager.configureSession(session);
    await f.manager.beforeQuit();
    const reopened = await new DaemonManager(f.options).configureSession(session);
    expect(reopened).toMatchObject({ status: 'running', daemonID: first.daemonID, pid: first.pid });
    expect(f.calls.filter((call) => call === 'daemon start')).toHaveLength(1);
    expect(f.calls).not.toContain('daemon stop');
  });
  it('does not adopt or stop a daemon reporting the default profile', async () => {
    const f = await fixture();
    const execute = f.execute.getMockImplementation()!;
    f.execute.mockImplementation(async (file, args, options) => {
      if (args[1] === 'status')
        return JSON.stringify({ status: 'running', profile: '', server_url: origin, pid: 123 });
      return execute(file, args, options);
    });
    await expect(f.manager.configureSession(session)).rejects.toThrow(
      'Cannot start an unverified daemon',
    );
    const status = await f.manager.getSnapshot();
    expect(status.status).toBe('unknown');
    expect(status.pid).toBeNull();
    expect(f.calls).not.toContain('daemon start');
    expect(f.calls).not.toContain('daemon stop');
  });
  it('rejects arbitrary API origins before sending credentials and rejects wrong session identities', async () => {
    const f = await fixture();
    await expect(
      f.manager.configureSession({ ...session, apiOrigin: 'https://evil.example' }),
    ).rejects.toThrow();
    expect(f.request).not.toHaveBeenCalled();
    await expect(
      f.manager.configureSession({ ...session, sessionToken: 'jwt-bob' }),
    ).rejects.toThrow();
    expect(f.calls).not.toContain('daemon start');
  });
  it('replaces a cached token belonging to another account', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    await writeFile(config, JSON.stringify({ server: origin, token: 'lyk_bob', workspace: '' }));
    await new DaemonManager(f.options).configureSession(session);
    expect(f.request.mock.calls.filter((c) => c[1].method === 'POST')).toHaveLength(2);
    expect(f.calls).toContain('daemon stop');
    expect(f.calls.filter((c) => c === 'daemon start')).toHaveLength(2);
  });
  it('prevents duplicate starts and preference changes have no immediate lifecycle effect', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await Promise.all([f.manager.start(), f.manager.start()]);
    await f.manager.setPreferences({ autoStart: false, autoStop: true });
    expect(f.calls.filter((c) => c === 'daemon start')).toHaveLength(1);
    expect(f.calls).not.toContain('daemon stop');
  });
  it.each([false, true])('actual quit obeys autoStop=%s', async (autoStop) => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.setPreferences({ autoStart: true, autoStop });
    await f.manager.beforeQuit();
    expect(f.calls.includes('daemon stop')).toBe(autoStop);
  });
  it('fences logout while token mint is pending and removes credentials', async () => {
    const f = await fixture();
    const gate = deferred<unknown>();
    f.request.mockImplementation(async (_url, options) =>
      options.method === 'POST' ? (gate.promise as Promise<{ token: string }>) : { id: 'alice' },
    );
    const pending = f.manager.configureSession(session);
    await vi.waitFor(() => expect(f.request).toHaveBeenCalledTimes(2));
    const clear = f.manager.clearSession();
    gate.resolve({ token: 'lyk_alice' });
    await pending;
    await clear;
    expect(f.calls).not.toContain('daemon start');
    await expect(f.manager.start()).rejects.toThrow();
    expect(JSON.stringify(await f.manager.getSnapshot())).not.toContain('lyk_');
  });
  it('fences a pending previous-account token when another account signs in', async () => {
    const f = await fixture();
    const gate = deferred<unknown>();
    const original = f.request.getMockImplementation()!;
    f.request.mockImplementation((url, options) =>
      options.method === 'POST' && options.token === 'jwt-alice'
        ? (gate.promise as Promise<{ token: string }>)
        : original(url, options),
    );
    const alice = f.manager.configureSession(session);
    await vi.waitFor(() => expect(f.request).toHaveBeenCalledTimes(2));
    const bob = f.manager.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' });
    gate.resolve({ token: 'lyk_alice' });
    await alice;
    expect(await bob).toMatchObject({ status: 'running', daemonID: 'daemon-bob' });
    expect(f.calls.filter((c) => c === 'daemon start')).toHaveLength(1);
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    expect(JSON.parse(await readFile(config, 'utf8')).token).toBe('lyk_bob');
  });
  it('stops a start completed after logout before clearing its credentials', async () => {
    const f = await fixture();
    const gate = deferred<string>();
    const original = f.execute.getMockImplementation()!;
    f.execute.mockImplementation(async (file, args, options) => {
      const output = await original(file, args, options);
      if (args[1] === 'start') await gate.promise;
      return output;
    });
    const pending = f.manager.configureSession(session);
    await vi.waitFor(() => expect(f.calls).toContain('daemon start'));
    const clear = f.manager.clearSession();
    gate.resolve('');
    await pending;
    await clear;
    expect(f.calls.indexOf('daemon stop')).toBeGreaterThan(f.calls.indexOf('daemon start'));
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    await expect(readFile(config)).rejects.toThrow();
  });
  it('revokes in-memory control immediately even if logout stop fails', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    await writeFile(path.join(path.dirname(config), 'daemon.pid'), '{}');
    f.execute.mockRejectedValue(new Error('cannot stop'));
    await expect(f.manager.clearSession()).rejects.toThrow();
    await expect(f.manager.start()).rejects.toThrow('Sign in');
  });
  it('leaves an in-flight start running on quit when autoStop is false', async () => {
    const f = await fixture();
    const gate = deferred<string>();
    const original = f.execute.getMockImplementation()!;
    f.execute.mockImplementation(async (file, args, options) => {
      const output = await original(file, args, options);
      if (args[1] === 'start') await gate.promise;
      return output;
    });
    const pending = f.manager.configureSession(session);
    await vi.waitFor(() => expect(f.calls).toContain('daemon start'));
    const quit = f.manager.beforeQuit();
    gate.resolve('');
    await pending;
    await quit;
    expect(f.calls).not.toContain('daemon stop');
  });
  it('treats CLI stop with no ownership record as already stopped', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.stop();
    const original = f.execute.getMockImplementation()!;
    f.execute.mockImplementation((file, args, options) =>
      args[1] === 'stop' ? Promise.reject(new Error('not running')) : original(file, args, options),
    );
    expect((await f.manager.clearSession()).status).toBe('stopped');
  });
  it('does not erase live daemon identity when stop lacks an ownership record', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const original = f.execute.getMockImplementation()!;
    f.execute.mockImplementation((file, args, options) =>
      args[1] === 'stop'
        ? Promise.reject(new Error('no ownership'))
        : original(file, args, options),
    );
    await expect(
      f.manager.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' }),
    ).rejects.toThrow();
    expect((await f.manager.getSnapshot()).daemonID).toBe('daemon-alice');
  });
  it('refuses to replace identity when a failed-stop profile still has a health record', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.stop();
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    await writeFile(path.join(path.dirname(config), 'daemon.health.json'), '{}', { mode: 0o600 });
    const original = f.execute.getMockImplementation()!;
    f.execute.mockImplementation((file, args, options) =>
      args[1] === 'stop'
        ? Promise.reject(new Error('unverifiable ownership'))
        : original(file, args, options),
    );
    await expect(
      f.manager.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' }),
    ).rejects.toThrow();
    expect((await f.manager.getSnapshot()).daemonID).toBe('daemon-alice');
  });
  it('retains credentials when missing ownership records cannot verify stopped status', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    f.execute.mockRejectedValue(new Error('state lock held; status unverified'));
    await expect(f.manager.clearSession()).rejects.toThrow();
    expect(JSON.parse(await readFile(config, 'utf8')).token).toBe('lyk_alice');
    await expect(f.manager.start()).rejects.toThrow('Sign in');
    expect(await f.manager.getSnapshot()).toMatchObject({
      status: 'unknown',
      daemonID: 'daemon-alice',
    });
  });
  it('keeps the daemon lock inode across account state reset', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.setPreferences({ autoStart: false, autoStop: false });
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    const lock = path.join(path.dirname(config), 'daemon/daemon.lock');
    await writeFile(lock, '', { mode: 0o600 });
    const inode = (await stat(lock)).ino;
    await f.manager.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' });
    expect((await stat(lock)).ino).toBe(inode);
  });
  it('refuses account reset when stop succeeds but verified status stays live', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    const original = f.execute.getMockImplementation()!;
    f.execute.mockImplementation((file, args, options) =>
      args[1] === 'stop' ? Promise.resolve('') : original(file, args, options),
    );
    await expect(
      f.manager.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' }),
    ).rejects.toThrow();
    expect((await f.manager.getSnapshot()).daemonID).toBe('daemon-alice');
  });
  it('replaces an unowned daemon identity only after stopping it', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.setPreferences({ autoStart: false, autoStop: false });
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    await rm(path.join(path.dirname(config), 'owner.json'));
    await new DaemonManager(f.options).configureSession({
      ...session,
      userID: 'bob',
      sessionToken: 'jwt-bob',
    });
    expect(f.calls).toContain('daemon stop');
    expect((await f.manager.getSnapshot()).daemonID).toBeNull();
  });
  it('refuses symlinked config instead of adopting a terminal credential', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.stop();
    const config = f.execute.mock.calls[0][2].env.LYK_CONFIG!;
    const terminal = path.join(f.root, 'terminal-config.json');
    await writeFile(
      terminal,
      JSON.stringify({ server: origin, token: 'lyk_terminal', workspace: '' }),
    );
    await rm(config);
    await symlink(terminal, config);
    await expect(new DaemonManager(f.options).configureSession(session)).rejects.toThrow();
    expect(f.request.mock.calls.some((c) => c[1].token === 'lyk_terminal')).toBe(false);
    expect(JSON.parse(await readFile(terminal, 'utf8')).token).toBe('lyk_terminal');
  });
  it('clears prior owner identity on an account switch before starting the next account', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    await f.manager.setPreferences({ autoStart: false, autoStop: false });
    await f.manager.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' });
    expect(f.calls).toContain('daemon stop');
    expect((await f.manager.getSnapshot()).daemonID).toBeNull();
  });
  it('never reports mismatched daemon diagnostics as running', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    f.execute.mockResolvedValue(
      JSON.stringify({
        status: 'running',
        profile: 'default',
        server_url: origin,
        daemon_id: 'somebody-else',
        pid: 10,
      }),
    );
    expect(await f.manager.getSnapshot()).toMatchObject({
      status: 'unknown',
      daemonID: 'daemon-alice',
    });
  });
  it('retains verified same-account diagnostics when automatic start lacks the CLI', async () => {
    const f = await fixture();
    await f.manager.configureSession(session);
    f.calls.length = 0;
    const missing = new DaemonManager({ ...f.options, cliAvailable: async () => false });
    await expect(missing.configureSession(session)).rejects.toThrow('Development lyk is missing');
    const value = await missing.getSnapshot();
    expect(value).toMatchObject({
      authenticatedUserID: 'alice',
      daemonID: 'daemon-alice',
      cliAvailable: false,
      status: 'unknown',
      error: expect.stringContaining('desktop:dev'),
    });
    expect(f.calls).toEqual([]);
    expect(f.request.mock.calls.filter((call) => call[1].token === 'lyk_alice')).toHaveLength(1);
  });
  it('verifies a fresh stopped profile without a CLI when auto-start is disabled', async () => {
    const f = await fixture();
    await f.manager.setPreferences({ autoStart: false, autoStop: false });
    const missing = new DaemonManager({ ...f.options, cliAvailable: async () => false });
    const value = await missing.configureSession(session);
    expect(value).toMatchObject({
      authenticatedUserID: 'alice',
      daemonID: null,
      cliAvailable: false,
    });
    const config = path.join(f.root, 'profiles', value.profile!, 'config.json');
    expect(JSON.parse(await readFile(config, 'utf8'))).toEqual({
      server: origin,
      token: 'lyk_alice',
      workspace: '',
    });
    expect((await stat(config)).mode & 0o777).toBe(0o600);
    expect(JSON.stringify(value)).not.toContain('lyk_alice');
    expect(f.calls).toEqual([]);
  });
  it('fails closed on missing-CLI account turnover and cached-token replacement', async () => {
    const f = await fixture();
    const previous = await f.manager.configureSession(session);
    const config = path.join(f.root, 'profiles', previous.profile!, 'config.json');
    f.request.mockClear();
    const missing = new DaemonManager({ ...f.options, cliAvailable: async () => false });
    await expect(
      missing.configureSession({ ...session, userID: 'bob', sessionToken: 'jwt-bob' }),
    ).rejects.toThrow('Development lyk is missing');
    expect(f.request).not.toHaveBeenCalled();
    expect((await missing.getSnapshot()).authenticatedUserID).toBeNull();
    expect(JSON.parse(await readFile(config, 'utf8')).token).toBe('lyk_alice');
    await writeFile(config, JSON.stringify({ server: origin, token: 'lyk_bob', workspace: '' }), {
      mode: 0o600,
    });
    await expect(missing.configureSession(session)).rejects.toThrow('Development lyk is missing');
    expect((await missing.getSnapshot()).authenticatedUserID).toBeNull();
    expect(JSON.parse(await readFile(config, 'utf8')).token).toBe('lyk_bob');
    expect(f.request.mock.calls.filter((call) => call[1].method === 'POST')).toHaveLength(0);
  });
  it('shows unavailable native and missing CLI states explicitly', async () => {
    const f = await fixture();
    const missing = new DaemonManager({ ...f.options, cliAvailable: async () => false });
    expect(await missing.getSnapshot()).toMatchObject({
      cliAvailable: false,
      error: expect.stringContaining('desktop:dev'),
    });
    await expect(missing.configureSession(session)).rejects.toThrow();
    const unsupported = new DaemonManager({ ...f.options, supported: false });
    expect(await unsupported.getSnapshot()).toMatchObject({ availability: 'unavailable' });
    await expect(unsupported.start()).rejects.toThrow();
    f.request.mockClear();
    await expect(unsupported.configureSession(session)).rejects.toThrow(
      'not available on this platform',
    );
    expect(f.request).not.toHaveBeenCalled();
  });
});

it('dashboard logout immediately fences an in-flight native credential mint before daemon start', async () => {
  const f = await fixture();
  const gate = deferred<void>();
  const request = f.request.getMockImplementation()!;
  let minting = false;
  f.request.mockImplementation(async (url, options) => {
    if (options.method === 'POST') {
      minting = true;
      await gate.promise;
    }
    return request(url, options);
  });
  const bridge = {
    getSnapshot: () => f.manager.getSnapshot(),
    configureSession: (value: typeof session) => f.manager.configureSession(value),
    clearSession: () => f.manager.clearSession(),
  };
  const ui = createDaemonSession({
    bridge: bridge as never,
    apiOrigin: origin,
    token: async () => ({ token: session.sessionToken }),
    reportError: () => {},
  });
  const login = ui.update(session.userID);
  await vi.waitFor(() => expect(minting).toBe(true));
  const logout = ui.clear();
  gate.resolve();
  await Promise.all([login, logout]);
  expect(f.calls).not.toContain('daemon start');
  const snapshot = await f.manager.getSnapshot();
  expect(snapshot.authenticatedUserID).toBeNull();
  await expect(
    readFile(path.join(f.root, 'profiles', snapshot.profile!, 'config.json')),
  ).rejects.toMatchObject({ code: 'ENOENT' });
  ui.dispose();
});

describe('unsupported desktop daemon', () => {
  it('does not touch an existing preview profile, even on logout and quit', async () => {
    const f = await fixture();
    const previous = await f.manager.configureSession(session);
    const config = path.join(f.root, 'profiles', previous.profile!, 'config.json');
    const data = await readFile(config, 'utf8');
    const prefs = path.join(f.root, 'desktop-preferences.json');
    await writeFile(prefs, JSON.stringify({ autoStart: false, autoStop: true }), { mode: 0o600 });
    const oldPrefs = await readFile(prefs, 'utf8');
    const unsupported = new DaemonManager({ ...f.options, supported: false, packaged: true });
    f.execute.mockClear();
    expect(await unsupported.getPreferences()).toEqual({ autoStart: true, autoStop: false });
    await expect(
      unsupported.setPreferences({ autoStart: false, autoStop: false }),
    ).rejects.toThrow();
    await unsupported.clearSession();
    await unsupported.beforeQuit();
    expect(await readFile(config, 'utf8')).toBe(data);
    expect(await readFile(prefs, 'utf8')).toBe(oldPrefs);
    expect(f.execute).not.toHaveBeenCalled();
  });
});

describe('desktop IPC boundary', () => {
  it('accepts only the current preview window main frame with a trusted origin', () => {
    const frame = { url: 'http://127.0.0.1:1431/settings' };
    const sender = { mainFrame: frame };
    expect(validDaemonSender({ sender, senderFrame: frame }, sender)).toBe(true);
    expect(validDaemonSender({ sender, senderFrame: { ...frame } }, sender)).toBe(false);
    expect(validDaemonSender({ sender: { mainFrame: frame }, senderFrame: frame }, sender)).toBe(
      false,
    );
    frame.url = 'http://evil.example';
    expect(validDaemonSender({ sender, senderFrame: frame }, sender)).toBe(false);
  });
  it('rejects malformed requests and executable/path injection', () => {
    expect(() => validateDaemonRequest('start', { executable: '/evil' })).toThrow();
    expect(() =>
      validateDaemonRequest('setPreferences', { autoStart: true, autoStop: 'false' }),
    ).toThrow();
    expect(() => validateDaemonRequest('configureSession', { ...session, path: '/tmp' })).toThrow();
    expect(() => validateDaemonRequest('configureSession', session)).not.toThrow();
  });
});

describe('registered daemon IPC and status events', () => {
  it('accepts only the selected bundled entry and rejects a session for another deployment', async () => {
    const f = await fixture();
    const handlers = new Map<
      string,
      (event: { sender: unknown; senderFrame: unknown }, value?: unknown) => unknown
    >();
    const frame = { url: `${origin}/__lykeion_desktop__/#/studies` };
    const sender = { mainFrame: frame };
    const event = { sender, senderFrame: frame };
    installDaemonIPC(
      {
        handle: (name, handler) => {
          handlers.set(name, handler);
        },
      },
      () => sender,
      f.manager,
      'file:///bundle/index.html',
      origin,
    );
    const configure = handlers.get('lykeion:daemon:configureSession')!;
    expect(await handlers.get('lykeion:daemon:getPreferences')!(event)).toEqual({
      autoStart: true,
      autoStop: false,
    });
    expect(() => configure({ sender, senderFrame: { ...frame } }, session)).toThrow(
      'Invalid sender',
    );
    await expect(
      configure(event, { ...session, apiOrigin: 'https://other.example' }),
    ).rejects.toThrow('Invalid desktop session target');
    expect(f.request).not.toHaveBeenCalled();
    frame.url = 'https://other.example/__lykeion_desktop__/';
    expect(() => configure(event, session)).toThrow('Invalid sender');
    frame.url = `${origin}/api/me`;
    expect(() => configure(event, session)).toThrow('Invalid sender');
    frame.url = `${origin}/__lykeion_desktop__/#/studies`;
    expect(await configure(event, session)).toMatchObject({
      status: 'running',
      authenticatedUserID: 'alice',
    });
  });
  it('enforces sender and payload validation before dispatch, allowing only packaged reads', async () => {
    const f = await fixture();
    const handlers = new Map<
      string,
      (event: { sender: unknown; senderFrame: unknown }, value?: unknown) => unknown
    >();
    const frame = { url: 'http://127.0.0.1:1431/' };
    const sender = { mainFrame: frame };
    const event = { sender, senderFrame: frame };
    installDaemonIPC(
      {
        handle: (name, handler) => {
          handlers.set(name, handler);
        },
      },
      () => sender,
      f.manager,
      'file:///bundle/index.html',
    );
    const configure = handlers.get('lykeion:daemon:configureSession')!;
    expect(() => configure({ ...event, senderFrame: {} }, session)).toThrow('Invalid sender');
    expect(() => configure(event, { ...session, executable: '/evil' })).toThrow(
      'Invalid daemon request',
    );
    expect(f.request).not.toHaveBeenCalled();
    frame.url = 'file:///bundle/index.html';
    expect(() => configure(event, session)).toThrow('Invalid sender');
    expect(await handlers.get('lykeion:daemon:getPreferences')!(event)).toEqual({
      autoStart: true,
      autoStop: false,
    });
    frame.url = 'file:///other.html';
    expect(() => handlers.get('lykeion:daemon:getPreferences')!(event)).toThrow('Invalid sender');
  });
  it('disposes status subscriptions and polls at five seconds', async () => {
    const f = await fixture();
    const receive = vi.fn();
    const dispose = f.manager.onStatusChange(receive);
    await f.manager.getSnapshot();
    expect(receive).toHaveBeenCalledOnce();
    dispose();
    await f.manager.getSnapshot();
    expect(receive).toHaveBeenCalledOnce();
    vi.useFakeTimers();
    try {
      f.manager.startPolling();
      const count = f.calls.length;
      await vi.advanceTimersByTimeAsync(4999);
      expect(f.calls).toHaveLength(count);
      await vi.advanceTimersByTimeAsync(1);
      await vi.waitFor(() => expect(f.calls).toHaveLength(count + 1));
      await f.manager.beforeQuit();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('desktop authentication HTTP transport', () => {
  it('refuses redirects before forwarding the bearer token', async () => {
    let leaked = false;
    const target = createServer((_request, response) => {
      leaked = true;
      response.end('{}');
    });
    await new Promise<void>((resolve) => target.listen(0, '127.0.0.1', resolve));
    const source = createServer((_request, response) => {
      response.writeHead(302, {
        Location: `http://127.0.0.1:${(target.address() as AddressInfo).port}/api/me`,
      });
      response.end();
    });
    await new Promise<void>((resolve) => source.listen(0, '127.0.0.1', resolve));
    try {
      await expect(
        requestDaemonAPI(`http://127.0.0.1:${(source.address() as AddressInfo).port}/api/me`, {
          method: 'GET',
          token: 'jwt-secret',
        }),
      ).rejects.toThrow();
      expect(leaked).toBe(false);
    } finally {
      source.closeAllConnections();
      target.closeAllConnections();
      await Promise.all([
        new Promise<void>((r) => source.close(() => r())),
        new Promise<void>((r) => target.close(() => r())),
      ]);
    }
  });
});
