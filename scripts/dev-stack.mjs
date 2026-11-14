import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const children = new Set();
let stopping = false;

function stop(code) {
  if (stopping) return;
  stopping = true;
  process.exitCode = code;
  for (const child of children) {
    try {
      if (process.platform === 'win32') child.kill('SIGTERM');
      else process.kill(-child.pid, 'SIGTERM');
    } catch (error) {
      if (error.code !== 'ESRCH') console.error(error);
    }
  }
}

function start(name, command, args, cwd) {
  const child = spawn(command, args, {
    cwd,
    env: process.env,
    stdio: 'inherit',
    detached: process.platform !== 'win32',
  });
  children.add(child);
  child.on('error', (error) => {
    console.error(`Unable to start ${name}: ${error.message}`);
    children.delete(child);
    stop(1);
  });
  child.on('exit', (code, signal) => {
    children.delete(child);
    if (!stopping) {
      console.error(`${name} stopped (${signal ?? code})`);
      stop(code || 1);
    }
  });
}

start('server', 'go', ['run', '-p', '1', './cmd/server'], path.join(root, 'server'));
start('dashboard', 'npm', ['run', 'web:app'], root);
start('landing page', 'npm', ['run', 'web'], root);
process.on('SIGINT', () => stop(0));
process.on('SIGTERM', () => stop(0));
