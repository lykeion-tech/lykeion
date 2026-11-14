'use strict';
const crypto = require('node:crypto');
const mode = process.argv[2];
if (mode === 'current') { process.stdout.write('{"state":"current","version":"1.2.3"}\n', () => process.exit(0)); return; }
const body = Buffer.from(mode === 'one' ? 'x' : 'small verified fixture artifact');
const h256 = crypto.createHash('sha256').update(body).digest('hex');
const h512 = crypto.createHash('sha512').update(body).digest('base64');
const descriptor = JSON.stringify({ state: 'downloaded', version: '1.2.3', size: body.length, sha256: h256, sha512: h512 }) + '\n';
if (mode === 'oversized') { process.stdout.write('x'.repeat(1025)); process.exit(0); }
if (mode === 'early') { process.stdout.write(Buffer.concat([Buffer.from(descriptor), body]), () => process.exit(0)); return; }
process.stdout.write(descriptor);
let command = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (part) => { command += part; });
process.stdin.on('end', () => {
  if (command !== 'STREAM\n') process.exit(2);
  if (mode === 'hang') return setInterval(() => {}, 1000);
  const sent = mode === 'mutation' ? Buffer.from('X'.repeat(body.length)) : body;
  const trailer = mode === 'missing' ? Buffer.alloc(0) : Buffer.from([mode === 'bad' ? 0 : 1]);
  const extra = mode === 'extra' ? Buffer.from([3]) : Buffer.alloc(0);
  process.stdout.write(Buffer.concat([sent, trailer, extra]), () => process.exit(mode === 'exit' ? 3 : 0));
});
