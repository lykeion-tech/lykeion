import { app, BrowserWindow, dialog, ipcMain, session, shell } from 'electron';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { readFile } from 'node:fs/promises';
import os from 'node:os';
import { packagedDaemonVerifier } from './packaged-daemon.js';
import { DaemonManager, desktopDaemonOptions } from './daemon-manager.js';
import { installDaemonIPC, previewOrigins, validDaemonSender } from './daemon-ipc.js';
import path from 'node:path';
import { desktopConfigGuidance, loadDesktopConfig } from './runtime-config.js';
import {
  desktopPartition,
  bindDesktopRequestLifetime,
  installDesktopRenderer,
  trustedDesktopFrame,
  validExternalURL,
} from './renderer-origin.js';
import { desktopUserData } from './desktop-user-data.js';
import { loadDesktopWindow } from './desktop-window-recovery.js';
import { DesktopVerification } from './desktop-verification.js';
import { DesktopGoogleAuth } from './desktop-google-auth.js';
import { DesktopAuthGate } from './desktop-auth-gate.js';
import {
  clearAbandonedLogout,
  finalizeLogoutShutdown,
  recoverLogoutIntent,
  waitForLogoutRecovery,
} from './desktop-auth-logout.js';
import { LogoutIntent } from './desktop-logout-intent.js';

const directory = path.dirname(fileURLToPath(import.meta.url));
const developmentUrl = app.isPackaged ? undefined : process.env.LYKEION_RENDERER_URL;
if (developmentUrl && !previewOrigins.has(developmentUrl))
  throw new Error('Unsupported development renderer origin');
const appName = app.isPackaged ? 'Lykeion' : 'Lykeion Preview';
app.setName(appName);
app.setPath('userData', desktopUserData(app.getPath('appData'), app.isPackaged));
let window: BrowserWindow | null = null;
const ownsInstance = app.requestSingleInstanceLock();
if (!ownsInstance) app.quit();
app.on('second-instance', () => {
  if (window?.isMinimized()) window.restore();
  window?.focus();
});
const bundledPath = path.resolve(directory, '../../../dist/dashboard/index.html');
const configBase =
  process.platform === 'linux'
    ? process.env.XDG_CONFIG_HOME || path.join(os.homedir(), '.config')
    : app.getPath('appData');
let daemon: DaemonManager | null = null;
let selectedOrigin = '';
let desktopSession: Electron.Session | null = null;
let desktopCancellation: ReturnType<typeof installDesktopRenderer> | null = null;
let logoutIntent: LogoutIntent | null = null;
let verificationIntent: LogoutIntent | null = null;
let verification: DesktopVerification | null = null;
let currentDocument: string | null = null;
let googleAuth: DesktopGoogleAuth | null = null;
let googleMutationID = Number.MIN_SAFE_INTEGER;
let startupRecovery: Promise<void> = Promise.resolve();
const authGate = new DesktopAuthGate(() => {
  if (!logoutIntent) throw new Error('Logout recovery unavailable');
  logoutIntent.mark();
});
async function finishLogoutRecovery(): Promise<void> {
  authGate.beginLogoutRecovery();
  await authGate.recoverAbandonedLogout(async () => {
    if (!desktopSession || !selectedOrigin || !logoutIntent)
      throw new Error('Logout recovery unavailable');
    await clearAbandonedLogout(desktopSession, selectedOrigin);
    logoutIntent.clear();
  });
}
function abandonDocumentAuthentication() {
  googleAuth?.cancel();
  currentDocument = null;
  authGate.abandonUnclaimed();
  if (authGate.acknowledgeInterruptedVerification())
    dialog.showErrorBox(
      'Sign-in interrupted',
      'Sign-in was interrupted before it was sent. Please try again.',
    );
  if (desktopSession && selectedOrigin) {
    void authGate
      .recoverAbandonedLogout(async () => {
        await clearAbandonedLogout(desktopSession!, selectedOrigin);
        logoutIntent?.clear();
      })
      .catch(() => {
        console.error('Interrupted logout could not be recovered');
      });
  }
}
let quitAllowed = false;
let quitPending = false;
let windowRecovery: ReturnType<typeof loadDesktopWindow> | undefined;
app.on('before-quit', (event) => {
  googleAuth?.cancel();
  if (quitAllowed || !ownsInstance) return;
  event.preventDefault();
  if (quitPending) return;
  quitPending = true;
  windowRecovery?.pause();
  void authGate
    .shutdown(
      async () => {
        await daemon?.beforeQuit();
      },
      15000,
      async () => {
        if (!desktopSession || !selectedOrigin) throw new Error('Logout recovery unavailable');
        await clearAbandonedLogout(desktopSession, selectedOrigin);
        logoutIntent?.clear();
      },
    )
    .then(async (results) => {
      if (results[0]?.status === 'rejected') {
        daemon?.resumeAfterQuit();
        authGate.resumeAfterQuit();
        quitPending = false;
        dialog.showErrorBox(
          'Lykeion could not close',
          results[0].reason instanceof Error &&
            results[0].reason.message === 'Sign-in was interrupted before it was sent'
            ? 'Sign-in was interrupted before it was sent. You can try signing in again after reopening Lykeion.'
            : 'An authentication request did not finish. Sign-in or sign-out may not have completed. Try closing Lykeion again after the request finishes.',
        );
        void windowRecovery?.resume();
        return;
      }
      if (results[1]?.status === 'rejected')
        console.error('Desktop daemon could not be stopped before quit');
      try {
        await finalizeLogoutShutdown(startupRecovery, desktopSession);
      } catch {
        daemon?.resumeAfterQuit();
        authGate.resumeAfterQuit(finalizeLogoutShutdown(startupRecovery, desktopSession, Infinity));
        quitPending = false;
        dialog.showErrorBox(
          'Lykeion could not close',
          'The sign-in session could not be safely saved. Try closing Lykeion again.',
        );
        void windowRecovery?.resume();
        return;
      }
      quitAllowed = true;
      app.quit();
    });
});
function createWindow() {
  window = new BrowserWindow({
    width: 1920,
    height: 960,
    minWidth: 900,
    minHeight: 640,
    title: 'Lykeion',
    backgroundColor: '#ffffff',
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    webPreferences: {
      ...(desktopSession ? { session: desktopSession } : {}),
      preload: path.join(directory, 'preload.cjs'),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  const owner = window;
  owner.webContents.on('render-process-gone', () => {
    if (window === owner) abandonDocumentAuthentication();
  });
  owner.webContents.on('destroyed', () => {
    if (window === owner) abandonDocumentAuthentication();
  });
  const drain = desktopCancellation
    ? bindDesktopRequestLifetime(owner.webContents, desktopCancellation, () => window === owner)
    : () => {};
  window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  window.webContents.on('will-navigate', (event) => {
    event.preventDefault();
  });
  window.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) =>
    callback(false),
  );
  if (developmentUrl) void owner.loadURL(developmentUrl);
  else
    windowRecovery = loadDesktopWindow(
      owner,
      selectedOrigin,
      () =>
        window === owner &&
        !owner.isDestroyed() &&
        !owner.webContents.isDestroyed() &&
        !quitPending &&
        !quitAllowed,
      async (signal) => {
        const result = await dialog.showMessageBox(owner, {
          type: 'warning',
          message: 'The Lykeion dashboard could not stay open.',
          detail: 'Select Retry to reopen the dashboard in this window.',
          buttons: ['Retry', 'Dismiss'],
          defaultId: 0,
          cancelId: 1,
          signal,
        });
        return result.response === 0;
      },
    );
  window.on('closed', () => {
    if (window === owner) abandonDocumentAuthentication();
    drain();
    if (window === owner) {
      windowRecovery = undefined;
      window = null;
    }
  });
}
void app.whenReady().then(async () => {
  if (!ownsInstance) return;
  let apiOrigin: string;
  try {
    if (developmentUrl && !app.isPackaged) {
      apiOrigin = process.env.LYK_API_URL ?? 'http://127.0.0.1:8080';
    } else {
      apiOrigin = await loadDesktopConfig(path.join(os.homedir(), '.lyk', 'desktop.json'), () =>
        readFile(path.join(directory, 'desktop-hosted-origin.txt'), 'utf8').catch(
          (error: NodeJS.ErrnoException) => {
            if (error.code === 'ENOENT') return undefined;
            throw error;
          },
        ),
      );
    }
  } catch (error) {
    dialog.showErrorBox('Lykeion configuration error', desktopConfigGuidance(error));
    app.quit();
    return;
  }
  selectedOrigin = apiOrigin;
  if (!developmentUrl) {
    desktopSession = session.fromPartition(desktopPartition(apiOrigin));
    logoutIntent = new LogoutIntent(app.getPath('userData'), apiOrigin);
    verificationIntent = new LogoutIntent(app.getPath('userData'), apiOrigin, 'verification');
    try {
      startupRecovery = waitForLogoutRecovery(
        recoverLogoutIntent(logoutIntent, desktopSession, apiOrigin),
      );
      await startupRecovery;
    } catch {
      dialog.showErrorBox(
        'Lykeion could not start',
        'An interrupted sign-out could not be recovered. Check access to your Lykeion profile and try again.',
      );
      app.exit(1);
      return;
    }
    verification = new DesktopVerification(
      apiOrigin,
      desktopSession,
      () => currentDocument,
      () => authGate.canFinishVerification(),
    );
    desktopSession.cookies.on('changed', (_event, cookie) => {
      if (cookie.name === 'lyk_session') verification?.cookieChanged();
    });
    desktopCancellation = installDesktopRenderer(
      desktopSession,
      apiOrigin,
      path.dirname(bundledPath),
      {
        beforeVerification: (token, headers) => verification!.capture(token, headers),
        continue: (id, token, path) => authGate.continueMutation(id, token, path),
        networkReady: () => authGate.networkReady(),
        waitForIdentityRead: () => authGate.waitForDrain(),
        networkBlocked: () => authGate.networkBlocked(),
        networkEpoch: () => authGate.networkEpoch(),
        finish: (id) => authGate.finishMutation(id),
        completeLogout: finishLogoutRecovery,
        waitForLogoutTurn: (id) => authGate.waitForLogoutTurn(id),
      },
    );
  }
  let packagedCLI: (() => Promise<boolean>) | undefined;
  if (app.isPackaged) {
    let manifest: unknown;
    try {
      manifest = JSON.parse(await readFile(path.join(directory, 'desktop-build.json'), 'utf8'));
    } catch {
      // The dashboard remains usable when the packaged runtime cannot be verified.
    }
    packagedCLI = packagedDaemonVerifier(process.resourcesPath, manifest, app.getVersion());
  }
  daemon = new DaemonManager({
    configRoot: path.join(configBase, 'lyk'),
    apiOrigin,
    packaged: app.isPackaged,
    cliAvailable: packagedCLI,
    authorizeBrowser: app.isPackaged
      ? () => verification!.daemonAuthorization(() => !verificationIntent!.exists())
      : undefined,
    ...desktopDaemonOptions(
      process.platform,
      ownsInstance,
      app.isPackaged,
      directory,
      process.resourcesPath,
      developmentUrl,
    ),
  });
  installDaemonIPC(
    ipcMain,
    () => window?.webContents ?? null,
    daemon,
    pathToFileURL(bundledPath).href,
    developmentUrl ? undefined : apiOrigin,
  );
  daemon.onStatusChange((snapshot) => {
    if (
      window &&
      !window.isDestroyed() &&
      !window.webContents.isDestroyed() &&
      !window.webContents.isCrashed()
    )
      window.webContents.send('lykeion:daemon:status', snapshot);
  });
  daemon.startPolling();
  ipcMain.handle('lykeion:open-documentation', async (event) => {
    if (
      !window ||
      !(developmentUrl
        ? validDaemonSender(event, window.webContents)
        : trustedDesktopFrame(event, window.webContents, apiOrigin))
    )
      throw new Error('Invalid sender');
    await shell.openExternal('https://github.com/lykeion-ia/lykeion');
  });
  ipcMain.on('lykeion:open-external', (event, url: unknown) => {
    if (
      !window ||
      !(developmentUrl
        ? validDaemonSender(event, window.webContents)
        : trustedDesktopFrame(event, window.webContents, apiOrigin)) ||
      !validExternalURL(url)
    )
      return;
    void shell.openExternal(url);
  });
  ipcMain.on('lykeion:cancel-request', (event, id: unknown) => {
    if (window && desktopCancellation && trustedDesktopFrame(event, window.webContents, apiOrigin))
      desktopCancellation.cancel(id);
  });
  ipcMain.on('lykeion:document-start', (event) => {
    if (window && desktopCancellation && trustedDesktopFrame(event, window.webContents, apiOrigin))
      abandonDocumentAuthentication();
    if (
      !window ||
      !desktopCancellation ||
      !trustedDesktopFrame(event, window.webContents, apiOrigin)
    ) {
      event.returnValue = null;
      return;
    }
    currentDocument = desktopCancellation.beginDocument();
    let pending = true;
    try {
      pending = verificationIntent?.exists() ?? true;
    } catch {
      /* keep private reads gated */
    }
    event.returnValue = { id: currentDocument, verificationPending: pending };
  });
  ipcMain.on('lykeion:reserve-auth', (event, path: unknown) => {
    try {
      if (
        !window ||
        typeof path !== 'string' ||
        !trustedDesktopFrame(event, window.webContents, apiOrigin)
      ) {
        event.returnValue = null;
        return;
      }
      if (path === '/auth/logout') googleAuth?.cancel();
      if (path === '/auth/verify-code' && (!verificationIntent || verificationIntent.exists())) {
        event.returnValue = null;
        return;
      }
      const token = authGate.reserve(path);
      if (token && path === '/auth/verify-code') {
        try {
          verificationIntent!.mark();
          if (currentDocument && daemon)
            verification?.reserve(token, currentDocument, daemon.verificationOwner());
        } catch (error) {
          authGate.release(token);
          throw error;
        }
      }
      if (token && path === '/auth/logout') verification?.invalidate();
      event.returnValue = token;
    } catch {
      event.returnValue = null;
    }
  });
  ipcMain.handle('lykeion:google-sign-in', async (event, documentID: unknown) => {
    if (
      typeof documentID !== 'string' ||
      documentID !== currentDocument ||
      !window ||
      !trustedDesktopFrame(event, window.webContents, apiOrigin) ||
      !verificationIntent ||
      !verification ||
      !desktopSession ||
      !daemon ||
      googleAuth ||
      verificationIntent.exists()
    )
      throw new Error('Google sign-in is unavailable');
    const token = authGate.reserve('/auth/verify-code');
    if (!token) throw new Error('Application authentication is busy');
    const mutation = googleMutationID++;
    try {
      verificationIntent.mark();
      verification.reserve(token, documentID, daemon.verificationOwner());
      if (!authGate.continueMutation(mutation, token, '/auth/verify-code'))
        throw new Error('Application authentication is busy');
    } catch (error) {
      authGate.release(token);
      throw error;
    }
    const session = desktopSession;
    const proof = verification;
    const flow = new DesktopGoogleAuth(apiOrigin, {
      request: fetch,
      open: (url) => shell.openExternal(url),
      set: (cookie) => session.cookies.set(cookie),
      flush: () => session.cookies.flushStore(),
      current: () =>
        documentID === currentDocument && !quitPending && !quitAllowed && !logoutIntent?.exists(),
      capture: async () => {
        const cookies = await session.cookies.get({
          url: apiOrigin + '/api/me',
          name: 'lyk_session',
        });
        await proof.capture(
          token,
          cookies.length === 1 ? { Cookie: 'lyk_session=' + cookies[0]!.value } : {},
        );
      },
    });
    googleAuth = flow;
    try {
      await flow.run();
    } finally {
      if (googleAuth === flow) googleAuth = null;
      authGate.finishMutation(mutation);
    }
  });
  ipcMain.on('lykeion:reload-after-authentication', (event, documentID: unknown) => {
    const owner = window;
    if (
      !owner ||
      documentID !== currentDocument ||
      googleAuth ||
      quitPending ||
      quitAllowed ||
      !trustedDesktopFrame(event, owner.webContents, apiOrigin)
    ) {
      event.returnValue = false;
      return;
    }
    event.returnValue = true;
    setImmediate(() => {
      if (window === owner && documentID === currentDocument && !quitPending && !quitAllowed)
        owner.webContents.reload();
    });
  });
  ipcMain.on('lykeion:cancel-google-sign-in', (event, documentID: unknown) => {
    if (
      documentID === currentDocument &&
      window &&
      trustedDesktopFrame(event, window.webContents, apiOrigin)
    )
      googleAuth?.cancel();
  });
  ipcMain.handle('lykeion:read-verification', async (event, documentID: unknown) => {
    if (
      typeof documentID !== 'string' ||
      documentID !== currentDocument ||
      !window ||
      !trustedDesktopFrame(event, window.webContents, apiOrigin) ||
      !verificationIntent ||
      !verification
    )
      throw new Error('Invalid verification request');
    await authGate.waitForDrain();
    if (documentID !== currentDocument) throw new Error('Account verification changed');
    // A refused reservation never created a marker. Its document can still recheck the current session.
    if (!verificationIntent.exists()) verification.invalidate();
    return verification.read(documentID);
  });
  ipcMain.handle('lykeion:can-retain-verification', (event, documentID: unknown, id: unknown) => {
    if (
      typeof documentID !== 'string' ||
      typeof id !== 'string' ||
      !window ||
      !trustedDesktopFrame(event, window.webContents, apiOrigin)
    )
      return false;
    return verification?.prepare(documentID, id) ?? false;
  });
  ipcMain.on(
    'lykeion:finish-verification',
    (event, documentID: unknown, id: unknown, receipt: unknown, retaining: unknown) => {
      if (
        !currentDocument ||
        documentID !== currentDocument ||
        !window ||
        !verificationIntent ||
        !trustedDesktopFrame(event, window.webContents, apiOrigin)
      ) {
        event.returnValue = false;
        return;
      }
      try {
        if (
          !verification?.matches(currentDocument, id, receipt) ||
          (retaining && (typeof id !== 'string' || !verification.canFinish(currentDocument, id)))
        ) {
          event.returnValue = false;
          return;
        }
        verificationIntent.clear();
        verification?.invalidate();
        event.returnValue = true;
      } catch {
        event.returnValue = false;
      }
    },
  );
  ipcMain.on('lykeion:release-auth', (event, token: unknown) => {
    if (
      window &&
      typeof token === 'string' &&
      trustedDesktopFrame(event, window.webContents, apiOrigin)
    )
      if (authGate.release(token)) void finishLogoutRecovery().catch(() => {});
  });
  ipcMain.handle('lykeion:retry-logout', async (event) => {
    if (
      !window ||
      !desktopCancellation ||
      !trustedDesktopFrame(event, window.webContents, apiOrigin)
    )
      throw new Error('Invalid sender');
    if (!authGate.networkBlocked()) return false;
    await finishLogoutRecovery();
    return true;
  });
  createWindow();
  app.on('activate', () => {
    if (!window) createWindow();
  });
});
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
