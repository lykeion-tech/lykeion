'use strict';
const { app } = require('electron');
const fs = require('node:fs');
const crypto = require('node:crypto');
const path = require('node:path');
const { Writable } = require('node:stream');
const { openDesktopHeldStream } = require('./desktop-held-stream.cjs');
const root = process.env.LYK_TEST_PRIVATE;
const report = process.env.LYK_TEST_REPORT;
app.setPath('userData', root);
app.whenReady().then(async () => {
  const observed = {};
  try {
    const prepare = (mode) => {
      const resources = fs.realpathSync(path.join(root, mode));
      const digest = crypto.createHash('sha256').update(fs.readFileSync(path.join(resources, 'bin', 'lyk'))).digest('hex');
      return { resources, manifest: { version: '1.0.0', platform: process.platform, arch: process.arch, daemonSHA256: digest } };
    };
    const good = prepare('good');
    observed.phase = 'good-open';
    const session = await openDesktopHeldStream(good.resources, good.manifest, '1.0.0', new AbortController().signal);
    observed.phase = 'good-deliver';
    const chunks = [];
    const sink = new Writable({ write(chunk, _encoding, done) { chunks.push(Buffer.from(chunk)); done(); } });
    await session.deliver(sink);
    observed.packaged = app.isPackaged;
    observed.good = Buffer.concat(chunks).length === session.descriptor.size;
    const badInput = prepare('bad');
    observed.phase = 'bad-open';
    const bad = await openDesktopHeldStream(badInput.resources, badInput.manifest, '1.0.0', new AbortController().signal);
    let received = 0;
    const invalidSink = new Writable({ write(chunk, _encoding, done) { received += chunk.length; done(); } });
    let rejected = false;
    try { await bad.deliver(invalidSink); } catch { rejected = true; }
    observed.badRejected = rejected && received < bad.descriptor.size;
    observed.disabledVerifier = await openDesktopHeldStream(good.resources, { ...good.manifest, daemonSHA256: '0'.repeat(64) }, '1.0.0', new AbortController().signal).then(() => false, () => true);
    delete observed.phase;
  } catch { observed.failed = true; }
  fs.writeFileSync(report, JSON.stringify(observed));
  app.exit(Object.values(observed).every((value) => value === true) && observed.packaged && observed.good && observed.badRejected && observed.disabledVerifier ? 0 : 1);
});
