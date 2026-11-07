import { contextBridge, ipcRenderer } from 'electron';
import type {
  DaemonPreferences,
  DaemonSession,
  DaemonSnapshot,
  DesktopDaemonBridge,
} from './daemon-types.js';

const startup =
  window.location.pathname === '/__lykeion_desktop__/'
    ? (ipcRenderer.sendSync('lykeion:document-start') as
        { id: string; verificationPending: boolean } | string | null)
    : null;
const documentID = typeof startup === 'string' ? startup : startup?.id;

window.addEventListener(
  'click',
  (event) => {
    if (!event.isTrusted || event.button !== 0 || event.defaultPrevented) return;
    const anchor = event.composedPath().find((item) => item instanceof HTMLAnchorElement);
    if (!(anchor instanceof HTMLAnchorElement) || !anchor.href) return;
    const target = new URL(anchor.href);
    if (
      target.origin === window.location.origin &&
      (anchor.hasAttribute('download') || target.pathname === window.location.pathname)
    )
      return;
    event.preventDefault();
    ipcRenderer.send('lykeion:open-external', target.href);
  },
  true,
);
const daemon: DesktopDaemonBridge = Object.freeze({
  getSnapshot: () => ipcRenderer.invoke('lykeion:daemon:getSnapshot') as Promise<DaemonSnapshot>,
  getPreferences: () =>
    ipcRenderer.invoke('lykeion:daemon:getPreferences') as Promise<DaemonPreferences>,
  setPreferences: (value: DaemonPreferences) =>
    ipcRenderer.invoke('lykeion:daemon:setPreferences', value) as Promise<DaemonPreferences>,
  configureSession: (value: DaemonSession) =>
    ipcRenderer.invoke('lykeion:daemon:configureSession', value) as Promise<DaemonSnapshot>,
  clearSession: () => ipcRenderer.invoke('lykeion:daemon:clearSession') as Promise<DaemonSnapshot>,
  start: () => ipcRenderer.invoke('lykeion:daemon:start') as Promise<DaemonSnapshot>,
  stop: () => ipcRenderer.invoke('lykeion:daemon:stop') as Promise<DaemonSnapshot>,
  restart: () => ipcRenderer.invoke('lykeion:daemon:restart') as Promise<DaemonSnapshot>,
  onStatusChange: (listener: (snapshot: DaemonSnapshot) => void) => {
    const receive = (_event: Electron.IpcRendererEvent, snapshot: DaemonSnapshot) =>
      listener(snapshot);
    ipcRenderer.on('lykeion:daemon:status', receive);
    return () => {
      ipcRenderer.removeListener('lykeion:daemon:status', receive);
    };
  },
});
contextBridge.exposeInMainWorld(
  'lykeionDesktop',
  Object.freeze({
    platform: process.platform,
    openDocumentation: () => ipcRenderer.invoke('lykeion:open-documentation') as Promise<void>,
    ...(documentID
      ? {
          requestDocument: documentID,
          ...(startup && typeof startup !== 'string'
            ? {
                verificationPending: startup.verificationPending,
                finishVerification: () =>
                  ipcRenderer.sendSync('lykeion:finish-verification', documentID) === true,
              }
            : {}),
          cancelRequest: (id: string) => ipcRenderer.send('lykeion:cancel-request', id),
          reserveAuth: (path: string) =>
            ipcRenderer.sendSync('lykeion:reserve-auth', path) as string | null,
          releaseAuth: (token: string) => ipcRenderer.send('lykeion:release-auth', token),
          retryLogout: () => ipcRenderer.invoke('lykeion:retry-logout') as Promise<boolean>,
        }
      : {}),
    daemon,
  }),
);
