import { workspaceId } from './session';
import { authenticationEpoch, authenticationLost } from './auth-loss';

// Let the browser stream to its download manager without buffering the file in JS.
export function downloadFile(path: string, workspace: string) {
  const query = new URLSearchParams({ download: '1', workspace });
  const anchor = document.createElement('a');
  anchor.href = `${path}?${query}`;
  anchor.download = '';
  anchor.click();
}

// A refused request: the HTTP status, the server's "error" text and the whole
// JSON body, which some refusals extend (a duplicate names the card it matched).
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body: Record<string, unknown> = {},
    readonly authEpoch = authenticationEpoch(),
  ) {
    super(message);
  }
}

export class PreviewTooLargeError extends Error {
  constructor() {
    super('File exceeds the preview size limit.');
    this.name = 'PreviewTooLargeError';
  }
}

async function boundedBlob(res: Response, limit: number): Promise<Blob> {
  const length = Number(res.headers.get('Content-Length'));
  if (length > limit) {
    await res.body?.cancel().catch(() => {});
    throw new PreviewTooLargeError();
  }
  const reader = res.body?.getReader();
  if (!reader) return new Blob([], { type: res.headers.get('Content-Type') ?? '' });
  const chunks: ArrayBuffer[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) {
        await reader.cancel().catch(() => {});
        throw new PreviewTooLargeError();
      }
      chunks.push(value.slice().buffer as ArrayBuffer);
    }
  } finally {
    reader.releaseLock();
  }
  return new Blob(chunks, { type: res.headers.get('Content-Type') ?? '' });
}

// One request to the Go server on the dashboard's own origin, so the session
// cookie rides along. JSON or form data in, JSON or bytes out; 204 gives undefined.
export async function api<T = void>(
  method: string,
  path: string,
  body?: unknown,
  raw?: { data: Blob; type: string },
  options: { workspace?: string; signal?: AbortSignal; response?: 'blob'; maxBytes?: number } = {},
): Promise<T> {
  const requestEpoch = authenticationEpoch();
  const headers: Record<string, string> = {};
  const workspace = options.workspace ?? workspaceId.value;
  if (workspace && path.startsWith('/api/')) headers['X-Workspace-ID'] = workspace;
  let payload: BodyInit | undefined;
  if (raw) {
    payload = raw.data;
    headers['Content-Type'] = raw.type;
  } else if (body instanceof FormData) {
    payload = body;
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  const cancel = typeof window !== 'undefined' ? window.lykeionDesktop?.cancelRequest : undefined;
  const documentID =
    typeof window !== 'undefined' ? window.lykeionDesktop?.requestDocument : undefined;
  const requestID =
    cancel && documentID && options.signal ? `${documentID}.${crypto.randomUUID()}` : undefined;
  if (requestID) headers['X-Lykeion-Desktop-Request'] = requestID;
  const desktop = typeof window !== 'undefined' ? window.lykeionDesktop : undefined;
  const needsAuthReservation =
    method === 'POST' &&
    ['/auth/verify-code', '/auth/logout'].includes(path) &&
    desktop?.reserveAuth;
  const authReservation = needsAuthReservation ? desktop.reserveAuth!(path) : undefined;
  if (needsAuthReservation && !authReservation) {
    if (path === '/auth/logout' && (await desktop?.retryLogout?.()))
      return { localOnly: true } as T;
    throw new Error('Application is shutting down or authentication is busy');
  }
  if (authReservation) headers['X-Lykeion-Desktop-Auth-Reservation'] = authReservation;
  const abort = () => {
    if (requestID) cancel?.(requestID);
  };
  options.signal?.addEventListener('abort', abort, { once: true });
  try {
    const res = await fetch(path, {
      method,
      headers,
      body: payload,
      credentials: 'same-origin',
      signal: options.signal,
    });
    if (res.status === 401) authenticationLost(requestEpoch);
    if (res.status === 204)
      return (
        path === '/auth/logout' && res.headers.get('X-Lykeion-Local-Logout') === '1'
          ? { localOnly: true }
          : undefined
      ) as T;
    if (res.ok && options.response === 'blob')
      return (await (options.maxBytes === undefined
        ? res.blob()
        : boundedBlob(res, options.maxBytes))) as T;
    const text = await res.text();
    let data: unknown;
    try {
      data = text ? JSON.parse(text) : undefined;
    } catch {
      data = undefined;
    }
    if (!res.ok) {
      const obj = data && typeof data === 'object' ? (data as Record<string, unknown>) : {};
      const message =
        typeof obj.error === 'string'
          ? obj.error
          : res.statusText || `Request failed (${res.status})`;
      throw new ApiError(res.status, message, obj, requestEpoch);
    }
    return data as T;
  } finally {
    options.signal?.removeEventListener('abort', abort);
    if (authReservation) desktop?.releaseAuth?.(authReservation);
  }
}
