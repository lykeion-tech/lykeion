import { app, BrowserWindow, session } from 'electron';
import http from 'node:http';
const root = process.cwd();
app.whenReady().then(async () => {
  const route = await import('file://' + root + '/apps/desktop/out/renderer-origin.js');
  const auth = await import('file://' + root + '/apps/desktop/out/google-sign-in.js');
  const server = http.createServer((q, s) => {
    if (q.url === '/auth/google') s.writeHead(302, { Location: '/provider' }).end();
    else if (q.url === '/provider')
      s.writeHead(302, { Location: '/auth/google/callback?code=fixture' }).end();
    else if (q.url.startsWith('/auth/google/callback'))
      s.writeHead(302, {
        'Set-Cookie': 'lyk_session=oauth-fixture; Path=/; HttpOnly; SameSite=Lax',
        Location: '/',
      }).end();
    else s.end('ok');
  });
  server.listen(0, '127.0.0.1', async () => {
    let origin = 'http://127.0.0.1:' + server.address().port;
    route.installDesktopRenderer(session.defaultSession, origin, root + '/dist/dashboard');
    const w = new BrowserWindow({
      show: false,
      webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false },
    });
    await w.loadURL(route.desktopEntryURL(origin));
    auth.openGoogleSignIn(w, origin);
    setTimeout(async () => {
      let cookies = await session.defaultSession.cookies.get({ url: origin, name: 'lyk_session' });
      console.log(
        JSON.stringify({
          transferred: cookies.some((c) => c.value === 'oauth-fixture'),
          cookies: cookies.length,
        }),
      );
      w.destroy();
      server.close();
      app.exit(cookies.some((c) => c.value === 'oauth-fixture') ? 0 : 1);
    }, 2500);
  });
});
