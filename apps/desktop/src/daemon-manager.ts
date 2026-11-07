import { execFile } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import {
  access,
  chmod,
  lstat,
  mkdir,
  readFile,
  readdir,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import path from 'node:path';
import type { DaemonPreferences, DaemonSession, DaemonSnapshot } from './daemon-types.js';
import { validPreferences, validSession } from './daemon-ipc.js';

type CommandOptions = { env: NodeJS.ProcessEnv; timeout: number };
type RequestOptions = { token: string; method: string; body?: unknown };
export interface DaemonManagerOptions {
  configRoot: string;
  apiOrigin: string;
  cliPath: string;
  supported: boolean;
  packaged?: boolean;
  execute?: (file: string, args: string[], options: CommandOptions) => Promise<string>;
  request?: (url: string, options: RequestOptions) => Promise<unknown>;
  cliAvailable?: () => Promise<boolean>;
}
const defaults: DaemonPreferences = { autoStart: true, autoStop: false };
export function desktopDaemonOptions(
  platform: string,
  ownsInstance: boolean,
  packaged: boolean,
  directory: string,
  resourcesPath: string,
  developmentUrl?: string,
): { supported: boolean; cliPath: string } {
  return {
    supported:
      ownsInstance && ['darwin', 'linux'].includes(platform) && !packaged && !!developmentUrl,
    cliPath: packaged ? path.join(resourcesPath, 'bin', 'lyk') : path.join(directory, 'lyk'),
  };
}
function origin(value: string): string {
  const url = new URL(value);
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.pathname !== '/' ||
    url.search ||
    url.hash
  )
    throw new Error('Invalid configured API origin');
  return url.origin;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid daemon response');
  return value as Record<string, unknown>;
}
async function readJSON(file: string): Promise<Record<string, unknown> | null> {
  try {
    const info = await lstat(file);
    if (
      !info.isFile() ||
      info.isSymbolicLink() ||
      info.size > 64 * 1024 ||
      (info.mode & 0o077) !== 0
    )
      throw new Error('Invalid private desktop file');
    return object(JSON.parse(await readFile(file, 'utf8')));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return null;
    // Parser errors can include token-bearing JSON.
    // eslint-disable-next-line preserve-caught-error
    throw new Error('Cannot read desktop daemon configuration');
  }
}
async function privateDirectory(directory: string): Promise<void> {
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink())
    throw new Error('Invalid desktop profile directory');
  await chmod(directory, 0o700);
}
async function atomicJSON(file: string, value: unknown): Promise<void> {
  await privateDirectory(path.dirname(file));
  const temporary = `${file}.${randomUUID()}.tmp`;
  try {
    await writeFile(temporary, JSON.stringify(value), { mode: 0o600, flag: 'wx' });
    await rename(temporary, file);
  } finally {
    await rm(temporary, { force: true });
  }
}
export function executeDaemon(
  file: string,
  args: string[],
  options: CommandOptions,
): Promise<string> {
  return new Promise((resolve, reject) =>
    execFile(
      file,
      args,
      { ...options, maxBuffer: 64 * 1024, killSignal: 'SIGKILL', windowsHide: true },
      (error, stdout) =>
        error ? reject(new Error('Daemon command failed or timed out')) : resolve(stdout),
    ),
  );
}
export async function requestDaemonAPI(url: string, options: RequestOptions): Promise<unknown> {
  const response = await fetch(url, {
    method: options.method,
    headers: { Authorization: `Bearer ${options.token}`, 'Content-Type': 'application/json' },
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    redirect: 'error',
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok || !response.body) throw new Error('Desktop authentication request failed');
  const reader = response.body.getReader();
  let size = 0;
  const chunks: Uint8Array[] = [];
  try {
    for (;;) {
      const result = await reader.read();
      if (result.done) break;
      size += result.value.length;
      if (size > 64 * 1024) throw new Error('Desktop authentication response too large');
      chunks.push(result.value);
    }
  } finally {
    await reader.cancel();
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}
export class DaemonManager {
  private readonly apiOrigin: string;
  private readonly profile: string;
  private readonly directory: string;
  private readonly config: string;
  private readonly preferencesFile: string;
  private readonly execute: NonNullable<DaemonManagerOptions['execute']>;
  private readonly request: NonNullable<DaemonManagerOptions['request']>;
  private readonly listeners = new Set<(value: DaemonSnapshot) => void>();
  private preferences: DaemonPreferences | null = null;
  private session: { userID: string } | null = null;
  private generation = 0;
  private quitting = false;
  private queue: Promise<unknown> = Promise.resolve();
  private polling: ReturnType<typeof setInterval> | null = null;
  private busy = 0;
  constructor(private readonly options: DaemonManagerOptions) {
    this.apiOrigin = origin(options.apiOrigin);
    const host = new URL(this.apiOrigin).host.replace(/[^a-z0-9.-]/gi, '-').toLowerCase();
    const hash = createHash('sha256').update(this.apiOrigin).digest('hex').slice(0, 12);
    this.profile = `desktop-${host}-${hash}`;
    this.directory = path.join(options.configRoot, 'profiles', this.profile);
    this.config = path.join(this.directory, 'config.json');
    this.preferencesFile = path.join(options.configRoot, 'desktop-preferences.json');
    this.execute = options.execute ?? executeDaemon;
    this.request = options.request ?? requestDaemonAPI;
  }
  private enqueue<T>(operation: () => Promise<T>): Promise<T> {
    this.busy++;
    const result = this.queue.then(operation);
    this.queue = result
      .catch(() => undefined)
      .finally(() => {
        this.busy--;
      });
    return result;
  }
  private fresh(version: number): boolean {
    return version === this.generation && !this.quitting;
  }
  private async available(): Promise<boolean> {
    if (!this.options.supported) return false;
    try {
      if (this.options.cliAvailable) return await this.options.cliAvailable();
      const info = await lstat(this.options.cliPath);
      if (!info.isFile() || info.isSymbolicLink()) return false;
      await access(this.options.cliPath, constants.X_OK);
      return true;
    } catch {
      return false;
    }
  }
  private async requireCLI(): Promise<void> {
    if (!this.options.supported) throw new Error(this.unavailableMessage());
    if (!(await this.available())) throw new Error(this.missingCLIMessage());
  }
  private unavailableMessage(): string {
    return this.options.packaged
      ? 'The packaged Lykeion runtime is not yet available.'
      : 'Local daemon controls are not available on this platform';
  }
  private missingCLIMessage(): string {
    return this.options.packaged
      ? 'The bundled Lykeion runtime is missing or invalid. Reinstall the app.'
      : 'Development lyk is missing. Run npm run desktop:dev to build it.';
  }
  private async command(action: 'status' | 'start' | 'stop'): Promise<string> {
    await this.prepare();
    await readJSON(this.config);
    return this.execute(
      this.options.cliPath,
      ['daemon', action, ...(action === 'status' ? ['--json'] : [])],
      {
        env: {
          ...process.env,
          LYK_CONFIG: this.config,
          LYK_DAEMON_PROFILE: this.profile,
          LYK_DESKTOP_SAFE_STOP: action === 'stop' ? '1' : '',
        },
        timeout: action === 'start' ? 60000 : action === 'stop' ? 15000 : 5000,
      },
    );
  }
  private async prepare(): Promise<void> {
    await privateDirectory(this.options.configRoot);
    await privateDirectory(path.join(this.options.configRoot, 'profiles'));
    await privateDirectory(this.directory);
    await privateDirectory(path.join(this.directory, 'daemon'));
  }
  private async loadPreferences(): Promise<DaemonPreferences> {
    if (this.preferences) return { ...this.preferences };
    let saved: Record<string, unknown> | null = null;
    try {
      saved = await readJSON(this.preferencesFile);
    } catch {
      /* An unreadable preference file uses launch defaults until a successful save. */
    }
    this.preferences = validPreferences(saved) ? { ...saved } : { ...defaults };
    return { ...this.preferences };
  }
  getPreferences(): Promise<DaemonPreferences> {
    return this.enqueue(() => this.loadPreferences());
  }
  setPreferences(preferences: DaemonPreferences): Promise<DaemonPreferences> {
    if (!validPreferences(preferences))
      return Promise.reject(new Error('Invalid daemon preferences'));
    const next = { ...preferences };
    return this.enqueue(async () => {
      await this.loadPreferences();
      await atomicJSON(this.preferencesFile, next);
      this.preferences = next;
      return { ...next };
    });
  }
  private emit(snapshot: DaemonSnapshot): DaemonSnapshot {
    snapshot.authenticatedUserID = this.options.supported ? (this.session?.userID ?? null) : null;
    for (const listener of this.listeners) {
      try {
        listener({ ...snapshot });
      } catch {
        /* A closed window cannot interrupt daemon cleanup. */
      }
    }
    return snapshot;
  }
  onStatusChange(listener: (snapshot: DaemonSnapshot) => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
  private async snapshot(): Promise<DaemonSnapshot> {
    const cliAvailable = await this.available();
    const snapshot: DaemonSnapshot = {
      authenticatedUserID: null,
      status: 'unknown',
      availability: this.options.supported ? 'available' : 'unavailable',
      cliAvailable,
      error: null,
      pid: null,
      uptime: null,
      daemonID: null,
      profile: this.options.supported ? this.profile : null,
      serverURL: this.options.supported ? this.apiOrigin : null,
      deviceName: null,
      workspaceCount: null,
      os: null,
    };
    if (!this.options.supported)
      return this.emit({
        ...snapshot,
        error: this.unavailableMessage(),
      });
    try {
      await this.prepare();
      const state = await readJSON(path.join(this.directory, 'daemon', 'daemon.json'));
      if (
        state &&
        state.server === this.apiOrigin &&
        typeof state.daemon_id === 'string' &&
        state.daemon_id
      )
        snapshot.daemonID = state.daemon_id;
      if (!cliAvailable)
        return this.emit({
          ...snapshot,
          error: this.missingCLIMessage(),
        });
      const status = object(JSON.parse(await this.command('status')));
      if (status.profile !== this.profile) throw new Error('Daemon profile does not match desktop');
      if (status.status === 'stopped') return this.emit({ ...snapshot, status: 'stopped' });
      if (
        !['running', 'starting'].includes(String(status.status)) ||
        status.server_url !== this.apiOrigin ||
        !snapshot.daemonID ||
        status.daemon_id !== snapshot.daemonID ||
        typeof status.pid !== 'number' ||
        !Number.isInteger(status.pid) ||
        status.pid <= 0
      )
        throw new Error('Daemon identity could not be verified');
      snapshot.status = status.status as 'running' | 'starting';
      snapshot.pid = status.pid;
      if (typeof status.uptime === 'number' && Number.isFinite(status.uptime) && status.uptime >= 0)
        snapshot.uptime = status.uptime;
      if (typeof status.device_name === 'string') snapshot.deviceName = status.device_name;
      if (
        typeof status.workspace_count === 'number' &&
        Number.isInteger(status.workspace_count) &&
        status.workspace_count >= 0
      )
        snapshot.workspaceCount = status.workspace_count;
      if (typeof status.os === 'string') snapshot.os = status.os;
      return this.emit(snapshot);
    } catch {
      return this.emit({ ...snapshot, error: 'Desktop daemon status could not be verified' });
    }
  }
  getSnapshot(): Promise<DaemonSnapshot> {
    return this.enqueue(() => this.snapshot());
  }
  private async noHealthRecord(): Promise<boolean> {
    try {
      await lstat(path.join(this.directory, 'daemon.health.json'));
      return false;
    } catch (error) {
      return (error as NodeJS.ErrnoException).code === 'ENOENT';
    }
  }
  private async stopManaged(): Promise<void> {
    await this.requireCLI();
    try {
      await this.command('stop');
    } catch {
      try {
        await lstat(path.join(this.directory, 'daemon.pid'));
      } catch (error) {
        if (
          (error as NodeJS.ErrnoException).code === 'ENOENT' &&
          (await this.noHealthRecord()) &&
          (await this.snapshot()).status === 'stopped'
        )
          return;
      }
      throw new Error('Desktop daemon could not be stopped; account credentials were not replaced');
    }
    if ((await this.snapshot()).status !== 'stopped')
      throw new Error(
        'Desktop daemon stop could not be verified; account credentials were not replaced',
      );
  }
  private async clearCredentials(): Promise<void> {
    await rm(this.config, { force: true });
    this.session = null;
  }
  configureSession(input: DaemonSession): Promise<DaemonSnapshot> {
    if (!validSession(input) || input.apiOrigin !== this.apiOrigin)
      return Promise.reject(new Error('Invalid desktop session target'));
    if (!this.options.supported) return Promise.reject(new Error(this.unavailableMessage()));
    const value = { ...input };
    const version = ++this.generation;
    this.session = null;
    return this.enqueue(async () => {
      if (!this.fresh(version)) return this.snapshot();
      await this.prepare();
      const ownerFile = path.join(this.directory, 'owner.json');
      const owner = await readJSON(ownerFile);
      const state = await readJSON(path.join(this.directory, 'daemon', 'daemon.json'));
      if ((owner && owner.userID !== value.userID) || (!owner && state)) {
        await this.stopManaged();
        await this.clearCredentials();
        const stateDirectory = path.join(this.directory, 'daemon');
        for (const name of await readdir(stateDirectory)) {
          if (name !== 'daemon.lock')
            await rm(path.join(stateDirectory, name), { recursive: true, force: true });
        }
        await rm(ownerFile, { force: true });
      }
      if (!this.fresh(version)) return this.snapshot();
      let identity: Record<string, unknown>;
      try {
        identity = object(
          await this.request(`${this.apiOrigin}/api/me`, {
            token: value.sessionToken,
            method: 'GET',
          }),
        );
      } catch {
        throw new Error('Desktop session identity verification failed');
      }
      if (!this.fresh(version)) return this.snapshot();
      if (identity.id !== value.userID)
        throw new Error('Desktop session identity does not match account');
      const cached = await readJSON(this.config);
      let token: string | null = null;
      if (
        owner?.userID === value.userID &&
        cached?.server === this.apiOrigin &&
        typeof cached.token === 'string' &&
        cached.token.startsWith('lyk_')
      ) {
        try {
          if (
            object(
              await this.request(`${this.apiOrigin}/api/me`, {
                token: cached.token,
                method: 'GET',
              }),
            ).id === value.userID
          )
            token = cached.token;
        } catch {
          /* Mint a replacement only after verifying the session identity. */
        }
      }
      if (!this.fresh(version)) return this.snapshot();
      if (!token) {
        if (cached || owner?.userID === value.userID) await this.stopManaged();
        if (!this.fresh(version)) return this.snapshot();
        let minted: Record<string, unknown>;
        try {
          minted = object(
            await this.request(`${this.apiOrigin}/api/tokens`, {
              token: value.sessionToken,
              method: 'POST',
              body: { name: `Lykeion Desktop (${new URL(this.apiOrigin).host})` },
            }),
          );
        } catch {
          throw new Error('Desktop token creation failed');
        }
        if (!this.fresh(version)) return this.snapshot();
        if (
          typeof minted.token !== 'string' ||
          !minted.token.startsWith('lyk_') ||
          /[\r\n]/.test(minted.token)
        )
          throw new Error('Invalid desktop token response');
        token = minted.token;
      }
      await atomicJSON(ownerFile, { userID: value.userID });
      await atomicJSON(this.config, { server: this.apiOrigin, token, workspace: '' });
      if (!this.fresh(version)) return this.snapshot();
      this.session = { userID: value.userID };
      if ((await this.loadPreferences()).autoStart) return this.startManaged(version);
      return this.snapshot();
    });
  }
  clearSession(): Promise<DaemonSnapshot> {
    ++this.generation;
    this.session = null;
    return this.enqueue(async () => {
      const configured = await readJSON(this.config);
      if (configured || this.session) await this.stopManaged();
      await this.clearCredentials();
      return this.snapshot();
    });
  }
  private async startManaged(version: number): Promise<DaemonSnapshot> {
    if (!this.session || !this.fresh(version)) return this.snapshot();
    await this.requireCLI();
    const current = await this.snapshot();
    if (!this.fresh(version) || current.status === 'running' || current.status === 'starting')
      return current;
    if (current.status !== 'stopped') throw new Error('Cannot start an unverified daemon');
    this.emit({ ...current, status: 'starting', error: null });
    try {
      await this.command('start');
    } catch {
      throw new Error('Desktop daemon failed to start; check the local daemon log');
    }
    if (!this.fresh(version)) {
      if (!this.quitting || (await this.loadPreferences()).autoStop) await this.stopManaged();
      return this.snapshot();
    }
    return this.snapshot();
  }
  start(): Promise<DaemonSnapshot> {
    const version = this.generation;
    return this.enqueue(async () => {
      if (!this.session || !this.fresh(version))
        throw new Error('Sign in before starting the desktop daemon');
      return this.startManaged(version);
    });
  }
  stop(): Promise<DaemonSnapshot> {
    const version = this.generation;
    return this.enqueue(async () => {
      if (!this.session || !this.fresh(version))
        throw new Error('Sign in before stopping the desktop daemon');
      await this.stopManaged();
      return this.snapshot();
    });
  }
  restart(): Promise<DaemonSnapshot> {
    const version = this.generation;
    return this.enqueue(async () => {
      if (!this.session || !this.fresh(version))
        throw new Error('Sign in before restarting the desktop daemon');
      await this.stopManaged();
      return this.startManaged(version);
    });
  }
  startPolling(): void {
    if (this.polling) return;
    this.polling = setInterval(() => {
      if (!this.busy && !this.quitting) void this.getSnapshot().catch(() => undefined);
    }, 5000);
    this.polling.unref();
  }
  async beforeQuit(): Promise<void> {
    this.quitting = true;
    ++this.generation;
    if (this.polling) {
      clearInterval(this.polling);
      this.polling = null;
    }
    await this.enqueue(async () => {
      if ((await this.loadPreferences()).autoStop) await this.stopManaged();
    });
  }
}
