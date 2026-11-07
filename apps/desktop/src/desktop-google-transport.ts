import { randomUUID } from 'node:crypto';
import { session, type Cookie, type CookiesSetDetails, type Session } from 'electron';

function details(origin: string, cookie: Cookie): CookiesSetDetails {
  return {
    url: origin,
    name: cookie.name,
    value: cookie.value,
    path: '/',
    httpOnly: true,
    secure: cookie.secure,
    sameSite: cookie.sameSite,
    ...(cookie.expirationDate ? { expirationDate: cookie.expirationDate } : {}),
  };
}
export async function prepareGoogleExchange(
  origin: string,
  target: Session,
  code: string,
  verifier: string,
  signal: AbortSignal,
  markRecovery: () => Promise<void>,
) {
  const temporary = session.fromPartition(`desktop-google-${randomUUID()}`);
  try {
    const response = await temporary.fetch(`${origin}/auth/desktop/exchange`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code, verifier }),
      credentials: 'include',
      redirect: 'error',
      signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
    });
    if (response.status !== 204) throw new Error('Desktop sign-in exchange failed');
    const cookies = await temporary.cookies.get({ url: origin, name: 'lyk_session' });
    const value = cookies.find(
      (c) =>
        c.hostOnly &&
        c.path === '/' &&
        c.httpOnly &&
        (new URL(origin).protocol !== 'https:' || c.secure),
    );
    if (!value) throw new Error('Desktop sign-in session missing');
    let previous: Cookie | undefined;
    let attempted = false;
    return {
      apply: async () => {
        const current = await target.cookies.get({ url: origin, name: 'lyk_session' });
        if (current.some((c) => !c.hostOnly || c.path !== '/'))
          throw new Error('Ambiguous desktop session');
        previous = current[0];
        if (signal.aborted) throw new Error('Sign-in canceled');
        await markRecovery();
        if (signal.aborted) throw new Error('Sign-in canceled');
        attempted = true;
        await target.cookies.set(details(origin, value));
      },
      rollback: async () => {
        if (!attempted) return;
        if (previous) await target.cookies.set(details(origin, previous));
        else await target.cookies.remove(origin, 'lyk_session');
        const restored = await target.cookies.get({ url: origin, name: 'lyk_session' });
        if (
          previous
            ? restored.length !== 1 ||
              restored[0]?.value !== previous.value ||
              !restored[0].hostOnly ||
              restored[0].path !== '/'
            : restored.length !== 0
        )
          throw new Error('Authentication session restore could not be verified');
      },
    };
  } finally {
    await temporary.clearStorageData();
  }
}
