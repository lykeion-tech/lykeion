import { app, BrowserWindow, session } from 'electron';
import http from 'node:http';
import crypto from 'node:crypto';
import zlib from 'node:zlib';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
const root = process.cwd();
const userData = fs.mkdtempSync(path.join(os.tmpdir(), 'lyk-electron-profile-'));
app.setPath('userData', userData);
app.whenReady().then(async () => {
  const route = await import('file://' + root + '/apps/desktop/out/renderer-origin.js');
  let wsOrigin = '';
  let apiOrigin = '';
  let foreignRequests = 0;
  const server = http.createServer((q, s) => {
    apiOrigin = q.headers.origin || apiOrigin;
    if (q.url === '/api/gzip') {
      s.writeHead(200, { 'Content-Encoding': 'gzip' });
      s.end(zlib.gzipSync('compressed-fixture'));
    } else if (q.url === '/api/redirect-out') {
      s.writeHead(307, { Location: `http://localhost:${server.address().port}/api/collect` });
      s.end();
    } else if (q.url === '/api/collect') {
      foreignRequests++;
      s.end('unexpected');
    } else if (q.url === '/api/upload') {
      let n = 0;
      q.on('data', (x) => (n += x.length));
      q.on('end', () => s.end(String(n)));
    } else if (q.url === '/auth/login') {
      s.setHeader('Set-Cookie', 'lyk=test; Path=/; HttpOnly; SameSite=Lax');
      s.end('ok');
    } else if (q.url === '/auth/redirect') {
      s.writeHead(302, { Location: '/api/file' });
      s.end();
    } else if (q.url === '/api/script.js') {
      s.setHeader('Content-Type', 'text/javascript');
      s.end('window.pwned=true');
    } else if (q.url.startsWith('/api/file')) {
      s.setHeader('Content-Type', 'application/octet-stream');
      if (q.url.includes('download=1'))
        s.setHeader('Content-Disposition', 'attachment; filename="fixture.bin"');
      s.end(q.headers.cookie === 'lyk=test' ? 'coresearch-fixture' : 'unauthorized');
    } else {
      s.writeHead(401, { 'Content-Type': 'application/json' });
      s.end('{"error":"not signed in"}');
    }
  });
  server.on('upgrade', (q, s) => {
    wsOrigin = q.headers.origin;
    const accept = crypto
      .createHash('sha1')
      .update(q.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11')
      .digest('base64');
    s.write(
      'HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' +
        accept +
        '\r\n\r\n',
    );
  });
  server.listen(0, '127.0.0.1', async () => {
    const origin = 'http://127.0.0.1:' + server.address().port;
    const alternate = http.createServer();
    let foreignWebSockets = 0;
    alternate.on('upgrade', (_q, socket) => {
      foreignWebSockets++;
      socket.destroy();
    });
    await new Promise((resolve) => alternate.listen(0, '127.0.0.1', resolve));
    const currentSession = session.fromPartition(route.desktopPartition(origin));
    const otherSession = session.fromPartition(
      route.desktopPartition('http://127.0.0.1:' + alternate.address().port),
    );
    const domainOne = session.fromPartition(route.desktopPartition('https://lab.example'));
    const domainTwo = session.fromPartition(route.desktopPartition('https://sub.lab.example'));
    await domainOne.cookies.set({
      url: 'https://lab.example',
      name: 'separate',
      value: 'secret',
      domain: '.lab.example',
      secure: true,
    });
    route.installDesktopRenderer(currentSession, origin, root + '/dist/dashboard');
    const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'lyk-download-'));
    const downloaded = new Promise((resolve) => {
      const timer = setTimeout(() => resolve(false), 2000);
      currentSession.once('will-download', (_event, item) => {
        item.setSavePath(path.join(temp, 'fixture.bin'));
        item.once('done', (_done, state) => {
          clearTimeout(timer);
          resolve(
            state === 'completed' &&
              fs.readFileSync(path.join(temp, 'fixture.bin'), 'utf8') === 'coresearch-fixture',
          );
        });
      });
    });
    let w = new BrowserWindow({
      show: false,
      webPreferences: {
        session: currentSession,
        sandbox: true,
        contextIsolation: true,
        nodeIntegration: false,
      },
    });
    try {
      await w.loadURL(route.desktopEntryURL(origin));
      let r = await w.webContents.executeJavaScript(
        `(async()=>{await fetch('/auth/login',{method:'POST',credentials:'same-origin'});let file=await (await fetch('/auth/redirect',{credentials:'same-origin'})).text();let compressed=await (await fetch('/api/gzip')).text();let uploaded=await (await fetch('/api/upload',{method:'POST',body:new Uint8Array(1048576)})).text();let redirected=await fetch('/api/redirect-out',{method:'POST',body:'private'}).then(()=> 'allowed',()=> 'blocked');await new Promise(resolve=>{let x=document.createElement('script');x.src='/api/script.js';x.onload=x.onerror=resolve;document.head.appendChild(x)});let sw=await navigator.serviceWorker.register('/api/script.js').then(()=> 'registered',()=> 'blocked');let worker=await new Promise(resolve=>{let w=new Worker('/api/script.js');w.onmessage=()=>resolve('executed');w.onerror=()=>resolve('blocked');setTimeout(()=>resolve('timeout'),1000)});let ws=await new Promise(resolve=>{let s=new WebSocket('ws://127.0.0.1:${server.address().port}/ws');s.onopen=()=>{s.close();resolve('open')};s.onerror=()=>resolve('error');setTimeout(()=>resolve('timeout'),2500)});return {title:document.title,origin:location.origin,file,compressed,uploaded,redirected,ws,sw,worker,pwned:!!window.pwned,body:document.body.textContent.slice(0,150)} })()`,
      );
      await w.webContents.executeJavaScript(
        "(()=>{let a=document.createElement('a');a.href='/api/file?download=1';a.download='';a.click()})()",
      );
      const download = await downloaded;
      const sameHostCookieLeak = (await otherSession.cookies.get({ url: origin, name: 'lyk' }))
        .length;
      const relatedDomainCookieLeak = (
        await domainTwo.cookies.get({ url: 'https://sub.lab.example', name: 'separate' })
      ).length;
      const foreignWs = await w.webContents.executeJavaScript(
        `new Promise(resolve=>{let ws=new WebSocket('ws://127.0.0.1:${alternate.address().port}/ws');ws.onopen=()=>{ws.close();resolve('open')};ws.onerror=()=>resolve('blocked');setTimeout(()=>resolve('timeout'),1500)})`,
      );
      console.log(
        JSON.stringify({
          ...r,
          apiOrigin,
          wsOrigin,
          foreignRequests,
          download,
          foreignWs,
          foreignWebSockets,
          sameHostCookieLeak,
          relatedDomainCookieLeak,
        }),
      );
      if (
        !r.title.includes('Lykeion') ||
        r.pwned ||
        r.sw !== 'blocked' ||
        r.worker !== 'blocked' ||
        r.redirected !== 'blocked' ||
        foreignRequests !== 0 ||
        !download ||
        foreignWs !== 'blocked' ||
        foreignWebSockets !== 0 ||
        sameHostCookieLeak !== 0 ||
        relatedDomainCookieLeak !== 0 ||
        r.compressed !== 'compressed-fixture' ||
        r.uploaded !== '1048576' ||
        r.file !== 'coresearch-fixture' ||
        r.ws !== 'open' ||
        wsOrigin !== origin
      )
        process.exitCode = 1;
    } catch (e) {
      console.error(e);
      process.exitCode = 1;
    } finally {
      w.destroy();
      server.close();
      alternate.close();
      fs.rmSync(temp, { recursive: true, force: true });
      fs.rmSync(userData, { recursive: true, force: true });
      app.exit(process.exitCode || 0);
    }
  });
});
