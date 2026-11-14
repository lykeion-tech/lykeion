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
import os from 'node:os';
import type {
  DaemonPreferences,
  DaemonSession,
  DaemonSnapshot,
  DesktopPrerequisites,
} from './daemon-types.js';
import { validPreferences, validSession } from './daemon-ipc.js';
import type { DaemonRuntime } from './daemon-runtime.js';

type CommandOptions = { env: NodeJS.ProcessEnv; timeout: number };
type BrowserAuthorization = (id: string | null) => Promise<() => boolean>;
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
  authorizeBrowser?: () => BrowserAuthorization;
  // runtime and revision are the packaged daemon's copies and the source
  // revision it was built from; a running daemon from another revision is
  // replaced once it is idle.
  runtime?: DaemonRuntime;
  revision?: string;
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
      ownsInstance &&
      ['darwin', 'linux'].includes(platform) &&
      (packaged ? !developmentUrl : !!developmentUrl),
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
      (process.geteuid && info.uid !== process.geteuid()) ||
      info.nlink !== 1 ||
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
  if (
    !info.isDirectory() ||
    info.isSymbolicLink() ||
    (process.geteuid && info.uid !== process.geteuid())
  )
    throw new Error('Invalid desktop profile directory');
  await chmod(directory, 0o700);
}
async function atomicJSON(file: string, value: unknown, allowed?: () => boolean): Promise<boolean> {
  await privateDirectory(path.dirname(file));
  const temporary = `${file}.${randomUUID()}.tmp`;
  try {
    await writeFile(temporary, JSON.stringify(value), { mode: 0o600, flag: 'wx' });
    if (allowed && !allowed()) return false;
    await rename(temporary, file);
    return true;
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
  return readDesktopAuthResponse(response);
}
class RejectedDaemonCredential extends Error {}
export async function readDesktopAuthResponse(response: Response): Promise<unknown> {
  if (!response.ok || !response.body) {
    await response.body?.cancel();
    if (response.status === 401)
      throw new RejectedDaemonCredential('Desktop authentication request failed');
    throw new Error('Desktop authentication request failed');
  }
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
  private nextAdmission = 0;
  private latestAdmission = 0;
  private quitting = false;
  private queue: Promise<unknown> = Promise.resolve();
  private polling: ReturnType<typeof setInterval> | null = null;
  private resumePollingAfterQuit = false;
  private busy = 0;
  private refreshingPrerequisites = false;
  private live: { revision: string | null; activeWork: number | null } = {
    revision: null,
    activeWork: null,
  };
  private notice: string | null = null;
  private swapFailed: string | null = null;
  constructor(private readonly options: DaemonManagerOptions) {
    this.apiOrigin = origin(options.apiOrigin);
    const host = new URL(this.apiOrigin).host.replace(/[^a-z0-9.-]/gi, '-').toLowerCase();
    const hash = createHash('sha256').update(this.apiOrigin).digest('hex').slice(0, 12);
    this.profile = `${options.packaged ? 'desktop-packaged' : 'desktop'}-${host}-${hash}`;
    this.directory = path.join(options.configRoot, 'profiles', this.profile);
    this.config = path.join(this.directory, 'config.json');
    this.preferencesFile = path.join(
      options.configRoot,
      options.packaged ? 'desktop-packaged-preferences.json' : 'desktop-preferences.json',
    );
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
  verificationOwner() {
    const version = this.generation;
    const session = this.session;
    const current = (id: string) =>
      this.fresh(version) &&
      (!this.options.supported || (session?.userID === id && this.session === session));
    return {
      current,
      verify: async (id: string) => {
        if (!current(id)) return false;
        // A disabled manager cannot own a run. Its generation still fences a concurrent clear.
        if (!this.options.supported) return true;
        const owner = await readJSON(path.join(this.directory, 'owner.json'));
        const config = await readJSON(this.config);
        if (
          owner?.userID !== id ||
          config?.server !== this.apiOrigin ||
          typeof config.token !== 'string' ||
          !/^lyk_[A-Za-z0-9_-]+$/.test(config.token)
        )
          return false;
        const me = object(
          await this.request(this.apiOrigin + '/api/me', { method: 'GET', token: config.token }),
        );
        const status = await this.getSnapshot();
        return current(id) && me.id === id && !status.error && status.status !== 'unknown';
      },
    };
  }
  private async available(): Promise<boolean> {
    if (!this.options.supported) return false;
    try {
      if (this.options.cliAvailable) return await this.options.cliAvailable();
      if (this.options.packaged) return false;
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
  private daemonPath() {
    return [
      ...(process.env.PATH?.split(path.delimiter).filter(Boolean) ?? []),
      path.join(os.homedir(), '.local', 'bin'),
      '/usr/local/bin',
      ...(process.platform === 'darwin' ? ['/opt/homebrew/bin'] : []),
    ].join(path.delimiter);
  }
  async refreshPrerequisites(): Promise<DesktopPrerequisites> {
    if (!this.options.supported) throw new Error(this.unavailableMessage());
    if (this.refreshingPrerequisites) throw new Error('A setup check is already running');
    this.refreshingPrerequisites = true;
    try {
      const version = this.generation;
      if (!this.options.packaged && !this.session) throw new Error('Sign in before checking setup');
      const browser = await this.browserAuthorization()(this.session?.userID ?? null);
      const current = () => this.fresh(version) && browser();
      if (!current()) throw new Error('Desktop daemon session changed');
      const available = await this.available();
      if (!current()) throw new Error('Desktop daemon session changed');
      if (!available) return { cliAvailable: false };
      const raw = await this.execute(this.options.cliPath, ['daemon', 'prerequisites'], {
        env: { PATH: this.daemonPath(), HOME: os.homedir(), TMPDIR: os.tmpdir() },
        timeout: 10000,
      });
      if (!current()) throw new Error('Desktop daemon session changed');
      let result: unknown;
      try {
        result = JSON.parse(raw);
      } catch {
        throw new Error('Invalid prerequisite response');
      }
      if (
        !result ||
        typeof result !== 'object' ||
        Object.keys(result).some(
          (key) => !['sandbox', 'claude', 'codex', 'sandbox_issue'].includes(key),
        ) ||
        !('sandbox' in result) ||
        typeof result.sandbox !== 'boolean' ||
        !('claude' in result) ||
        typeof result.claude !== 'boolean' ||
        !('codex' in result) ||
        typeof result.codex !== 'boolean' ||
        ('sandbox_issue' in result &&
          (result.sandbox ||
            typeof result.sandbox_issue !== 'string' ||
            !['account_lookup', 'selected_home'].includes(result.sandbox_issue)))
      )
        throw new Error('Invalid prerequisite response');
      return {
        cliAvailable: true,
        sandbox: result.sandbox,
        claude: result.claude,
        codex: result.codex,
        ...('sandbox_issue' in result
          ? { sandboxIssue: result.sandbox_issue as 'account_lookup' | 'selected_home' }
          : {}),
      };
    } finally {
      this.refreshingPrerequisites = false;
    }
  }
  private async command(
    action: 'status' | 'start' | 'stop',
    allowed?: () => boolean,
    { binary = this.options.cliPath, idleOnly = false } = {},
  ): Promise<string> {
    await this.prepare();
    const config = await readJSON(this.config);
    if (config && config.server !== this.apiOrigin)
      throw new Error('Desktop daemon deployment mismatch');
    if (this.options.packaged && !config) {
      const health = await readJSON(path.join(this.directory, 'daemon.health.json'));
      if (health && (health.profile !== this.profile || health.server_url !== this.apiOrigin))
        throw new Error('Desktop daemon health record belongs to another deployment');
    }
    if (this.options.packaged) await this.requireCLI();
    if (allowed && !allowed()) throw new Error('Desktop daemon session changed');
    const runtime =
      this.options.packaged && this.session
        ? this.profile +
          '-' +
          createHash('sha256').update(this.session.userID).digest('hex').slice(0, 16)
        : undefined;
    return this.execute(
      binary,
      ['daemon', action, ...(action === 'status' ? ['--json'] : [])],
      {
        env: {
          ...process.env,
          PATH: this.daemonPath(),
          LYK_CONFIG: this.config,
          LYK_DAEMON_PROFILE: this.profile,
          ...(runtime
            ? {
                LYK_DAEMON_HOME: path.join(
                  os.tmpdir(),
                  'lykeion-' + process.getuid!() + '-' + runtime + '-workspaces',
                ),
                LYK_ENV_DIR: path.join(os.homedir(), '.lyk', 'envs-' + runtime),
              }
            : {}),
          LYK_DESKTOP_SAFE_STOP: action === 'stop' ? (idleOnly ? 'idle' : '1') : '',
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
    if (!this.options.supported) return Promise.resolve({ ...defaults });
    return this.enqueue(() => this.loadPreferences());
  }
  setPreferences(preferences: DaemonPreferences): Promise<DaemonPreferences> {
    if (!this.options.supported) return Promise.reject(new Error(this.unavailableMessage()));
    if (!validPreferences(preferences))
      return Promise.reject(new Error('Invalid daemon preferences'));
    const next = { ...preferences };
    const version = this.generation;
    const authorization = this.browserAuthorization();
    return this.enqueue(async () => {
      await this.loadPreferences();
      let allowed: (() => boolean) | undefined;
      if (this.options.packaged) {
        await this.requireCLI();
        allowed = await this.controlAuthorization(version, authorization);
      }
      if (!(await atomicJSON(this.preferencesFile, next, allowed)))
        throw new Error('Desktop daemon session changed');
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
    this.live = { revision: null, activeWork: null };
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
      this.live = {
        revision: typeof status.revision === 'string' && status.revision ? status.revision : null,
        activeWork:
          typeof status.active_work === 'number' &&
          Number.isInteger(status.active_work) &&
          status.active_work >= 0
            ? status.active_work
            : null,
      };
      // A daemon from before this app reports neither its revision nor its
      // work, so it cannot be replaced safely without the person.
      const unversioned =
        this.options.runtime && !this.live.revision
          ? 'This computer is still running the runtime from an earlier Lykeion. Restart it here once no work is running to finish the update.'
          : null;
      return this.emit({ ...snapshot, error: this.notice ?? unversioned });
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
  private async stopManaged(allowed?: () => boolean): Promise<void> {
    await this.requireCLI();
    try {
      await this.command('stop', allowed);
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
  private browserAuthorization(): BrowserAuthorization {
    if (!this.options.packaged) return async () => () => true;
    return (
      this.options.authorizeBrowser?.() ??
      (async () => {
        throw new Error('Desktop browser authentication unavailable');
      })
    );
  }
  async configureSession(input: DaemonSession): Promise<DaemonSnapshot> {
    if (!validSession(input) || input.apiOrigin !== this.apiOrigin)
      throw new Error('Invalid desktop session target');
    if (!this.options.supported) throw new Error(this.unavailableMessage());
    const value = { ...input };
    const admission = ++this.nextAdmission;
    const authorization = this.browserAuthorization();
    if (this.options.packaged) await this.requireCLI();
    const browser = await authorization(value.userID);
    let identity: Record<string, unknown>;
    try {
      identity = object(
        await this.request(this.apiOrigin + '/api/me', {
          token: value.sessionToken,
          method: 'GET',
        }),
      );
    } catch {
      throw new Error('Desktop session identity verification failed');
    }
    if (identity.id !== value.userID || !browser())
      throw new Error('Desktop session identity does not match account');
    if (admission < this.latestAdmission || this.quitting) return this.getSnapshot();
    this.latestAdmission = admission;
    const version = ++this.generation;
    const current = () => this.fresh(version) && browser();
    if (this.session?.userID !== value.userID) this.session = null;
    return this.enqueue(async () => {
      if (!current()) return this.snapshot();
      await this.prepare();
      const ownerFile = path.join(this.directory, 'owner.json');
      const owner = await readJSON(ownerFile);
      const state = await readJSON(path.join(this.directory, 'daemon', 'daemon.json'));
      if ((owner && owner.userID !== value.userID) || (!owner && state)) {
        await this.stopManaged(current);
        if (!current()) return this.snapshot();
        await this.clearCredentials();
        const stateDirectory = path.join(this.directory, 'daemon');
        for (const name of await readdir(stateDirectory)) {
          if (name !== 'daemon.lock')
            await rm(path.join(stateDirectory, name), { recursive: true, force: true });
        }
        await rm(ownerFile, { force: true });
      }
      if (!current()) return this.snapshot();
      const cached = await readJSON(this.config);
      let token: string | null = null;
      if (
        owner?.userID === value.userID &&
        cached?.server === this.apiOrigin &&
        typeof cached.token === 'string' &&
        cached.token.startsWith('lyk_')
      ) {
        try {
          const identity = object(
            await this.request(this.apiOrigin + '/api/me', { token: cached.token, method: 'GET' }),
          );
          if (typeof identity.id !== 'string' || !identity.id)
            throw new Error('Invalid cached identity');
          if (identity.id === value.userID) token = cached.token;
        } catch (error) {
          if (!(error instanceof RejectedDaemonCredential))
            // eslint-disable-next-line preserve-caught-error -- upstream parse errors may contain credentials.
            throw new Error('Could not verify the existing desktop token');
        }
      }
      if (!current()) return this.snapshot();
      if (!token) {
        if (cached || owner?.userID === value.userID) await this.stopManaged(current);
        if (!current()) return this.snapshot();
        if (this.options.packaged) await this.requireCLI();
        if (!current()) return this.snapshot();
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
        if (!current()) return this.snapshot();
        if (
          typeof minted.token !== 'string' ||
          !minted.token.startsWith('lyk_') ||
          /[\r\n]/.test(minted.token)
        )
          throw new Error('Invalid desktop token response');
        token = minted.token;
      }
      if (this.options.packaged) await this.requireCLI();
      if (!current()) return this.snapshot();
      if (
        !(await atomicJSON(ownerFile, { userID: value.userID }, current)) ||
        !(await atomicJSON(this.config, { server: this.apiOrigin, token, workspace: '' }, current))
      )
        return this.snapshot();
      if (!current()) return this.snapshot();
      this.session = { userID: value.userID };
      if ((await this.loadPreferences()).autoStart) return this.startManaged(version, current);
      return this.snapshot();
    });
  }
  clearSession(): Promise<DaemonSnapshot> {
    this.latestAdmission = ++this.nextAdmission;
    ++this.generation;
    this.session = null;
    if (!this.options.supported) return this.getSnapshot();
    return this.enqueue(async () => {
      const configured = await readJSON(this.config);
      if (!configured && this.options.packaged && !(await this.available())) {
        // Only the empty directories created by status polling can be cleared without a working CLI.
        await this.prepare();
        const entries = await readdir(this.directory);
        const daemonEntries = await readdir(path.join(this.directory, 'daemon'));
        if (entries.every((name) => name === 'daemon') && !daemonEntries.length)
          return this.snapshot();
      }
      if (configured || (await this.snapshot()).status !== 'stopped') await this.stopManaged();
      await this.clearCredentials();
      const cleared = await this.snapshot();
      if (cleared.status !== 'stopped')
        throw new Error('Desktop daemon stop could not be verified');
      return cleared;
    });
  }
  private async startManaged(
    version: number,
    allowed = () => this.fresh(version),
  ): Promise<DaemonSnapshot> {
    if (!this.session || !allowed()) return this.snapshot();
    await this.requireCLI();
    const current = await this.snapshot();
    if (!allowed() || current.status === 'running' || current.status === 'starting') return current;
    if (current.status !== 'stopped') throw new Error('Cannot start an unverified daemon');
    this.emit({ ...current, status: 'starting', error: null });
    const runtime = this.options.runtime;
    if (!runtime) {
      try {
        await this.command('start', allowed);
      } catch {
        throw new Error('Desktop daemon failed to start; check the local daemon log');
      }
      // A superseding clear, identity switch or auto-stop already owns teardown in the queue.
      return this.snapshot();
    }
    const previous = await runtime.lastGood();
    try {
      await this.command('start', allowed, { binary: await runtime.install() });
      const started = await this.snapshot();
      if (started.status === 'running') {
        await runtime.started(runtime.sha256);
        this.notice = null;
      }
      return started;
    } catch {
      if (!allowed() || !previous || previous.sha256 === runtime.sha256)
        throw new Error('Desktop daemon failed to start; check the local daemon log');
    }
    // The new runtime did not start, so the last one that did runs again.
    this.swapFailed = this.options.revision ?? null;
    if ((await this.snapshot()).status !== 'stopped')
      throw new Error('Desktop daemon failed to start; check the local daemon log');
    try {
      await this.command('start', allowed, { binary: previous.path });
    } catch {
      throw new Error('Desktop daemon failed to start; check the local daemon log');
    }
    this.notice =
      'The updated Lykeion runtime failed to start, so the previous version is running. Reinstall the app to retry.';
    return this.snapshot();
  }
  // replaceOutdated restarts a daemon built from another revision on this
  // app's runtime, but only through an idle-only stop: the daemon refuses
  // while any work is active or being claimed, and it is tried again later.
  private async replaceOutdated(version: number, allowed: () => boolean): Promise<void> {
    const target = this.options.revision;
    if (!this.options.runtime || !target || this.swapFailed === target || !allowed()) return;
    const current = await this.snapshot();
    if (
      current.status !== 'running' ||
      !this.live.revision ||
      this.live.revision === target ||
      this.live.activeWork !== 0
    )
      return;
    try {
      await this.command('stop', allowed, { idleOnly: true });
    } catch {
      return;
    }
    if ((await this.snapshot()).status !== 'stopped' || !allowed()) return;
    await this.startManaged(version, allowed);
  }
  private async controlAuthorization(version: number, authorization: BrowserAuthorization) {
    const owner = this.session;
    if (!owner || !this.fresh(version))
      throw new Error('Sign in before controlling the desktop daemon');
    const browser = await authorization(owner.userID);
    const allowed = () => this.fresh(version) && this.session === owner && browser();
    if (!allowed()) throw new Error('Desktop daemon session changed');
    return allowed;
  }
  start(): Promise<DaemonSnapshot> {
    const version = this.generation;
    const authorization = this.browserAuthorization();
    return this.enqueue(async () =>
      this.startManaged(version, await this.controlAuthorization(version, authorization)),
    );
  }
  stop(): Promise<DaemonSnapshot> {
    const version = this.generation;
    const authorization = this.browserAuthorization();
    return this.enqueue(async () => {
      await this.stopManaged(await this.controlAuthorization(version, authorization));
      return this.snapshot();
    });
  }
  restart(): Promise<DaemonSnapshot> {
    const version = this.generation;
    const authorization = this.browserAuthorization();
    return this.enqueue(async () => {
      const allowed = await this.controlAuthorization(version, authorization);
      await this.stopManaged(allowed);
      return this.startManaged(version, allowed);
    });
  }
  // refresh reads the daemon's status and replaces a daemon from another
  // revision once it is idle.
  refresh(): Promise<void> {
    const version = this.generation;
    const owner = this.session;
    return this.enqueue(async () => {
      await this.snapshot();
      if (owner && this.live.revision && this.live.revision !== this.options.revision)
        await this.replaceOutdated(version, () => this.fresh(version) && this.session === owner);
    });
  }
  startPolling(): void {
    if (this.polling) return;
    this.polling = setInterval(() => {
      if (!this.busy && !this.quitting) void this.refresh().catch(() => undefined);
    }, 5000);
    this.polling.unref();
  }
  async beforeQuit(): Promise<void> {
    this.quitting = true;
    this.latestAdmission = ++this.nextAdmission;
    ++this.generation;
    if (this.polling) {
      this.resumePollingAfterQuit = true;
      clearInterval(this.polling);
      this.polling = null;
    }
    if (!this.options.supported) return;
    await this.enqueue(async () => {
      if ((await this.loadPreferences()).autoStop) await this.stopManaged();
    });
  }
  resumeAfterQuit(): void {
    this.quitting = false;
    if (this.resumePollingAfterQuit) {
      this.resumePollingAfterQuit = false;
      this.startPolling();
    }
  }
}
