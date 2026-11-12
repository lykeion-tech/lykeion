import { createHash, randomBytes } from 'node:crypto';
import type { CookiesSetDetails } from 'electron';

type CodeAttempt = { code: string; settle(valid: boolean): void };

export class DesktopGoogleAuth {
  private readonly abort = new AbortController();
  private codeReceiver: ((attempt: CodeAttempt) => void) | null = null;
  constructor(
    private readonly origin: string,
    private readonly ops: {
      request: typeof fetch;
      open(url: string): Promise<void>;
      confirm(email: string, signal: AbortSignal): Promise<boolean>;
      set(cookie: CookiesSetDetails): Promise<void>;
      flush(): Promise<void>;
      current(): boolean;
      capture(): Promise<void>;
    },
  ) {}
  cancel() {
    this.abort.abort();
  }
  get canceled(): boolean {
    return this.abort.signal.aborted;
  }
  async submitCode(code: unknown): Promise<boolean> {
    if (
      typeof code !== 'string' ||
      !/^[A-Za-z0-9_-]{43}$/.test(code) ||
      Buffer.from(code, 'base64url').toString('base64url') !== code ||
      !this.codeReceiver ||
      this.abort.signal.aborted ||
      !this.ops.current()
    )
      return false;
    const receiver = this.codeReceiver;
    return new Promise((settle) => receiver({ code, settle }));
  }
  private waitForCode(deadline: number): Promise<CodeAttempt> {
    return new Promise((resolve, reject) => {
      const signal = this.abort.signal;
      const finish = (attempt?: CodeAttempt) => {
        clearTimeout(timer);
        signal.removeEventListener('abort', abort);
        this.codeReceiver = null;
        if (attempt) resolve(attempt);
        else reject(new Error('Google sign-in interrupted or expired'));
      };
      const abort = () => finish();
      const timer = setTimeout(abort, Math.max(0, deadline - Date.now()));
      this.codeReceiver = finish;
      signal.addEventListener('abort', abort, { once: true });
      if (signal.aborted) abort();
    });
  }
  private check() {
    if (this.abort.signal.aborted || !this.ops.current()) throw new Error('Sign-in interrupted');
  }
  private async request(path: string, body: unknown) {
    this.check();
    const response = await this.ops.request(this.origin + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      redirect: 'error',
      cache: 'no-store',
      signal: AbortSignal.any([this.abort.signal, AbortSignal.timeout(15_000)]),
    });
    this.check();
    return response;
  }
  private async openBrowser(url: string): Promise<void> {
    const signal = AbortSignal.any([this.abort.signal, AbortSignal.timeout(15_000)]);
    await new Promise<void>((resolve, reject) => {
      const aborted = () => reject(new Error('Sign-in interrupted'));
      signal.addEventListener('abort', aborted, { once: true });
      if (signal.aborted) return aborted();
      void this.ops
        .open(url)
        .then(resolve, reject)
        .finally(() => {
          signal.removeEventListener('abort', aborted);
        });
    });
    this.check();
  }
  private async data(response: Response): Promise<Record<string, unknown>> {
    if (Number(response.headers.get('content-length')) > 65536)
      throw new Error('Invalid sign-in response');
    const reader = response.body?.getReader();
    if (!reader) throw new Error('Invalid sign-in response');
    const chunks: Uint8Array[] = [];
    let size = 0;
    try {
      for (;;) {
        const item = await reader.read();
        if (item.done) break;
        size += item.value.length;
        if (size > 65536) throw new Error('Invalid sign-in response');
        chunks.push(item.value);
      }
    } finally {
      reader.releaseLock();
    }
    const decoded: unknown = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    if (!decoded || typeof decoded !== 'object' || Array.isArray(decoded))
      throw new Error('Invalid sign-in response');
    return decoded as Record<string, unknown>;
  }
  async run(): Promise<void> {
    const deadline = Date.now() + 5 * 60_000;
    const verifier = randomBytes(32).toString('base64url');
    const challenge = createHash('sha256').update(verifier).digest('base64url');
    await this.ops.capture();
    this.check();
    const initial = await this.request('/auth/desktop/google/start', { challenge });
    if (!initial.ok) throw new Error('Google sign-in is unavailable');
    const { ticket } = await this.data(initial);
    if (typeof ticket !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(ticket))
      throw new Error('Invalid sign-in response');
    this.check();
    await this.openBrowser(this.origin + '/auth/google?desktop_ticket=' + ticket);
    let verified: Record<string, unknown> | undefined;
    for (let attempts = 0; attempts < 3; attempts++) {
      const attempt = await this.waitForCode(deadline);
      try {
        this.check();
        const response = await this.request('/auth/desktop/google/exchange', {
          ticket,
          verifier,
          code: attempt.code,
        });
        if (response.status === 400) {
          await response.body?.cancel();
          attempt.settle(false);
          continue;
        }
        if (!response.ok) throw new Error('Google sign-in failed or expired');
        verified = await this.data(response);
        attempt.settle(true);
        break;
      } catch (error) {
        attempt.settle(false);
        throw error;
      }
    }
    if (!verified) throw new Error('Google sign-in failed or expired');
    const { token, expires_in: expiresIn, email } = verified;
    if (
      typeof token !== 'string' ||
      !/^[A-Za-z0-9._-]{1,16384}$/.test(token) ||
      typeof expiresIn !== 'number' ||
      !Number.isInteger(expiresIn) ||
      expiresIn <= 0 ||
      expiresIn > 31_536_000 ||
      typeof email !== 'string' ||
      email.length > 254 ||
      !email.includes('@') ||
      email.startsWith('@') ||
      email.endsWith('@') ||
      email.indexOf('@') !== email.lastIndexOf('@') ||
      [...email].some((character) => {
        const point = character.codePointAt(0)!;
        return (
          point <= 0x20 ||
          (point >= 0x7f && point <= 0x9f) ||
          point === 0x061c ||
          (point >= 0x200b && point <= 0x200f) ||
          (point >= 0x2028 && point <= 0x202e) ||
          (point >= 0x2060 && point <= 0x206f) ||
          point === 0xfeff
        );
      })
    )
      throw new Error('Invalid sign-in response');
    this.check();
    const confirmationSignal = AbortSignal.any([this.abort.signal, AbortSignal.timeout(300_000)]);
    const confirmed = await new Promise<boolean>((resolve, reject) => {
      const aborted = () => reject(new Error('Sign-in interrupted'));
      confirmationSignal.addEventListener('abort', aborted, { once: true });
      if (confirmationSignal.aborted) return aborted();
      void this.ops
        .confirm(email, confirmationSignal)
        .then(resolve, reject)
        .finally(() => {
          confirmationSignal.removeEventListener('abort', aborted);
        });
    });
    if (!confirmed) {
      this.cancel();
      throw new Error('Sign-in canceled');
    }
    this.check();
    // Once started, the cookie write and flush must drain even if sign-in is cancelled.
    await this.ops.set({
      url: this.origin,
      name: 'lyk_session',
      value: token,
      path: '/',
      httpOnly: true,
      secure: this.origin.startsWith('https://'),
      sameSite: 'lax',
      expirationDate: Date.now() / 1000 + expiresIn,
    });
    await this.ops.flush();
    this.check();
    return;
  }
}
