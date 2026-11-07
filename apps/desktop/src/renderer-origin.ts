import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { Readable } from 'node:stream';
import { createHash } from 'node:crypto';
import { net } from 'electron';
import type { Session } from 'electron';

const prefix = '/__lykeion_desktop__/';
const executableRequests = new Set(['mainFrame', 'subFrame', 'script', 'stylesheet', 'object']);

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

export function installDesktopRenderer(session: Session, origin: string, root: string): void {
  session.webRequest.onBeforeRequest(
    { urls: ['http://*/*', 'https://*/*', 'ws://*/*', 'wss://*/*'] },
    (request, done) => {
      const asset = desktopAssetPath(request.url, origin, root);
      const target = new URL(request.url);
      const selected = new URL(origin);
      if (target.protocol === 'ws:' || target.protocol === 'wss:')
        target.protocol = target.protocol === 'wss:' ? 'https:' : 'http:';
      done({
        cancel:
          target.origin !== selected.origin ||
          (executableRequests.has(request.resourceType) && !asset),
      });
    },
  );
  session.protocol.handle(new URL(origin).protocol.slice(0, -1), (request) => {
    const asset = desktopAssetPath(request.url, origin, root);
    if (asset === false || (asset && request.method !== 'GET' && request.method !== 'HEAD'))
      return new Response('Not found', { status: 404 });
    if (asset) return session.fetch(pathToFileURL(asset).href);
    if (new URL(request.url).origin !== origin) return new Response('Forbidden', { status: 403 });
    return forward(session, request);
  });
}

function forward(session: Session, incoming: Request): Promise<Response> {
  return new Promise((resolve, reject) => {
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
        !['host', 'cookie', 'content-length', 'connection', 'transfer-encoding'].includes(
          key.toLowerCase(),
        )
      )
        request.setHeader(key, value);
    }
    request.on('redirect', (status, _method, url, headers) => {
      const result = new Headers();
      for (const [key, values] of Object.entries(headers))
        for (const value of values) result.append(key, value);
      result.set('location', url);
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
      resolve(new Response(body, { status, headers }));
    });
    request.on('error', reject);
    incoming.signal.addEventListener('abort', () => request.abort(), { once: true });
    if (incoming.body) {
      const stream = Readable.fromWeb(incoming.body as import('node:stream/web').ReadableStream);
      stream.on('error', reject);
      stream.pipe(request as unknown as NodeJS.WritableStream);
    } else request.end();
  });
}
