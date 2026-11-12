'use strict';
const { app } = require('electron');
const fs = require('node:fs');
const path = require('node:path');
const report = process.env.LYK_TEST_REPORT;
const root = process.env.LYK_TEST_PRIVATE;
app.setPath('userData', path.join(root, 'user-data'));
app.whenReady().then(async () => {
  try {
    const { MacUpdater } = require('./electron-updater.cjs');
    const configPath = path.join(root, 'app-update.yml');
    fs.writeFileSync(configPath, 'updaterCacheDirName: fixed-cache\n');
    const adapter = {
      version: '1.0.0', name: 'Test', isPackaged: true,
      appUpdateConfigPath: configPath,
      userDataPath: path.join(root, 'user-data'),
      baseCachePath: path.join(root, 'cache'),
      whenReady: () => Promise.resolve(), relaunch: () => { throw Error('unexpected relaunch'); },
      quit: () => { throw Error('unexpected quit'); }, onQuit: () => { throw Error('unexpected quit handler'); },
    };
    const updater = new MacUpdater(undefined, adapter);
    updater.logger = null;
    updater.autoDownload = false;
    updater.autoInstallOnAppQuit = false;
    updater.allowDowngrade = false;
    const helper = await updater.getOrCreateDownloadHelper();
    const value = {
      packaged: app.isPackaged,
      adapter: updater.app === adapter,
      executor: !!updater.httpExecutor,
      autoDownload: updater.autoDownload,
      autoInstallOnAppQuit: updater.autoInstallOnAppQuit,
      allowDowngrade: updater.allowDowngrade,
      cache: helper.cacheDir === path.join(root, 'cache', 'fixed-cache'),
    };
    fs.writeFileSync(report, JSON.stringify(value));
    app.exit(value.packaged && value.adapter && value.executor && value.cache && !value.autoDownload && !value.autoInstallOnAppQuit && !value.allowDowngrade ? 0 : 1);
  } catch {
    fs.writeFileSync(report, JSON.stringify({ error: 'fixture failed' }));
    app.exit(1);
  }
});
