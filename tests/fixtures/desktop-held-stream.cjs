'use strict';
// Test-only parent protocol. It is not imported or enabled by the desktop app.
const { createHash } = require('node:crypto');
const MAX_DESCRIPTOR = 1024;
const MAX_ARTIFACT = 1 << 30;
const MAX_CHUNK = 128 * 1024;
const bad = () => new Error('Update stream unavailable');

async function openHeldStream(child, signal) {
  let sink;
  let stopping = false;
  let force;
  let stderrBytes = 0;
  let closed = false;
  const done = new Promise((resolve) => {
    child.once('error', () => {});
    child.once('close', (code, exitSignal) => { closed = true; resolve(code === 0 && exitSignal === null); });
  });
  const stop = () => {
    if (stopping) return;
    stopping = true;
    if (sink && !sink.destroyed) sink.destroy();
    child.stdout.destroy();
    child.stdin.destroy();
    if (!closed) {
      child.kill('SIGTERM');
      force = setTimeout(() => { if (!closed) child.kill('SIGKILL'); }, 2000);
    }
  };
  const timer = setTimeout(stop, 30 * 60 * 1000);
  const cleanup = () => {
    clearTimeout(timer);
    if (force) clearTimeout(force);
    signal.removeEventListener('abort', stop);
    if (sink) { sink.off('error', stop); sink.off('close', stop); }
  };
  child.stdin.on('error', stop);
  child.stderr.on('data', (data) => { stderrBytes += data.length; if (stderrBytes > 4096) stop(); });
  signal.addEventListener('abort', stop, { once: true });
  const iterator = child.stdout[Symbol.asyncIterator]();
  try {
    if (signal.aborted) throw bad();
    let header = Buffer.alloc(0);
    let pending = Buffer.alloc(0);
    for (;;) {
      const next = await iterator.next();
      if (next.done || next.value.length > MAX_CHUNK) throw bad();
      const chunk = Buffer.from(next.value);
      const newline = chunk.indexOf(10);
      if (newline < 0) {
        if (header.length + chunk.length > MAX_DESCRIPTOR) throw bad();
        header = Buffer.concat([header, chunk]);
        continue;
      }
      if (header.length + newline >= MAX_DESCRIPTOR) throw bad();
      header = Buffer.concat([header, chunk.subarray(0, newline)]);
      pending = chunk.subarray(newline + 1);
      break;
    }
    const raw = header.toString('utf8');
    if (pending.length) throw bad();
    const descriptor = JSON.parse(raw);
    const version = /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/;
    if (descriptor && typeof descriptor === 'object' && !Array.isArray(descriptor) && JSON.stringify(descriptor) === raw && descriptor.state === 'current' && typeof descriptor.version === 'string' && version.test(descriptor.version) && Object.keys(descriptor).join(',') === 'state,version') {
      if (pending.length || !(await iterator.next()).done || !(await done) || signal.aborted || stopping) throw bad();
      cleanup();
      return { descriptor: Object.freeze(descriptor) };
    }
    if (!descriptor || typeof descriptor !== 'object' || Array.isArray(descriptor) || JSON.stringify(descriptor) !== raw || descriptor.state !== 'downloaded' || typeof descriptor.version !== 'string' || !version.test(descriptor.version) || !Number.isSafeInteger(descriptor.size) || descriptor.size < 1 || descriptor.size > MAX_ARTIFACT || typeof descriptor.sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(descriptor.sha256) || typeof descriptor.sha512 !== 'string' || Buffer.from(descriptor.sha512, 'base64').length !== 64 || Buffer.from(descriptor.sha512, 'base64').toString('base64') !== descriptor.sha512 || Object.keys(descriptor).join(',') !== 'state,version,size,sha256,sha512') throw bad();
    Object.freeze(descriptor);
    if (signal.aborted || stopping) throw bad();
    let used = false;
    return {
      descriptor,
      async stop() {
        stop();
        await done;
        cleanup();
      },
      async deliver(destination) {
        if (used) throw bad();
        used = true;
        sink = destination;
        destination.on('error', stop);
        destination.on('close', stop);
        const sha256 = createHash('sha256');
        const sha512 = createHash('sha512');
        let remaining = descriptor.size;
        let held;
        let trailer;
        const write = async (buffer) => {
          if (signal.aborted || stopping || destination.destroyed) throw bad();
          if (!buffer.length || destination.write(buffer)) return;
          await new Promise((resolve, reject) => {
            const clean = () => { destination.off('drain', onDrain); destination.off('close', onFail); destination.off('error', onFail); signal.removeEventListener('abort', onFail); };
            const onDrain = () => { clean(); resolve(); };
            const onFail = () => { clean(); reject(bad()); };
            destination.once('drain', onDrain); destination.once('close', onFail); destination.once('error', onFail); signal.addEventListener('abort', onFail, { once: true });
            if (signal.aborted || destination.destroyed) onFail();
          });
        };
        try {
          if (signal.aborted || stopping) throw bad();
          child.stdin.end('STREAM\n');
          for (;;) {
            const next = pending.length ? { done: false, value: pending } : await iterator.next();
            pending = Buffer.alloc(0);
            if (next.done) break;
            if (next.value.length > MAX_CHUNK || signal.aborted || stopping) throw bad();
            let chunk = Buffer.from(next.value);
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
              if (trailer !== undefined || chunk.length !== 1) throw bad();
              trailer = chunk[0];
            }
          }
          const exited = await done;
          if (signal.aborted || stopping || !exited || remaining !== 0 || trailer !== 1 || !held || sha256.digest('hex') !== descriptor.sha256 || sha512.digest('base64') !== descriptor.sha512) throw bad();
          await write(held);
          await new Promise((resolve, reject) => {
            const clean = () => { destination.off('finish', onFinish); destination.off('close', onFail); destination.off('error', onFail); signal.removeEventListener('abort', onFail); };
            const onFinish = () => { clean(); resolve(); };
            const onFail = () => { clean(); reject(bad()); };
            destination.once('finish', onFinish); destination.once('close', onFail); destination.once('error', onFail); signal.addEventListener('abort', onFail, { once: true });
            if (signal.aborted || destination.destroyed) { onFail(); return; }
            try { destination.end(); } catch { onFail(); }
          });
          cleanup();
        } catch {
          stop();
          await done;
          cleanup();
          throw bad();
        }
      },
    };
  } catch {
    stop();
    await done;
    cleanup();
    throw bad();
  }
}
module.exports = { openHeldStream };
