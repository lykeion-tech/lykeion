import type { Session } from 'electron';

// Match the server's host-only, root-path logout cookie without touching
// unrelated domain cookies or an ambiguous same-name cookie.
export async function clearAbandonedLogout(session: Session, origin: string): Promise<void> {
  const current = await session.cookies.get({ url: origin, name: 'lyk_session' });
  const hostOnly = current.filter((cookie) => cookie.hostOnly && cookie.path === '/');
  if (hostOnly.length > 1 || (hostOnly.length && current.length !== 1))
    throw new Error('Ambiguous session cookie');
  if (hostOnly.length) await session.cookies.remove(origin, 'lyk_session');
  const remaining = await session.cookies.get({ url: origin, name: 'lyk_session' });
  if (remaining.some((cookie) => cookie.hostOnly && cookie.path === '/'))
    throw new Error('Session cookie could not be cleared');
  await session.cookies.flushStore();
}
