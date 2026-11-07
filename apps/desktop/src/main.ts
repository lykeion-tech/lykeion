import { app, BrowserWindow, dialog, ipcMain, session, shell } from 'electron';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { readFile } from 'node:fs/promises';
import os from 'node:os';
import { DaemonManager } from './daemon-manager.js';
import { installDaemonIPC, previewOrigins, validDaemonSender } from './daemon-ipc.js';
import path from 'node:path';
import { desktopConfigGuidance, loadDesktopConfig } from './runtime-config.js';
import { desktopEntryURL, installDesktopRenderer, trustedDesktopFrame } from './renderer-origin.js';
import { openGoogleSignIn } from './google-sign-in.js';

const directory = path.dirname(fileURLToPath(import.meta.url));
const developmentUrl = process.env.LYKEION_RENDERER_URL;
if (developmentUrl && !previewOrigins.has(developmentUrl))
  throw new Error('Unsupported development renderer origin');
app.setName('Lykeion Preview');
app.setPath('userData', path.join(app.getPath('appData'), 'Lykeion Preview'));
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
let quitAllowed = false;
let quitPending = false;
app.on('before-quit', (event) => {
  if (quitAllowed || !ownsInstance || !daemon) return;
  event.preventDefault();
  if (quitPending) return;
  quitPending = true;
  void daemon
    .beforeQuit()
    .catch(() => {
      console.error('Desktop daemon could not be stopped before quit');
    })
    .finally(() => {
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
      preload: path.join(directory, 'preload.cjs'),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
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
    window = null;
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
    installDesktopRenderer(session.defaultSession, apiOrigin, path.dirname(bundledPath));
  }
  daemon = new DaemonManager({
    configRoot: path.join(configBase, 'lyk'),
    apiOrigin,
    cliPath: path.join(directory, 'lyk'),
    supported:
      ownsInstance &&
      !app.isPackaged &&
      !!developmentUrl &&
      ['darwin', 'linux'].includes(process.platform),
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
  ipcMain.handle('lykeion:google-sign-in', (event) => {
    if (
      !window ||
      !(developmentUrl
        ? validDaemonSender(event, window.webContents)
        : trustedDesktopFrame(event, window.webContents, apiOrigin))
    )
      throw new Error('Invalid sender');
    openGoogleSignIn(window, apiOrigin);
  });
  createWindow();
  app.on('activate', () => {
    if (!window) createWindow();
  });
});
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
