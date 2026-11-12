'use strict';
const { app, autoUpdater, net } = require('electron');
const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { openHeldStream } = require('./desktop-held-stream.cjs');
const { openDesktopHeldStream } = require('./production-held.cjs');
const report = process.env.LYK_TEST_REPORT;
const root = process.env.LYK_TEST_PRIVATE;
app.setPath('userData', path.join(root, 'user-data'));

app.whenReady().then(async () => {
  let server;
  let updater;
  let heldSession;
  let failure = false;
  const children = new Set();
  const track = (child) => { children.add(child); child.once('close', () => children.delete(child)); return child; };
  const observed = {};
  const checkpoint = (phase) => fs.writeFileSync(report, JSON.stringify({ phase }));
  try {
    const bytes = Buffer.from('small verified fixture artifact');
    const sha256 = crypto.createHash('sha256').update(bytes).digest('hex');
    const sha512 = crypto.createHash('sha512').update(bytes).digest('base64');
    const arch = process.arch === 'arm64' ? 'arm64' : 'x64';
    const filename = arch === 'arm64' ? 'Lykeion-1.2.3-arm64-mac.zip' : 'Lykeion-1.2.3-mac.zip';
    const payload = Buffer.from(JSON.stringify([1, 'stable', 1, '1.2.3', 'darwin', arch, filename, bytes.length, sha256]));
    const { publicKey, privateKey } = crypto.generateKeyPairSync('ed25519');
    const signature = crypto.sign(null, payload, privateKey);
    const authentic = (content) => crypto.verify(null, content, publicKey, signature);
    observed.badSignatureRejected = !authentic(Buffer.concat([payload, Buffer.from('tamper')]));
    if (!authentic(payload)) throw Error('fixture authentication failed');
    observed.signatureSelfCheck = true;
    const token = crypto.randomBytes(32).toString('hex');
    const yaml = Buffer.from(`version: 1.2.3\nfiles:\n  - url: ${filename}\n    sha512: ${sha512}\n    size: ${bytes.length}\npath: ${filename}\nsha512: ${sha512}\n`);
    let requests = 0;
    let authorized = 0;
    let heldTransfer;
    let activeCancellation;
    let pendingCancellation = false;
    let phase = 'redirect';
    let artifactRequested;
    const artifactStarted = new Promise((resolve) => { artifactRequested = resolve; });
    server = http.createServer((req, res) => {
      requests++;
      if (req.method !== 'GET' || req.headers.authorization !== `Bearer ${token}` || !['/latest-mac.yml', `/${filename}`].includes(req.url)) {
        res.writeHead(403); res.end(); return;
      }
      authorized++;
      if (req.url === '/latest-mac.yml' && phase === 'redirect') {
        res.writeHead(302, { Location: `http://127.0.0.1:${server.address().port}/unapproved` });
        res.end(); return;
      }
      if (req.url === `/${filename}` && phase === 'cancel') {
        artifactRequested();
        return;
      }
      if (req.url === `/${filename}` && heldSession) {
        res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Length': bytes.length });
        heldTransfer = heldSession.deliver(res).then(() => true, () => {
          pendingCancellation = true;
          activeCancellation?.cancel();
          return false;
        });
        return;
      }
      const content = req.url === '/latest-mac.yml' ? yaml : bytes;
      res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Length': content.length });
      res.end(content);
    });
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    const port = server.address().port;
    observed.badBearerRejected = await new Promise((resolve, reject) => {
      http.get({ hostname: '127.0.0.1', port, path: '/latest-mac.yml', headers: { authorization: 'Bearer wrong' } }, (response) => { response.resume(); response.once('end', () => resolve(response.statusCode === 403)); }).on('error', reject);
    });
    const base = `http://127.0.0.1:${port}/`;
    const { MacUpdater } = require('./electron-updater.cjs');
    const configPath = path.join(root, 'app-update.yml');
    fs.writeFileSync(configPath, 'updaterCacheDirName: probe-cache\n');
    fs.mkdirSync(path.join(root, 'user-data'), { recursive: true, mode: 0o700 });
    const adapter = {
      version: '1.0.0', name: 'Probe', isPackaged: true,
      appUpdateConfigPath: configPath, userDataPath: path.join(root, 'user-data'),
      baseCachePath: path.join(root, 'cache'), whenReady: () => Promise.resolve(),
      relaunch: () => { throw Error('unexpected relaunch'); }, quit: () => { throw Error('unexpected quit'); },
      onQuit: () => { throw Error('unexpected quit handler'); },
    };
    let nativeCheck = 0, nativeInstall = 0, nativeFeed = 0;
    const originalSetFeed = autoUpdater.setFeedURL.bind(autoUpdater);
    autoUpdater.setFeedURL = (...args) => { nativeFeed++; return originalSetFeed(...args); };
    autoUpdater.checkForUpdates = () => { nativeCheck++; throw Error('unexpected native check'); };
    autoUpdater.quitAndInstall = () => { nativeInstall++; throw Error('unexpected native install'); };
    updater = new MacUpdater(undefined, adapter);
    const logs = [];
    updater.logger = {
      info: (...values) => logs.push(values.map(String).join(' ')),
      warn: (...values) => logs.push(values.map(String).join(' ')),
      error: (...values) => logs.push(values.map(String).join(' ')),
    };
    updater.autoDownload = false;
    updater.autoInstallOnAppQuit = false;
    updater.disableDifferentialDownload = true;
    updater.setFeedURL({ provider: 'generic', url: base });
    updater.channel = 'latest';
    updater.allowDowngrade = false;
    updater.requestHeaders = { authorization: `Bearer ${token}` };
    const session = updater.netSession;
    await session.setProxy({ mode: 'direct' });
    let blocked = 0;
    session.webRequest.onBeforeRequest((details, callback) => {
      const url = new URL(details.url);
      const allow = url.protocol === 'http:' && url.hostname === '127.0.0.1' && url.port === String(port) && !url.search && ['/latest-mac.yml', `/${filename}`].includes(url.pathname);
      if (!allow) blocked++;
      callback({ cancel: !allow });
    });
    observed.remoteBlocked = await new Promise((resolve) => {
      const request = net.request({ url: 'https://example.invalid/', session });
      request.on('response', (response) => { response.resume(); resolve(false); });
      request.on('error', () => resolve(blocked > 0));
      request.end();
    });
    let eventsBeforeRecheck = 0;
    let recheckStarted = false;
    updater.on('update-downloaded', () => { if (!recheckStarted) eventsBeforeRecheck++; });
    updater.on('error', () => {});
    updater.autoDownload = true;
    checkpoint('redirect');
    observed.guards = updater.autoDownload === true && updater.autoInstallOnAppQuit === false && updater.allowDowngrade === false && updater.disableDifferentialDownload === true;
    let redirected = false;
    try { await updater.checkForUpdates(); } catch { redirected = true; }
    observed.redirectBlocked = redirected && blocked >= 2;
    phase = 'cancel';
    checkpoint('cancel-check');
    const first = updater.checkForUpdates();
    const second = updater.checkForUpdates();
    observed.singleFlight = first === second;
    const canceled = await first;
    if (!canceled?.downloadPromise || !canceled.cancellationToken) throw Error('fixture download not started');
    const canceledResult = canceled.downloadPromise.then(() => false, () => true);
    checkpoint('cancel-wait-artifact');
    await artifactStarted;
    checkpoint('cancel-token');
    canceled.cancellationToken.cancel();
    observed.canceled = await canceledResult;
    checkpoint('retry');
    let failedSafely = true;
    const prepareHeld = async (mode) => {
      const child = track(spawn(process.env.LYK_TEST_NODE, [path.join(root, 'fake-helper.cjs'), mode], {
        env: { PATH: '/usr/bin:/bin', HOME: root }, stdio: ['pipe', 'pipe', 'pipe'],
      }));
      const session = await openHeldStream(child, new AbortController().signal);
      if (session.descriptor.size !== bytes.length || session.descriptor.sha256 !== sha256 || session.descriptor.sha512 !== sha512) throw Error('fixture descriptor mismatch');
      heldTransfer = undefined;
      activeCancellation = undefined;
      pendingCancellation = false;
      return session;
    };
    const spawnGo = (mode) => {
      const home = path.join(root, `go-${mode}`);
      fs.mkdirSync(home, { mode: 0o700 });
      const marker = path.join(home, 'ready');
      const child = track(spawn(path.join(root, 'go-stream-probe'), ['-test.run=^TestPackagedStreamChild$'], {
        env: { PATH: '/usr/bin:/bin', HOME: home, LYK_TEST_PACKAGED_STREAM: mode, LYK_TEST_PACKAGED_HOME: home, LYK_TEST_PACKAGED_READY: marker },
        stdio: ['pipe', 'pipe', 'pipe', 'pipe'],
      }));
      // FD3's peer stays open and silent until child termination.
      child.stdio[3].resume();
      return { child, marker };
    };
    const prepareGo = (mode, suffix = mode) => {
      const home = path.join(root, `module-go-${suffix}`);
      const bin = path.join(home, 'bin');
      fs.mkdirSync(bin, { recursive: true, mode: 0o700 });
      const marker = path.join(home, 'ready');
      const quote = (value) => `'${value.replaceAll("'", "'\\''")}'`;
      const wrapper = path.join(bin, 'lyk');
      // Test-only wrapper supplies ephemeral harness inputs; it is not the production CLI.
      fs.writeFileSync(wrapper, `#!/bin/sh\nHOME=${quote(home)} PATH=/usr/bin:/bin LYK_TEST_PACKAGED_STREAM=${quote(mode)} LYK_TEST_PACKAGED_HOME=${quote(home)} LYK_TEST_PACKAGED_READY=${quote(marker)} exec ${quote(path.join(root, 'go-stream-probe'))} '-test.run=^TestPackagedStreamChild$'\n`, { mode: 0o700 });
      const resources = fs.realpathSync(home);
      const digest = crypto.createHash('sha256').update(fs.readFileSync(wrapper)).digest('hex');
      const manifest = { version: '1.0.0', platform: process.platform, arch: process.arch, daemonSHA256: digest };
      return { resources, manifest, marker };
    };
    for (const mode of ['bad', 'missing', 'extra', 'exit', 'mutation']) {
      phase = mode;
      checkpoint(`invalid-${mode}-prepare`);
      heldSession = await prepareHeld(mode);
      checkpoint(`invalid-${mode}-check`);
      const invalid = await updater.checkForUpdates();
      if (!invalid?.downloadPromise) throw Error('fixture invalid download not started');
      activeCancellation = invalid.cancellationToken;
      if (pendingCancellation) activeCancellation?.cancel();
      checkpoint(`invalid-${mode}-download`);
      const rejected = await invalid.downloadPromise.then(() => false, () => true);
      checkpoint(`invalid-${mode}-transfer`);
      const transfer = await heldTransfer;
      if (!rejected || transfer !== false || nativeFeed !== 0 || nativeCheck !== 0 || nativeInstall !== 0 || eventsBeforeRecheck !== 0) failedSafely = false;
    }
    observed.badStreamsNeverComplete = failedSafely;
    {
      const test = prepareGo('invalid');
      const rejected = await openDesktopHeldStream(test.resources, test.manifest, '1.0.0', new AbortController().signal).then(() => false, () => true);
      observed.realGoInvalidRejected = rejected && fs.existsSync(test.marker) && fs.readFileSync(test.marker, 'utf8') === 'signature-rejected';
    }
    {
      const { child, marker } = spawnGo('liveness');
      const waiting = openHeldStream(child, new AbortController().signal).then(() => false, () => true);
      const deadline = Date.now() + 5000;
      while (!fs.existsSync(marker) && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 20));
      if (!fs.existsSync(marker)) { child.kill('SIGKILL'); throw Error('fixture liveness did not start'); }
      child.stdio[3].destroy();
      observed.realGoLivenessClosed = await waiting;
    }
    {
      const test = prepareGo('liveness', 'module-liveness');
      const controller = new AbortController();
      const waiting = openDesktopHeldStream(test.resources, test.manifest, '1.0.0', controller.signal).then(() => false, () => true);
      const deadline = Date.now() + 5000;
      while (!fs.existsSync(test.marker) && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 20));
      if (!fs.existsSync(test.marker)) { controller.abort(); await waiting; throw Error('fixture module did not block'); }
      controller.abort();
      observed.realModuleMetadataCanceled = await waiting;
    }
    {
      const test = prepareGo('mutation');
      heldSession = await openDesktopHeldStream(test.resources, test.manifest, '1.0.0', new AbortController().signal);
      heldTransfer = undefined; activeCancellation = undefined; pendingCancellation = false;
      const invalid = await updater.checkForUpdates();
      if (!invalid?.downloadPromise) throw Error('fixture mutation download not started');
      activeCancellation = invalid.cancellationToken;
      if (pendingCancellation) activeCancellation?.cancel();
      const rejected = await invalid.downloadPromise.then(() => false, () => true);
      const transfer = await heldTransfer;
      observed.realGoMutationRejected = rejected && transfer === false && nativeFeed === 0 && eventsBeforeRecheck === 0;
    }
    {
      const test = prepareGo('blocked-stream');
      const controller = new AbortController();
      heldSession = await openDesktopHeldStream(test.resources, test.manifest, '1.0.0', controller.signal);
      heldTransfer = undefined; activeCancellation = undefined; pendingCancellation = false;
      const pending = await updater.checkForUpdates();
      if (!pending?.downloadPromise) throw Error('fixture blocked download not started');
      activeCancellation = pending.cancellationToken;
      if (pendingCancellation) activeCancellation?.cancel();
      const rejected = pending.downloadPromise.then(() => false, () => true);
      const deadline = Date.now() + 5000;
      while (!fs.existsSync(test.marker) && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 20));
      if (!fs.existsSync(test.marker)) { controller.abort(); throw Error('fixture stream did not block'); }
      controller.abort();
      await heldSession.stop();
      const rejectedValue = await rejected;
      const transferValue = await heldTransfer;
      observed.realGoCancelReaped = rejectedValue && transferValue === false && nativeFeed === 0 && eventsBeforeRecheck === 0;
    }
    phase = 'success';
    {
      const test = prepareGo('good');
      heldSession = await openDesktopHeldStream(test.resources, test.manifest, '1.0.0', new AbortController().signal);
      heldTransfer = undefined; activeCancellation = undefined; pendingCancellation = false;
      if (heldSession.descriptor.size !== bytes.length || heldSession.descriptor.sha256 !== sha256 || heldSession.descriptor.sha512 !== sha512) throw Error('fixture Go descriptor mismatch');
    }
    const result = await updater.checkForUpdates();
    checkpoint('retry-download');
    if (!result?.isUpdateAvailable || !result.downloadPromise) throw Error('fixture retry not started');
    activeCancellation = result.cancellationToken;
    if (pendingCancellation) activeCancellation?.cancel();
    const paths = await result.downloadPromise;
    if (!(await heldTransfer)) throw Error('fixture held transfer failed');
    checkpoint('verify');
    if (!Array.isArray(paths) || paths.length !== 1) throw Error('fixture output missing');
    recheckStarted = true;
    const pending = fs.readFileSync(paths[0]);
    const matches = (candidate) => candidate.length === bytes.length && crypto.createHash('sha256').update(candidate).digest('hex') === sha256 && crypto.createHash('sha512').update(candidate).digest('base64') === sha512;
    // This local observer demonstrates ordering only; it is not a product gate.
    let verifiedNotifications = 0;
    const notifyIfVerified = (candidate) => { if (matches(candidate)) verifiedNotifications++; };
    notifyIfVerified(Buffer.concat([pending, Buffer.from('tamper')]));
    observed.tamperNotNotified = verifiedNotifications === 0;
    notifyIfVerified(pending);
    observed.exactBytes = matches(pending) && verifiedNotifications === 1;
    observed.eventBeforeRecheck = eventsBeforeRecheck === 1;
    observed.noNativeInstall = nativeCheck === 0 && nativeInstall === 0 && nativeFeed === 1;
    observed.exactRequests = requests - authorized === 1 && authorized >= 19 && authorized <= 24;
    observed.noSecretLogs = logs.every((line) => !line.includes(token));
    observed.packaged = app.isPackaged;
    if (Object.values(observed).some((x) => x !== true)) throw Error('fixture assertion failed');
  } catch {
    failure = true;
  } finally {
    checkpoint('cleanup');
    if (typeof heldSession?.stop === 'function') await heldSession.stop();
    const childStops = [...children].map((child) => new Promise((resolve) => {
      if (child.exitCode !== null || child.signalCode !== null) return resolve(true);
      const timer = setTimeout(() => resolve(false), 2000);
      child.once('close', () => { clearTimeout(timer); resolve(true); });
      child.kill('SIGKILL');
    }));
    const childrenClosed = (await Promise.all(childStops)).every(Boolean);
    const closeLocal = async (listener) => {
      if (!listener?.listening) return true;
      return await new Promise((resolve) => {
        const timer = setTimeout(() => resolve(false), 2000);
        listener.close(() => { clearTimeout(timer); resolve(true); });
        listener.closeAllConnections();
      });
    };
    const closedUpdater = await closeLocal(updater?.server);
    const closedFeed = await closeLocal(server);
    observed.cleanupDone = childrenClosed && closedUpdater && closedFeed;
    if (!observed.cleanupDone) failure = true;
    fs.writeFileSync(report, JSON.stringify({ ...observed, failed: failure }));
    app.exit(failure ? 1 : 0);
  }
});
