import { spawn } from 'node:child_process';
import { Writable } from 'node:stream';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { openHeldStream } from './fixtures/desktop-held-stream.cjs';
import type { HeldStreamSession } from '../apps/desktop/src/desktop-held-stream';

function assertDownloaded(session: HeldStreamSession): asserts session is Extract<HeldStreamSession, { deliver: unknown }> {
  if (session.descriptor.state !== 'downloaded' || !('deliver' in session)) throw Error('unexpected state');
}

const fixture = path.resolve('tests/fixtures/desktop-fake-stream-helper.cjs');
function output() {
  const parts: Buffer[] = [];
  const stream = new Writable({ write(chunk, _encoding, callback) { parts.push(Buffer.from(chunk)); callback(); } });
  return { stream, bytes: () => Buffer.concat(parts) };
}
function child(mode: string) { return spawn(process.execPath, [fixture, mode], { stdio: ['pipe', 'pipe', 'pipe'] }); }

describe('verified helper stream parent', () => {
  it('accepts current state only after clean EOF and process exit', async () => {
    const session = await openHeldStream(child('current'), new AbortController().signal);

    expect(session.descriptor).toEqual({ state: 'current', version: '1.2.3' });
    expect('deliver' in session).toBe(false);
  });
  for (const mode of ['good', 'one']) it(`releases the last byte only after valid ${mode} framing`, async () => {
    const session = await openHeldStream(child(mode), new AbortController().signal);
    assertDownloaded(session);
    const sink = output();
    await session.deliver(sink.stream);
    expect(sink.bytes().length).toBe(session.descriptor.size);
  });
  for (const mode of ['missing', 'bad', 'extra', 'exit', 'mutation']) it(`withholds the final byte for ${mode}`, async () => {
    const session = await openHeldStream(child(mode), new AbortController().signal);
    assertDownloaded(session);
    const sink = output();
    await expect(session.deliver(sink.stream)).rejects.toThrow('Update stream unavailable');
    expect(sink.bytes().length).toBeLessThan(session.descriptor.size);
  });
  it('rejects an oversized descriptor', async () => {
    await expect(openHeldStream(child('oversized'), new AbortController().signal)).rejects.toThrow('Update stream unavailable');
  });
  it('rejects body bytes sent with the descriptor before STREAM', async () => {
    await expect(openHeldStream(child('early'), new AbortController().signal)).rejects.toThrow('Update stream unavailable');
  });
  it('can stop and reap a session abandoned before delivery', async () => {
    const process = child('hang');
    try {
      const session = await openHeldStream(process, new AbortController().signal);
      assertDownloaded(session);
      await session.stop();
      expect(process.exitCode !== null || process.signalCode !== null).toBe(true);
    } finally {
      if (process.exitCode === null && process.signalCode === null) process.kill('SIGKILL');
    }
  });
  it('cancels and reaps a blocked helper', async () => {
    const controller = new AbortController();
    const process = child('hang');
    const session = await openHeldStream(process, controller.signal);
    assertDownloaded(session);
    const sink = output();
    const pending = session.deliver(sink.stream);
    controller.abort();
    await expect(pending).rejects.toThrow('Update stream unavailable');
    expect(process.exitCode !== null || process.signalCode !== null).toBe(true);
  });
  it('cancels a slow consumer under backpressure without releasing the final byte', async () => {
    const controller = new AbortController();
    const process = child('good');
    const session = await openHeldStream(process, controller.signal);
    assertDownloaded(session);
    let bytes = 0;
    const sink = new Writable({ highWaterMark: 1, write(chunk, _encoding, _callback) { bytes += chunk.length; controller.abort(); } });
    await expect(session.deliver(sink)).rejects.toThrow('Update stream unavailable');
    expect(bytes).toBeLessThan(session.descriptor.size);
    expect(process.exitCode !== null || process.signalCode !== null).toBe(true);
  });
  it('reports a destination finalization error instead of success', async () => {
    const session = await openHeldStream(child('good'), new AbortController().signal);
    assertDownloaded(session);
    const sink = new Writable({
      write(_chunk, _encoding, callback) { callback(); },
      final(callback) { setImmediate(() => callback(new Error('late failure'))); },
    });
    await expect(session.deliver(sink)).rejects.toThrow('Update stream unavailable');
  });
});
