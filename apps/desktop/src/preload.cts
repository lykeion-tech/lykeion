import { contextBridge, ipcRenderer } from 'electron';
contextBridge.exposeInMainWorld('lykeionDesktop', Object.freeze({
  platform: process.platform,
  openDocumentation: () => ipcRenderer.invoke('lykeion:open-documentation') as Promise<void>,
}));
