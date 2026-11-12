'use strict';
// Test-only parent: owns the sole peer for child fd 3 and never writes to it.
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const [binary, mode, home, ready, finished, pidfile] = process.argv.slice(2);
const child = spawn(binary, ['-test.run=^TestLivenessProcessChild$'], {
  env: { PATH: '/usr/bin:/bin', HOME: home, LYK_TEST_LIVENESS_MODE: mode, LYK_TEST_LIVENESS_HOME: home, LYK_TEST_LIVENESS_READY: ready, LYK_TEST_LIVENESS_FINISHED: finished },
  stdio: ['pipe', 'pipe', 'ignore', 'pipe'],
});
// Spawn an unrelated child *after* the liveness pipe exists. It anchors the
// fixture process group and must not inherit the liveness peer.
spawn('/bin/sleep', ['60'], { stdio: 'ignore' });
fs.writeFileSync(pidfile, String(child.pid));
child.on('error', () => process.exit(4));
child.on('exit', () => process.exit(5));
if (mode === 'stream') {
  child.stdout.once('data', () => { child.stdout.pause(); child.stdin.end('STREAM\n'); });
} else {
  child.stdout.resume();
}
setInterval(() => {}, 1000);
