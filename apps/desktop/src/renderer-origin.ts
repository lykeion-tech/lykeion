import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { Readable } from 'node:stream';
import { createHash, randomUUID } from 'node:crypto';
import { net } from 'electron';
import type { Session, WebContents } from 'electron';

const prefix = '/__lykeion_desktop__/';
const executableRequests = new Set(['mainFrame', 'subFrame', 'script', 'stylesheet', 'object']);
const requestHeader = 'x-lykeion-desktop-request';
const uuid = '[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}';
const requestID = new RegExp(`^(${uuid})\\.${uuid}$`);

export function createRequestCancellation() {
  const active = new Map<string, () => void>();
  const early = new Map<string, number>();
  let document: string | null = null;
  return {
    beginDocument() {
      this.cancelAll();
      document = randomUUID();
      return document;
    },
    cancel(id: unknown) {
      if (typeof id !== 'string' || requestID.exec(id)?.[1] !== document) return;
      const abort = active.get(id);
      if (abort) return abort();
      const now = Date.now();
      for (const [key, expires] of early) if (expires <= now) early.delete(key);
      if (early.size >= 256) early.delete(early.keys().next().value!);
      early.set(id, now + 30_000);
    },
    register(id: string, abort: () => void): boolean {
      if (requestID.exec(id)?.[1] !== document || active.has(id)) return false;
      const expires = early.get(id);
      early.delete(id);
      if (expires && expires > Date.now()) return false;
      active.set(id, abort);
      return true;
    },
    release(id: string, abort: () => void) {
      if (active.get(id) === abort) active.delete(id);
    },
    cancelAll() {
      document = null;
      const callbacks = [...active.values()];
      active.clear();
      for (const callback of callbacks) callback();
    },
  };
}
type RequestCancellation = ReturnType<typeof createRequestCancellation>;

export function bindDesktopRequestLifetime(
  contents: WebContents,
  cancellation: RequestCancellation,
  isCurrent: () => boolean,
): () => void {
  const drain = () => {
    if (isCurrent()) cancellation.cancelAll();
  };
  contents.on('render-process-gone', drain);
  contents.on('destroyed', drain);
  return drain;
}

export function desktopPartition(origin: string): string {
  return `persist:lykeion-${createHash('sha256').update(origin).digest('hex')}`;
}

export function validExternalURL(value: unknown): value is string {
  if (
    typeof value !== 'string' ||
    [...value].some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127) ||
    /%(?:0[0-9a-f]|1[0-9a-f]|7f)/i.test(value)
  )
    return false;
  try {
    const url = new URL(value);
    if (url.protocol === 'mailto:') return Boolean(url.pathname);
    return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function desktopEntryURL(origin: string): string {
  return `${origin}${prefix}`;
}

// null means network; false means a denied bundled asset request.
export function desktopAssetPath(url: string, origin: string, root: string): string | null | false {
  const parsed = new URL(url);
  if (parsed.origin !== origin || !parsed.pathname.startsWith(prefix)) return null;
  const relative = parsed.pathname.slice(prefix.length);
  if (!relative) return path.join(root, 'index.html');
  if (
    !/^[A-Za-z0-9._/-]+$/.test(relative) ||
    relative.split('/').some((p) => !p || p === '.' || p === '..') ||
    /\.html?$/i.test(relative)
  )
    return false;
  return path.join(root, relative);
}

export function trustedDesktopFrame(
  event: { sender: unknown; senderFrame: unknown },
  current: { mainFrame: { url: string } } | null,
  origin: string,
): boolean {
  if (!current || event.sender !== current || event.senderFrame !== current.mainFrame) return false;
  try {
    const url = new URL(current.mainFrame.url);
    return url.origin === origin && url.pathname === prefix && !url.search;
  } catch {
    return false;
  }
}

export function installDesktopRenderer(
  session: Session,
  origin: string,
  root: string,
  auth?: { start(id: number): Promise<void>; finish(id: number): void },
): RequestCancellation {
  const cancellation = createRequestCancellation();
  session.webRequest.onBeforeRequest(
    { urls: ['http://*/*', 'https://*/*', 'ws://*/*', 'wss://*/*'] },
    (request, done) => {
      const asset = desktopAssetPath(request.url, origin, root);
      const target = new URL(request.url);
      const selected = new URL(origin);
      if (target.protocol === 'ws:' || target.protocol === 'wss:')
        target.protocol = target.protocol === 'wss:' ? 'https:' : 'http:';
      const denied =
        target.origin !== selected.origin ||
        (executableRequests.has(request.resourceType) && !asset);
      if (
        !denied &&
        auth &&
        request.resourceType === 'xhr' &&
        request.method === 'POST' &&
        ['/auth/verify-code', '/auth/logout'].includes(target.pathname)
      ) {
        void auth.start(request.id).then(
          () => done({ cancel: false }),
          () => done({ cancel: true }),
        );
      } else done({ cancel: denied });
    },
  );
  if (auth) {
    session.webRequest.onCompleted((request) => auth.finish(request.id));
    session.webRequest.onErrorOccurred((request) => auth.finish(request.id));
  }
  session.protocol.handle(new URL(origin).protocol.slice(0, -1), (request) => {
    const asset = desktopAssetPath(request.url, origin, root);
    if (asset === false || (asset && request.method !== 'GET' && request.method !== 'HEAD'))
      return new Response('Not found', { status: 404 });
    if (asset) return session.fetch(pathToFileURL(asset).href);
    if (new URL(request.url).origin !== origin) return new Response('Forbidden', { status: 403 });
    return forward(session, request, cancellation);
  });
  return cancellation;
}

export function forward(
  session: Session,
  incoming: Request,
  cancellation: RequestCancellation,
): Promise<Response> {
  return new Promise((resolve, reject) => {
    if (incoming.signal.aborted) return reject(new DOMException('Request aborted', 'AbortError'));
    const request = net.request({
      url: incoming.url,
      method: incoming.method,
      session,
      bypassCustomProtocolHandlers: true,
      useSessionCookies: incoming.credentials !== 'omit',
      redirect: 'manual',
    });
    for (const [key, value] of incoming.headers) {
      if (
        ![
          'host',
          'cookie',
          'content-length',
          'connection',
          'transfer-encoding',
          requestHeader,
        ].includes(key.toLowerCase())
      )
        request.setHeader(key, value);
    }
    let stream: Readable | undefined;
    const id = incoming.headers.get(requestHeader);
    const release = () => {
      if (id) cancellation.release(id, abort);
    };
    const abort = () => {
      release();
      stream?.destroy();
      request.abort();
      reject(new DOMException('Request aborted', 'AbortError'));
    };
    if (id && !cancellation.register(id, abort)) {
      request.abort();
      return reject(new DOMException('Request aborted', 'AbortError'));
    }
    request.on('redirect', (status, _method, url, headers) => {
      const result = new Headers();
      for (const [key, values] of Object.entries(headers))
        for (const value of values) result.append(key, value);
      result.set('location', url);
      release();
      resolve(new Response(null, { status, headers: result }));
    });
    request.on('response', (response) => {
      const headers = new Headers();
      for (const [key, values] of Object.entries(response.headers))
        for (const value of Array.isArray(values) ? values : [values])
          if (value !== undefined) headers.append(key, String(value));
      const status = response.statusCode;
      const body =
        incoming.method === 'HEAD' || [204, 205, 304].includes(status)
          ? null
          : (Readable.toWeb(response as unknown as Readable) as ReadableStream<Uint8Array>);
      if (body) {
        response.on('end', release);
        (response as unknown as Readable).on('close', release);
        response.on('error', release);
      } else release();
      resolve(new Response(body, { status, headers }));
    });
    request.on('error', (error) => {
      release();
      stream?.destroy();
      reject(error);
    });
    incoming.signal.addEventListener('abort', abort, { once: true });
    if (incoming.body) {
      stream = Readable.fromWeb(incoming.body as import('node:stream/web').ReadableStream);
      stream.on('data', (chunk: Uint8Array) => {
        if (chunk.length && !request.chunkedEncoding) request.chunkedEncoding = true;
      });
      stream.on('error', (error) => {
        release();
        request.abort();
        reject(error);
      });
      stream.pipe(request as unknown as NodeJS.WritableStream);
    } else request.end();
  });
}
