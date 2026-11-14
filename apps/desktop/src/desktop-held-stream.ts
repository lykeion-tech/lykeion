import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { Duplex, type Writable } from 'node:stream';
import path from 'node:path';
import { packagedDaemonVerifier } from './packaged-daemon.js';

const maxDescriptor = 1024;
const maxArtifact = 1 << 30;
const maxChunk = 128 * 1024;
const stableVersion = /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/;
const unavailable = () => new Error('Update stream unavailable');

type CurrentDescriptor = Readonly<{ state: 'current'; version: string }>;
type DownloadDescriptor = Readonly<{ state: 'downloaded'; version: string; size: number; sha256: string; sha512: string }>;
export type HeldStreamSession =
  | { descriptor: CurrentDescriptor }
  | { descriptor: DownloadDescriptor; deliver(destination: Writable): Promise<void>; stop(): Promise<void> };

const discardDestinationError = () => {};

export function watchHeldLiveness(pipe: Duplex, isClosed: () => boolean, isStopping: () => boolean, stop: () => void): () => void {
  let ended = false;
  let grace: NodeJS.Timeout | undefined;
  const onEnd = () => {
    ended = true;
    pipe.destroy();
    if (!isClosed() && !isStopping()) grace = setTimeout(() => { if (!isClosed()) stop(); }, 1000);
  };
  const onClose = () => { if (!ended && !isClosed() && !isStopping()) stop(); };
  pipe.on('error', discardDestinationError);
  pipe.on('error', stop);
  pipe.once('end', onEnd);
  pipe.once('close', onClose);
  pipe.resume();
  return () => {
    if (grace) clearTimeout(grace);
    pipe.off('error', stop);
    pipe.off('end', onEnd);
    pipe.off('close', onClose);
  };
}

// The helper path and verifier are derived together from packaged resources.
// This module is not connected to production IPC while platform proofs are closed.
// Verification and spawn remain separate path operations and require an
// independent protected-ancestry proof before this module can be enabled.
export async function openDesktopHeldStream(resources: string, manifest: unknown, version: string, signal: AbortSignal): Promise<HeldStreamSession> {
  const binary = path.join(resources, 'bin', 'lyk');
  try {
    if (signal.aborted) throw unavailable();
    const verify = packagedDaemonVerifier(resources, manifest, version);
    let timer: NodeJS.Timeout | undefined;
    let onAbort: (() => void) | undefined;
    try {
      const interrupted = new Promise<boolean>((resolve) => {
        onAbort = () => resolve(false);
        signal.addEventListener('abort', onAbort, { once: true });
        timer = setTimeout(() => resolve(false), 10_000);
        if (signal.aborted) onAbort();
      });
      if (!(await Promise.race([verify(), interrupted])) || signal.aborted) throw unavailable();
    } finally {
      if (timer) clearTimeout(timer);
      if (onAbort) signal.removeEventListener('abort', onAbort);
    }
  } catch { throw unavailable(); }
  const child = spawn(binary, ['desktop-update-stream'], { env: {}, stdio: ['pipe', 'pipe', 'pipe', 'pipe'] });
  const liveness = child.stdio[3];
  if (!(liveness instanceof Duplex)) { child.kill('SIGKILL'); throw unavailable(); }
  let sink: Writable | undefined;
  let stopping = false;
  let force: NodeJS.Timeout | undefined;
  let stderrBytes = 0;
  let closed = false;
  let deliveryDone: Promise<void> | undefined;
  const done = new Promise<boolean>((resolve) => {
    child.once('error', () => {});
    child.once('close', (code, exitSignal) => { closed = true; resolve(code === 0 && exitSignal === null); });
  });
  const stop = () => {
    if (stopping) return;
    stopping = true;
    if (sink && !sink.destroyed) sink.destroy();
    liveness.destroy(); child.stdout.destroy(); child.stdin.destroy(); child.stderr.destroy();
    if (!closed) {
      child.kill('SIGTERM');
      force = setTimeout(() => { if (!closed) child.kill('SIGKILL'); }, 2000);
    }
  };
  const stopLivenessWatch = watchHeldLiveness(liveness, () => closed, () => stopping, stop);
  const timer = setTimeout(stop, 30 * 60 * 1000);
  const cleanup = () => {
    clearTimeout(timer);
    if (force) clearTimeout(force);
    stopLivenessWatch();
    signal.removeEventListener('abort', stop);
    if (sink) { sink.off('error', stop); sink.off('close', stop); }
  };
  child.stdin.on('error', stop);
  child.stderr.on('data', (data: Buffer) => { stderrBytes += data.length; if (stderrBytes > 4096) stop(); });
  child.stderr.on('error', stop);
  signal.addEventListener('abort', stop, { once: true });
  const iterator = child.stdout[Symbol.asyncIterator]();
  try {
    if (signal.aborted) throw unavailable();
    let header = Buffer.alloc(0);
    for (;;) {
      const next = await iterator.next();
      if (next.done || next.value.length > maxChunk) throw unavailable();
      const chunk = Buffer.from(next.value as Buffer);
      const newline = chunk.indexOf(10);
      if (newline < 0) {
        if (header.length + chunk.length > maxDescriptor) throw unavailable();
        header = Buffer.concat([header, chunk]);
        continue;
      }
      if (header.length + newline >= maxDescriptor || chunk.length !== newline + 1) throw unavailable();
      header = Buffer.concat([header, chunk.subarray(0, newline)]);
      break;
    }
    const raw = header.toString('utf8');
    const descriptor: unknown = JSON.parse(raw);
    if (!descriptor || typeof descriptor !== 'object' || Array.isArray(descriptor) || JSON.stringify(descriptor) !== raw) throw unavailable();
    const value = descriptor as Record<string, unknown>;
    if (value.state === 'current' && typeof value.version === 'string' && stableVersion.test(value.version) && Object.keys(value).join(',') === 'state,version') {
      if (!(await iterator.next()).done || !(await done) || signal.aborted || stopping) throw unavailable();
      cleanup();
      return { descriptor: Object.freeze(value) as CurrentDescriptor };
    }
    if (value.state !== 'downloaded' || typeof value.version !== 'string' || !stableVersion.test(value.version) || typeof value.size !== 'number' || !Number.isSafeInteger(value.size) || value.size < 1 || value.size > maxArtifact || typeof value.sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.sha256) || typeof value.sha512 !== 'string' || Buffer.from(value.sha512, 'base64').length !== 64 || Buffer.from(value.sha512, 'base64').toString('base64') !== value.sha512 || Object.keys(value).join(',') !== 'state,version,size,sha256,sha512') throw unavailable();
    const trusted = Object.freeze(value) as DownloadDescriptor;
    if (signal.aborted || stopping) throw unavailable();
    let used = false;
    return {
      descriptor: trusted,
      async stop() { stop(); await done; if (deliveryDone) await deliveryDone; cleanup(); },
      async deliver(destination: Writable) {
        if (used) throw unavailable();
        used = true; sink = destination;
        let finishDelivery!: () => void;
        deliveryDone = new Promise<void>((resolve) => { finishDelivery = resolve; });
        // The caller transfers destination ownership; retain a non-capturing
        // error listener so delayed writes after close cannot crash the process.
        destination.on('error', discardDestinationError);
        destination.on('error', stop); destination.on('close', stop);
        const sha256 = createHash('sha256'), sha512 = createHash('sha512');
        let remaining = trusted.size, trailer: number | undefined, held: Buffer | undefined;
        const write = async (buffer: Buffer) => {
          if (signal.aborted || stopping || destination.destroyed) throw unavailable();
          if (!buffer.length || destination.write(buffer)) return;
          await new Promise<void>((resolve, reject) => {
            const clean = () => { destination.off('drain', onDrain); destination.off('close', onFail); destination.off('error', onFail); signal.removeEventListener('abort', onFail); };
            const onDrain = () => { clean(); resolve(); };
            const onFail = () => { clean(); reject(unavailable()); };
            destination.once('drain', onDrain); destination.once('close', onFail); destination.once('error', onFail); signal.addEventListener('abort', onFail, { once: true });
            if (signal.aborted || destination.destroyed) onFail();
          });
        };
        try {
          if (signal.aborted || stopping) throw unavailable();
          child.stdin.end('STREAM\n');
          for (;;) {
            const next = await iterator.next();
            if (next.done) break;
            if (next.value.length > maxChunk || signal.aborted || stopping) throw unavailable();
            let chunk = Buffer.from(next.value as Buffer);
            const take = Math.min(remaining, chunk.length);
            if (take) {
              const part = chunk.subarray(0, take);
              sha256.update(part); sha512.update(part); remaining -= take;
              if (held) await write(held);
              if (part.length > 1) await write(part.subarray(0, -1));
              held = part.subarray(-1);
              chunk = chunk.subarray(take);
            }
            if (chunk.length) {
              if (trailer !== undefined || chunk.length !== 1) throw unavailable();
              trailer = chunk[0];
            }
          }
          const exited = await done;
          if (signal.aborted || stopping || !exited || remaining !== 0 || trailer !== 1 || !held || sha256.digest('hex') !== trusted.sha256 || sha512.digest('base64') !== trusted.sha512) throw unavailable();
          await write(held);
          await new Promise<void>((resolve, reject) => {
            const clean = () => { destination.off('finish', onFinish); destination.off('close', onFail); destination.off('error', onFail); signal.removeEventListener('abort', onFail); };
            const onFinish = () => { clean(); resolve(); };
            const onFail = () => { clean(); reject(unavailable()); };
            destination.once('finish', onFinish); destination.once('close', onFail); destination.once('error', onFail); signal.addEventListener('abort', onFail, { once: true });
            if (signal.aborted || destination.destroyed) { onFail(); return; }
            try { destination.end(); } catch { onFail(); }
          });
          cleanup();
        } catch {
          stop(); await done; cleanup(); throw unavailable();
        } finally {
          finishDelivery();
        }
      },
    };
  } catch {
    stop(); await done; cleanup(); throw unavailable();
  }
}
