import { createHash, randomBytes } from 'node:crypto';
import type { CookiesSetDetails } from 'electron';

export class DesktopGoogleAuth {
  private readonly abort = new AbortController();
  constructor(
    private readonly origin: string,
    private readonly ops: {
      request: typeof fetch;
      open(url: string): Promise<void>;
      set(cookie: CookiesSetDetails): Promise<void>;
      flush(): Promise<void>;
      current(): boolean;
      capture(): Promise<void>;
    },
  ) {}
  cancel() {
    this.abort.abort();
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
    await this.ops.open(this.origin + '/auth/google?desktop_ticket=' + ticket);
    const until = Date.now() + 5 * 60_000;
    while (Date.now() < until) {
      this.check();
      const response = await this.request('/auth/desktop/google/exchange', { ticket, verifier });
      if (response.status === 202) {
        await response.body?.cancel();
        await new Promise<void>((resolve, reject) => {
          const timer = setTimeout(done, 10_000);
          const abort = () => {
            clearTimeout(timer);
            reject(new Error('Sign-in interrupted'));
          };
          function done() {
            signal.removeEventListener('abort', abort);
            resolve();
          }
          const signal = this.abort.signal;
          signal.addEventListener('abort', abort, { once: true });
          if (signal.aborted) abort();
        });
        continue;
      }
      if (!response.ok) throw new Error('Google sign-in failed or expired');
      const { token, expires_in: expiresIn } = await this.data(response);
      if (
        typeof token !== 'string' ||
        !/^[A-Za-z0-9._-]{1,16384}$/.test(token) ||
        typeof expiresIn !== 'number' ||
        !Number.isInteger(expiresIn) ||
        expiresIn <= 0 ||
        expiresIn > 31_536_000
      )
        throw new Error('Invalid sign-in response');
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
    throw new Error('Google sign-in expired');
  }
}
