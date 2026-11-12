import { chmodSync, mkdtempSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Duplex, Writable } from 'node:stream';
import { describe, expect, it, vi } from 'vitest';
import * as packaged from '../apps/desktop/src/packaged-daemon';
import { openDesktopHeldStream, watchHeldLiveness } from '../apps/desktop/src/desktop-held-stream';

function fixture(mode: string) {
  const dir = realpathSync(mkdtempSync(path.join(tmpdir(), 'lyk-real-parent-')));
  const resources = path.join(dir, 'resources');
  mkdirSync(path.join(resources, 'bin'), { recursive: true });
  const binary = path.join(resources, 'bin', 'lyk');
  const marker = path.join(dir, 'spawned');
  const source = readFileSync(path.resolve('tests/fixtures/desktop-fake-stream-helper.cjs'), 'utf8')
    .replace("const mode = process.argv[2];", `const mode = ${JSON.stringify(mode)}; require('node:fs').fstatSync(3); require('node:fs').writeFileSync(${JSON.stringify(marker)}, 'yes');`)
    .replace('process.stdout.write(descriptor);', "process.stdout.write(descriptor); if (mode === 'fdclose') { require('node:fs').closeSync(3); setInterval(() => {}, 1000); return; }");
  writeFileSync(binary, `#!${process.execPath}\n${source}`); chmodSync(binary, 0o700);
  const manifest = { version: '1.0.0', platform: process.platform, arch: process.arch, daemonSHA256: createHash('sha256').update(readFileSync(binary)).digest('hex') };
  return { resources, manifest, marker, cleanup: () => rmSync(dir, { recursive: true, force: true }) };
}
function sink() {
  const parts: Buffer[] = [];
  const stream = new Writable({ write(chunk, _encoding, done) { parts.push(Buffer.from(chunk)); done(); } });
  return { stream, bytes: () => Buffer.concat(parts) };
}

describe.skipIf(process.platform === 'win32')('desktop held-stream module', () => {
  it('stops safely on an injected liveness error', () => {
    const pipe = new Duplex({ read() {}, write(_chunk, _encoding, done) { done(); } });
    const stop = vi.fn();
    const cleanup = watchHeldLiveness(pipe, () => false, () => false, stop);
    pipe.emit('error', new Error('private diagnostic'));
    expect(stop).toHaveBeenCalledOnce();
    cleanup(); pipe.destroy();
  });
  it('bounds and cancels a verifier that never settles', async () => {
    const f = fixture('good');
    const spy = vi.spyOn(packaged, 'packagedDaemonVerifier').mockReturnValue(() => new Promise<boolean>(() => {}));
    try {
      vi.useFakeTimers();
      const pending = openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
      const rejected = expect(pending).rejects.toThrow('Update stream unavailable');
      await vi.advanceTimersByTimeAsync(10_001);
      await rejected;
      const controller = new AbortController();
      const canceled = openDesktopHeldStream(f.resources, f.manifest, '1.0.0', controller.signal);
      controller.abort();
      await expect(canceled).rejects.toThrow('Update stream unavailable');
      expect(existsSync(f.marker)).toBe(false);
    } finally { spy.mockRestore(); vi.useRealTimers(); f.cleanup(); }
  });
  it('does not spawn when helper verification fails', async () => {
    const f = fixture('good');
    try {
      await expect(openDesktopHeldStream(f.resources, { ...f.manifest, daemonSHA256: '0'.repeat(64) }, '1.0.0', new AbortController().signal)).rejects.toThrow('Update stream unavailable');
      expect(existsSync(f.marker)).toBe(false);
    } finally { f.cleanup(); }
  });
  for (const mode of ['good', 'one']) it(`forwards verified ${mode} framing through a real FD3`, async () => {
    const f = fixture(mode);
    try {
      const session = await openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
      if (session.descriptor.state !== 'downloaded') throw Error('unexpected state');
      const output = sink(); await session.deliver(output.stream);
      expect(output.bytes().length).toBe(session.descriptor.size);
      expect(existsSync(f.marker)).toBe(true);
    } finally { f.cleanup(); }
  });
  for (const mode of ['bad', 'missing', 'extra', 'exit', 'mutation', 'early']) it(`fails closed on ${mode}`, async () => {
    const f = fixture(mode);
    try {
      const run = async () => {
        const session = await openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
        if (session.descriptor.state !== 'downloaded') throw Error('unexpected state');
        const output = sink();
        try { await session.deliver(output.stream); } finally { expect(output.bytes().length).toBeLessThan(session.descriptor.size); }
      };
      await expect(run()).rejects.toThrow('Update stream unavailable');
    } finally { f.cleanup(); }
  });
  it('stops a session abandoned before transfer', async () => {
    const f = fixture('hang');
    try {
      const session = await openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
      if (session.descriptor.state !== 'downloaded') throw Error('unexpected state');
      await session.stop();
    } finally { f.cleanup(); }
  });
  it('fails and reaps a child that closes its liveness channel prematurely', async () => {
    const f = fixture('fdclose');
    try {
      const session = await openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
      if (session.descriptor.state !== 'downloaded') throw Error('unexpected state');
      await expect(session.deliver(sink().stream)).rejects.toThrow('Update stream unavailable');
    } finally { f.cleanup(); }
  }, 5000);
  it('coordinates stop with a delayed destination write error', async () => {
    const f = fixture('good');
    try {
      const session = await openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
      if (session.descriptor.state !== 'downloaded') throw Error('unexpected state');
      let started!: () => void;
      const writing = new Promise<void>((resolve) => { started = resolve; });
      const destination = new Writable({ highWaterMark: 1, write(_chunk, _encoding, done) { started(); setTimeout(() => done(new Error('late private error')), 30); } });
      const delivering = session.deliver(destination);
      const rejected = expect(delivering).rejects.toThrow('Update stream unavailable');
      await writing;
      await session.stop();
      await rejected;
      await new Promise((resolve) => setTimeout(resolve, 50));
    } finally { f.cleanup(); }
  });
  it('returns current only after a clean child exit', async () => {
    const f = fixture('current');
    try {
      const session = await openDesktopHeldStream(f.resources, f.manifest, '1.0.0', new AbortController().signal);
      expect(session.descriptor).toEqual({ state: 'current', version: '1.2.3' });
    } finally { f.cleanup(); }
  });
});
