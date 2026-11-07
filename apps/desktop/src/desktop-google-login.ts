import { createHash, randomBytes, timingSafeEqual } from 'node:crypto';
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import type { DesktopAuthGate } from './desktop-auth-gate.js';

type Exchange = { apply(): Promise<void>; rollback(): Promise<void> };
type Transport = {
  open(url: string): Promise<void>;
  exchange(code: string, verifier: string, signal: AbortSignal): Promise<Exchange>;
};
const codePattern = /^[A-Za-z0-9_-]{43}$/;
const script = `const p=new URLSearchParams(location.hash.slice(1));history.replaceState(null,'','/callback');fetch('/callback',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({state:p.get('state'),code:p.get('code')})}).then(r=>{document.body.textContent=r.ok?'Signed in. You can close this tab.':'Sign-in failed. Return to Lykeion and try again.'}).catch(()=>{document.body.textContent='Sign-in failed. Return to Lykeion and try again.'});`;
const scriptHash = createHash('sha256').update(script).digest('base64');
const page = `<!doctype html><html><head><meta charset="utf-8"><title>Lykeion sign-in</title></head><body>Completing sign-in…<script>${script}</script></body></html>`;
function randomToken(): string {
  return randomBytes(32).toString('base64url');
}
function validToken(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    codePattern.test(value) &&
    Buffer.from(value, 'base64url').toString('base64url') === value
  );
}
function equal(a: string, b: string): boolean {
  const left = Buffer.from(a),
    right = Buffer.from(b);
  return left.length === right.length && timingSafeEqual(left, right);
}
function finishResponse(res: ServerResponse, status: number, text: string): void {
  res.writeHead(status, {
    'Content-Type': 'text/plain; charset=utf-8',
    'Cache-Control': 'no-store',
    'Referrer-Policy': 'no-referrer',
    'X-Content-Type-Options': 'nosniff',
  });
  res.end(text);
}
async function body(req: IncomingMessage): Promise<unknown> {
  let size = 0;
  const chunks: Buffer[] = [];
  for await (const chunk of req) {
    const part = Buffer.from(chunk);
    size += part.length;
    if (size > 1024) throw new Error('invalid callback');
    chunks.push(part);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}
export class DesktopGoogleLogin {
  private current: AbortController | null = null;
  constructor(
    private readonly origin: string,
    private readonly gate: DesktopAuthGate,
    private readonly transport: Transport,
  ) {}
  cancel(): void {
    this.gate.cancel();
    this.current?.abort();
    this.current = null;
  }
  async start(): Promise<void> {
    this.cancel();
    const generation = this.gate.begin();
    const controller = new AbortController();
    this.current = controller;
    const nonce = randomToken(),
      verifier = randomToken(),
      challenge = createHash('sha256').update(verifier).digest('base64url');
    let port = 0,
      busy = false,
      settled = false;
    let resolve!: () => void, reject!: (error: Error) => void;
    const result = new Promise<void>((yes, no) => {
      resolve = yes;
      reject = no;
    });
    void result.catch(() => {});
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      if (error) reject(error);
      else resolve();
    };
    const server = createServer(async (req, res) => {
      const origin = `http://127.0.0.1:${port}`;
      if (
        req.socket.remoteAddress !== '127.0.0.1' ||
        req.headers.host !== `127.0.0.1:${port}` ||
        req.url !== '/callback'
      )
        return finishResponse(res, 400, 'Invalid request');
      if (req.method === 'GET') {
        res.writeHead(200, {
          'Content-Type': 'text/html; charset=utf-8',
          'Cache-Control': 'no-store',
          'Referrer-Policy': 'no-referrer',
          'X-Content-Type-Options': 'nosniff',
          'Content-Security-Policy': `default-src 'none'; script-src 'sha256-${scriptHash}'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'`,
        });
        res.end(page);
        return;
      }
      if (
        req.method !== 'POST' ||
        req.headers.origin !== origin ||
        req.headers['content-type'] !== 'application/json'
      )
        return finishResponse(res, 403, 'Invalid request');
      let input: unknown;
      try {
        input = await body(req);
      } catch {
        return finishResponse(res, 400, 'Invalid request');
      }
      if (!input || typeof input !== 'object' || Array.isArray(input))
        return finishResponse(res, 400, 'Invalid request');
      const values = input as Record<string, unknown>;
      if (
        Object.keys(values).length !== 2 ||
        !validToken(values.state) ||
        !equal(values.state, nonce) ||
        !validToken(values.code)
      )
        return finishResponse(res, 400, 'Invalid request');
      if (busy || settled) return finishResponse(res, 409, 'Sign-in already in progress');
      busy = true;
      try {
        const transaction = await this.transport.exchange(values.code, verifier, controller.signal);
        if (controller.signal.aborted) throw new Error('Sign-in canceled');
        const committed = await this.gate.commit(
          generation,
          transaction.apply,
          transaction.rollback,
        );
        if (!committed || controller.signal.aborted) {
          finishResponse(res, 409, 'Sign-in canceled');
          finish(new Error('Sign-in canceled'));
          return;
        }
        finishResponse(res, 200, 'Signed in');
        finish();
      } catch {
        finishResponse(res, 400, 'Sign-in failed');
        if (controller.signal.aborted) finish(new Error('Sign-in canceled'));
        else busy = false;
      }
    });
    server.headersTimeout = 5000;
    server.requestTimeout = 5000;
    try {
      await new Promise<void>((yes, no) => {
        server.once('error', no);
        server.listen(0, '127.0.0.1', () => yes());
      });
      const address = server.address();
      if (!address || typeof address === 'string') throw new Error('Loopback listener unavailable');
      port = address.port;
      const url = new URL('/auth/google', this.origin);
      url.search = new URLSearchParams({
        desktop_port: String(port),
        desktop_state: nonce,
        desktop_challenge: challenge,
      }).toString();
      controller.signal.addEventListener('abort', () => finish(new Error('Sign-in canceled')), {
        once: true,
      });
      if (controller.signal.aborted) {
        finish(new Error('Sign-in canceled'));
        await result;
      }
      const timer = setTimeout(() => controller.abort(), 5 * 60 * 1000);
      timer.unref();
      try {
        await this.transport.open(url.href);
        await result;
      } finally {
        clearTimeout(timer);
      }
    } finally {
      server.close();
      const timer = setTimeout(() => server.closeAllConnections(), 1000);
      timer.unref();
      if (this.current === controller) this.current = null;
    }
  }
}
