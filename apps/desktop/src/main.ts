import { app, BrowserWindow, dialog, ipcMain, session, shell } from 'electron';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { readFile } from 'node:fs/promises';
import os from 'node:os';
import { DaemonManager, desktopDaemonOptions } from './daemon-manager.js';
import { installDaemonIPC, previewOrigins, validDaemonSender } from './daemon-ipc.js';
import path from 'node:path';
import { desktopConfigGuidance, loadDesktopConfig } from './runtime-config.js';
import {
  desktopEntryURL,
  desktopPartition,
  bindDesktopRequestLifetime,
  installDesktopRenderer,
  trustedDesktopFrame,
  validExternalURL,
} from './renderer-origin.js';
import { desktopUserData } from './desktop-user-data.js';
import { DesktopAuthGate } from './desktop-auth-gate.js';
import { clearAbandonedLogout } from './desktop-auth-logout.js';

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
const authGate = new DesktopAuthGate();
function abandonDocumentAuthentication() {
  authGate.abandonUnclaimed();
  if (authGate.acknowledgeInterruptedVerification())
    dialog.showErrorBox(
      'Sign-in interrupted',
      'Sign-in was interrupted before it was sent. Please try again.',
    );
  if (desktopSession && selectedOrigin) {
    void authGate
      .recoverAbandonedLogout(() => clearAbandonedLogout(desktopSession!, selectedOrigin))
      .catch(() => {
        console.error('Interrupted logout could not be recovered');
      });
  }
}
let quitAllowed = false;
let quitPending = false;
app.on('before-quit', (event) => {
  if (quitAllowed || !ownsInstance) return;
  event.preventDefault();
  if (quitPending) return;
  quitPending = true;
  void authGate
    .shutdown(
      async () => {
        await daemon?.beforeQuit();
      },
      15000,
      async () => {
        if (!desktopSession || !selectedOrigin) throw new Error('Logout recovery unavailable');
        await clearAbandonedLogout(desktopSession, selectedOrigin);
      },
    )
    .then(async (results) => {
      if (results[0]?.status === 'rejected') {
        quitPending = false;
        dialog.showErrorBox(
          'Lykeion could not close',
          results[0].reason instanceof Error &&
            results[0].reason.message === 'Sign-in was interrupted before it was sent'
            ? 'Sign-in was interrupted before it was sent. You can try signing in again after reopening Lykeion.'
            : 'An authentication request did not finish. Sign-in or sign-out may not have completed. Try closing Lykeion again after the request finishes.',
        );
        return;
      }
      if (results[1]?.status === 'rejected')
        console.error('Desktop daemon could not be stopped before quit');
      try {
        await desktopSession?.cookies.flushStore();
      } catch {
        quitPending = false;
        dialog.showErrorBox(
          'Lykeion could not close',
          'The sign-in session could not be safely saved. Try closing Lykeion again.',
        );
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
  if (developmentUrl) void window.loadURL(developmentUrl);
  else void window.loadURL(desktopEntryURL(selectedOrigin));
  window.on('closed', () => {
    if (window === owner) abandonDocumentAuthentication();
    drain();
    if (window === owner) window = null;
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
    desktopCancellation = installDesktopRenderer(
      desktopSession,
      apiOrigin,
      path.dirname(bundledPath),
      {
        continue: (id, token, path) => authGate.continueMutation(id, token, path),
        networkReady: () => authGate.networkReady(),
        networkBlocked: () => authGate.networkBlocked(),
        networkEpoch: () => authGate.networkEpoch(),
        finish: (id) => authGate.finishMutation(id),
      },
    );
  }
  daemon = new DaemonManager({
    configRoot: path.join(configBase, 'lyk'),
    apiOrigin,
    packaged: app.isPackaged,
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
    if (window && !window.isDestroyed()) window.webContents.send('lykeion:daemon:status', snapshot);
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
    event.returnValue =
      window && desktopCancellation && trustedDesktopFrame(event, window.webContents, apiOrigin)
        ? desktopCancellation.beginDocument()
        : null;
  });
  ipcMain.on('lykeion:reserve-auth', (event, path: unknown) => {
    event.returnValue =
      window &&
      typeof path === 'string' &&
      trustedDesktopFrame(event, window.webContents, apiOrigin)
        ? authGate.reserve(path)
        : null;
  });
  ipcMain.on('lykeion:release-auth', (event, token: unknown) => {
    if (
      window &&
      typeof token === 'string' &&
      trustedDesktopFrame(event, window.webContents, apiOrigin)
    )
      authGate.release(token);
  });
  createWindow();
  app.on('activate', () => {
    if (!window) createWindow();
  });
});
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
