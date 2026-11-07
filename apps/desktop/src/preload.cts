import { contextBridge, ipcRenderer } from 'electron';
import type {
  DaemonPreferences,
  DaemonSession,
  DaemonSnapshot,
  DesktopDaemonBridge,
} from './daemon-types.js';
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
    openGoogleSignIn: () => ipcRenderer.invoke('lykeion:google-sign-in') as Promise<void>,
    daemon,
  }),
);
