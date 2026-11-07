import type { Session } from 'electron';
import type { LogoutIntent } from './desktop-logout-intent.js';

// Match the server's host-only, root-path logout cookie without touching
// unrelated domain cookies or an ambiguous same-name cookie.
export async function clearAbandonedLogout(session: Session, origin: string): Promise<void> {
  const current = await session.cookies.get({ name: 'lyk_session' });
  const hostname = new URL(origin).hostname.toLowerCase();
  if (
    current.length > 1 ||
    current.some(
      (cookie) =>
        !cookie.hostOnly || cookie.path !== '/' || cookie.domain?.toLowerCase() !== hostname,
    )
  )
    throw new Error('Ambiguous session cookie');
  if (current.length) await session.cookies.remove(origin, 'lyk_session');
  const remaining = await session.cookies.get({ name: 'lyk_session' });
  if (remaining.length) throw new Error('Session cookie could not be cleared');
  await session.cookies.flushStore();
}

export async function recoverLogoutIntent(
  intent: LogoutIntent,
  session: Session,
  origin: string,
): Promise<void> {
  if (!intent.exists()) return;
  await clearAbandonedLogout(session, origin);
  intent.clear();
}
