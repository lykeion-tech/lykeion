import { contextBridge, ipcRenderer } from 'electron';
contextBridge.exposeInMainWorld('lykeionDesktop', Object.freeze({
  platform: process.platform,
  realtime: (input:{action:'open'|'read'|'close';workspace?:string;after?:string;id?:string}) => ipcRenderer.invoke('lykeion:realtime',input) as Promise<{id:string;frames:unknown[];closed:boolean}>,
  controlOrigin: () => ipcRenderer.invoke('lykeion:control-origin') as Promise<string>,
  control: (method:'GET'|'POST',path:string,body?:unknown,key?:string,csrf?:string) => ipcRenderer.invoke('lykeion:control',{method,path,body,key,csrf}) as Promise<{status:number;body:unknown}>,
  openDocumentation: () => ipcRenderer.invoke('lykeion:open-documentation') as Promise<void>,
}));
