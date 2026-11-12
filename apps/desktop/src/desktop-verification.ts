import { randomUUID } from 'node:crypto';
import type { Cookie, Session } from 'electron';
import type { DaemonManager } from './daemon-manager.js';
import { readDesktopAuthResponse } from './daemon-manager.js';

function accountID(user: unknown): string {
  if (
    !user ||
    typeof user !== 'object' ||
    !('id' in user) ||
    typeof user.id !== 'string' ||
    !/^\S{1,255}$/.test(user.id)
  )
    throw new Error('Invalid verified account');
  return user.id;
}

type Owner = ReturnType<DaemonManager['verificationOwner']>;
type Attempt = {
  token: string;
  document: string;
  owner: Owner;
  before?: string;
  prepared?: string;
};

export class DesktopVerification {
  private attempt?: Attempt;
  private revision = 0;
  private receipt?: {
    key: string;
    document: string;
    id: string | null;
    revision: number;
    expires: number;
    sessionCookie: boolean;
  };
  private readKey?: string;

  constructor(
    private readonly origin: string,
    private readonly session: Pick<Session, 'fetch'> & { cookies: Pick<Session['cookies'], 'get'> },
    private readonly document: () => string | null,
    private readonly ready: () => boolean,
  ) {}

  reserve(token: string, document: string, owner: Owner) {
    this.invalidate();
    this.attempt = { token, document, owner };
  }
  invalidate() {
    this.revision++;
    this.attempt = undefined;
    this.receipt = undefined;
    this.readKey = undefined;
  }
  cookieChanged() {
    this.revision++;
  }
  private rootCookie(found: Cookie[]) {
    const cookie = found[0];
    if (
      found.length !== 1 ||
      !cookie?.hostOnly ||
      !cookie.httpOnly ||
      cookie.path !== '/' ||
      (cookie.secure && !this.origin.startsWith('https://')) ||
      cookie.domain !== new URL(this.origin).hostname ||
      !/^[A-Za-z0-9._-]{1,16384}$/.test(cookie.value)
    )
      return;
    return cookie;
  }
  private async identity(value: string) {
    const response = await fetch(this.origin + '/api/me', {
      headers: { Cookie: 'lyk_session=' + value },
      redirect: 'error',
      signal: AbortSignal.timeout(5000),
    });
    return accountID(await readDesktopAuthResponse(response));
  }
  async capture(token: string, headers: Record<string, string>): Promise<void> {
    const attempt = this.attempt;
    if (!attempt || attempt.token !== token) return;
    const revision = this.revision;
    try {
      const values = Object.entries(headers).filter(([key]) => key.toLowerCase() === 'cookie');
      if (values.length !== 1) return;
      const sessions = values[0]![1]
        .split(';')
        .map((part) => part.trim())
        .filter((part) => part.startsWith('lyk_session='));
      const cookie = this.rootCookie(
        await this.session.cookies.get({
          url: this.origin + '/api/me',
          name: 'lyk_session',
        }),
      );
      if (!cookie || sessions.length !== 1 || sessions[0] !== 'lyk_session=' + cookie.value) return;
      const id = await this.identity(cookie.value);
      if (id && attempt === this.attempt && revision === this.revision && attempt.owner.current(id))
        attempt.before = id;
    } catch {
      // Failed continuity evidence still allows normal sign-in through the clear barrier.
    }
  }
  async read(document: string) {
    const key = randomUUID();
    const revision = this.revision;
    this.readKey = key;
    this.receipt = undefined;
    const current = () =>
      this.readKey === key &&
      this.document() === document &&
      this.revision === revision &&
      this.ready();
    if (!current()) throw new Error('Account verification changed');
    const { user, id, expires, sessionCookie } = await this.browserIdentity();
    if (!current()) throw new Error('Account verification changed');
    this.receipt = { key, document, id, revision, expires, sessionCookie };
    return { receipt: key, user };
  }
  private async browserIdentity() {
    const url = this.origin + '/api/me';
    const cookies = await this.session.cookies.get({ url, name: 'lyk_session' });
    const res = await this.session.fetch(url, {
      credentials: 'include',
      cache: 'no-store',
      redirect: 'error',
      bypassCustomProtocolHandlers: true,
      signal: AbortSignal.timeout(15000),
    });
    const user = res.status === 401 ? null : await readDesktopAuthResponse(res);
    if (res.status === 401) await res.body?.cancel();
    const id = res.status === 401 ? null : accountID(user);
    const expires = Math.min(
      ...cookies.map((cookie) => (cookie.expirationDate ?? Infinity) * 1000),
    );
    return { user, id, expires, sessionCookie: !!this.rootCookie(cookies) };
  }
  daemonAuthorization(allowed: () => boolean) {
    const document = this.document();
    const revision = this.revision;
    const current = () =>
      !!document &&
      this.document() === document &&
      this.revision === revision &&
      this.ready() &&
      allowed();
    return async (id: string) => {
      if (!current()) throw new Error('Sign in before configuring the desktop daemon');
      const owner = await this.browserIdentity();
      const authenticated = () => current() && owner.expires > Date.now();
      if (!owner.sessionCookie || owner.id !== id || !authenticated())
        throw new Error('Desktop session identity does not match account');
      return authenticated;
    };
  }
  matches(document: string, id: unknown, key: unknown) {
    const receipt = this.receipt;
    return Boolean(
      receipt &&
      receipt.key === key &&
      receipt.document === document &&
      this.document() === document &&
      receipt.id === id &&
      receipt.revision === this.revision &&
      receipt.expires > Date.now() &&
      this.ready(),
    );
  }
  async prepare(document: string, id: string): Promise<boolean> {
    const attempt = this.attempt;
    const key = this.receipt?.key;
    if (
      !attempt ||
      !this.receipt?.sessionCookie ||
      attempt.before !== id ||
      attempt.document === document ||
      !this.matches(document, id, key)
    )
      return false;
    attempt.prepared = undefined;
    try {
      if (!(await attempt.owner.verify(id)) || !this.matches(document, id, key)) return false;
      attempt.prepared = key;
      return this.canFinish(document, id);
    } catch {
      return false;
    }
  }
  canFinish(document: string, id: string): boolean {
    const attempt = this.attempt;
    return Boolean(
      attempt?.before === id &&
      attempt.prepared &&
      this.matches(document, id, attempt.prepared) &&
      attempt.owner.current(id),
    );
  }
}
