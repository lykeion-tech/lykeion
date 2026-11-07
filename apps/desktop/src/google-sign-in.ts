import { BrowserWindow, dialog, session } from 'electron';
import { randomUUID } from 'node:crypto';

let googleWindow: BrowserWindow | null = null;
export function openGoogleSignIn(parent: BrowserWindow, origin: string): void {
  if (googleWindow && !googleWindow.isDestroyed()) {
    googleWindow.focus();
    return;
  }
  const authSession = session.fromPartition(`google-${randomUUID()}`);
  authSession.setPermissionRequestHandler((_contents, _permission, done) => done(false));
  authSession.setPermissionCheckHandler(() => false);
  const auth = new BrowserWindow({
    parent,
    width: 560,
    height: 760,
    webPreferences: {
      session: authSession,
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  googleWindow = auth;
  auth.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  const allowNavigation = (url: string) => {
    try {
      const target = new URL(url);
      return target.protocol === 'https:' || target.origin === origin;
    } catch {
      return false;
    }
  };
  auth.webContents.on('will-navigate', (event, url) => {
    if (!allowNavigation(url)) event.preventDefault();
  });
  auth.webContents.on('will-redirect', (event, url) => {
    if (!allowNavigation(url)) event.preventDefault();
  });
  let callback = false;
  auth.webContents.on('did-start-navigation', (_event, url, _inPlace, mainFrame) => {
    if (!mainFrame) return;
    const target = new URL(url);
    if (target.origin === origin && target.pathname === '/auth/google/callback') callback = true;
  });
  auth.webContents.on('did-redirect-navigation', (_event, url, _inPlace, mainFrame) => {
    if (!mainFrame) return;
    const target = new URL(url);
    if (target.origin === origin && target.pathname === '/auth/google/callback') callback = true;
  });
  auth.webContents.on('did-navigate', (_event, url) => {
    const target = new URL(url);
    if (callback && target.origin === origin && target.pathname === '/') {
      callback = false;
      void (async () => {
        const cookies = await authSession.cookies.get({ url: origin, name: 'lyk_session' });
        const cookie = cookies.find(
          (c) =>
            c.hostOnly &&
            c.path === '/' &&
            c.httpOnly &&
            (new URL(origin).protocol !== 'https:' || c.secure),
        );
        if (!cookie) throw new Error('Missing session cookie');
        await parent.webContents.session.cookies.set({
          url: origin,
          name: cookie.name,
          value: cookie.value,
          path: '/',
          httpOnly: true,
          secure: cookie.secure,
          sameSite: cookie.sameSite,
          ...(cookie.expirationDate ? { expirationDate: cookie.expirationDate } : {}),
        });
        auth.close();
        if (!parent.isDestroyed()) parent.webContents.reload();
      })().catch(() =>
        dialog.showErrorBox(
          'Lykeion sign-in',
          'Google sign-in could not be completed. Please try again.',
        ),
      );
    }
  });
  auth.on('closed', () => {
    googleWindow = null;
    void authSession.clearStorageData();
  });
  void auth.loadURL(`${origin}/auth/google`);
}
