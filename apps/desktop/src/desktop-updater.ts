import { createRequire } from 'node:module';
import path from 'node:path';
import { Notification } from 'electron';

const startupDelay = 5_000;
const checkInterval = 60 * 60 * 1000;

type Updater = {
  channel: string | null;
  allowDowngrade: boolean;
  autoDownload: boolean;
  autoInstallOnAppQuit: boolean;
  on(event: 'update-downloaded', listener: (info: { version: string }) => void): unknown;
  on(event: 'error', listener: (error: Error) => void): unknown;
  checkForUpdates(): Promise<{ downloadPromise?: Promise<unknown> } | null>;
};

// The app updates itself from the GitHub release feed written into the package.
// Updates download in the background and install when the app quits; quitting
// leaves the daemon running, and the new app moves it to its own runtime only
// once it is idle. macOS installs only an update signed by the same Developer ID.
export function startDesktopUpdater(outDirectory: string, notify = true): () => void {
  const { autoUpdater } = createRequire(import.meta.url)(
    path.join(outDirectory, 'electron-updater.cjs'),
  ) as { autoUpdater: Updater };
  if (process.platform === 'darwin' && process.arch === 'x64') {
    // Intel Macs read their own feed; setting a channel also allows
    // downgrades, which a feed per architecture must not.
    autoUpdater.channel = 'latest-x64';
    autoUpdater.allowDowngrade = false;
  }
  autoUpdater.autoDownload = true;
  autoUpdater.autoInstallOnAppQuit = true;
  let notified = '';
  autoUpdater.on('update-downloaded', ({ version }) => {
    if (!notify || notified === version || !Notification.isSupported()) return;
    notified = version;
    new Notification({
      title: `Lykeion ${version} is ready`,
      body: 'It installs when you quit Lykeion. Running work is not interrupted.',
    }).show();
  });
  autoUpdater.on('error', (error) => console.error('Update failed:', error.message));
  let checking: Promise<unknown> | null = null;
  const check = () => {
    checking ??= autoUpdater
      .checkForUpdates()
      .then((result) => result?.downloadPromise)
      .catch((error: Error) => console.error('Update check failed:', error.message))
      .finally(() => {
        checking = null;
      });
  };
  const first = setTimeout(check, startupDelay);
  const every = setInterval(check, checkInterval);
  every.unref();
  return () => {
    clearTimeout(first);
    clearInterval(every);
  };
}
