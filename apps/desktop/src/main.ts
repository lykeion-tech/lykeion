import { app, BrowserWindow, ipcMain, shell } from 'electron';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { controlBridge } from './control-bridge.js';

const directory = path.dirname(fileURLToPath(import.meta.url));
const developmentUrl = process.env.LYKEION_RENDERER_URL;
if (developmentUrl && !['http://127.0.0.1:1430', 'http://localhost:1430'].includes(developmentUrl)) throw new Error('Unsupported development renderer origin');
app.setName('Lykeion Preview');
app.setPath('userData', path.join(app.getPath('appData'), 'Lykeion Preview'));
let window: BrowserWindow | null = null;
function createWindow() {
  window = new BrowserWindow({
    width: 1920, height: 960, minWidth: 900, minHeight: 640, title: 'Lykeion',
    backgroundColor: '#ffffff', titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    webPreferences: { preload: path.join(directory, 'preload.cjs'), sandbox: true, contextIsolation: true, nodeIntegration: false },
  });
  window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  window.webContents.on('will-navigate', (event) => { event.preventDefault(); });
  window.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
  if (developmentUrl) void window.loadURL(developmentUrl);
  else void window.loadFile(path.resolve(directory, '../../../dist/dashboard/index.html'));
  window.on('closed', () => { window = null; });
}
void app.whenReady().then(() => {
  const control=controlBridge(()=>window,process.env.LYKEION_CONTROL_ORIGIN||'http://127.0.0.1:18431');
  ipcMain.handle('lykeion:control', control.request);
  ipcMain.handle('lykeion:realtime', control.realtime);
  app.on('before-quit',control.close);
  ipcMain.handle('lykeion:control-origin', event=>{if(event.sender!==window?.webContents||event.senderFrame!==window.webContents.mainFrame)throw new Error('Invalid sender');return process.env.LYKEION_CONTROL_ORIGIN||'http://127.0.0.1:18431';});
  ipcMain.handle('lykeion:open-documentation', async (event) => {
    if (event.sender !== window?.webContents || event.senderFrame !== window.webContents.mainFrame) throw new Error('Invalid sender');
    await shell.openExternal('https://github.com/lykeion-ia/lykeion');
  });
  createWindow();
  app.on('activate', () => { if (!window) createWindow(); });
});
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
