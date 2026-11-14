import { app } from 'electron';
import { spawn } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import path from 'node:path';

app.setPath('userData', path.join(process.env.TMPDIR, 'profile'));
app.whenReady().then(() => {
  const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {
    env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' },
    stdio: 'ignore',
  });
  writeFileSync(process.env.LYK_HANG_FIXTURE_PIDS, JSON.stringify([process.pid, child.pid]));
  setInterval(() => {}, 1000);
});
