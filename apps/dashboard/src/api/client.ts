import { workspaceId } from './session';

// A refused request: the HTTP status, the server's "error" text and the whole
// JSON body, which some refusals extend (a duplicate names the card it matched).
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body: Record<string, unknown> = {},
  ) {
    super(message);
  }
}

// One request to the Go server on the dashboard's own origin, so the session
// cookie rides along. JSON in, JSON out; 204 gives undefined.
export async function api<T = void>(
  method: string,
  path: string,
  body?: unknown,
  raw?: { data: Blob; type: string },
): Promise<T> {
  const headers: Record<string, string> = {};
  if (workspaceId.value && path.startsWith('/api/')) headers['X-Workspace-ID'] = workspaceId.value;
  let payload: BodyInit | undefined;
  if (raw) {
    payload = raw.data;
    headers['Content-Type'] = raw.type;
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(path, { method, headers, body: payload, credentials: 'same-origin' });
  if (res.status === 204) return undefined as T;
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
    throw new ApiError(res.status, message, obj);
  }
  return data as T;
}
