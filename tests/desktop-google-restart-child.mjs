import { app, BrowserWindow, shell } from 'electron';
import assert from 'node:assert/strict';
import { join } from 'node:path';
const temporary = process.env.LYK_GOOGLE_FIXTURE_HOME;
const origin = process.env.LYK_GOOGLE_FIXTURE_ORIGIN;
const phase = process.env.LYK_GOOGLE_FIXTURE_PHASE;
app.setPath('appData', join(temporary, 'appdata'));
delete process.env.LYKEION_RENDERER_URL;
shell.openExternal = async (url) => {
  await fetch(url);
};
async function until(fn, msg) {
  for (let i = 0; i < 200; i++) {
    if (await fn()) return;
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error('Timed out: ' + msg);
}
async function run() {
  try {
    await import('../apps/desktop/out/main.js');
    await until(() => BrowserWindow.getAllWindows().length > 0, 'window');
    const win = BrowserWindow.getAllWindows()[0];
    await until(
      async () =>
        win.webContents
          .executeJavaScript('Boolean(window.lykeionDesktop?.requestDocument)')
          .catch(() => false),
      'document',
    );
    if (phase === 'first') {
      await until(
        async () =>
          win.webContents
            .executeJavaScript(
              "[...document.querySelectorAll('button')].some(x=>x.textContent.includes('Continue with Google'))",
            )
            .catch(() => false),
        'Google button',
      );
      await win.webContents.executeJavaScript(
        "[...document.querySelectorAll('button')].find(x=>x.textContent.includes('Continue with Google')).click()",
      );
      await until(
        async () => (await fetch(origin + '/control')).status === 200,
        'pending exchange',
      );
      app.quit();
    } else {
      assert.equal(
        await win.webContents.executeJavaScript('window.lykeionDesktop.verificationPending'),
        true,
      );
      await until(
        async () =>
          win.webContents
            .executeJavaScript(
              '!document.querySelector(\'dialog[aria-label=\\"Verify your account\\"]\')',
            )
            .catch(() => false),
        'recovery',
      );
      assert.equal((await win.webContents.session.cookies.get({ name: 'lyk_session' })).length, 0);
      console.log('restart recovered interrupted Google sign-in without session');
      app.quit();
    }
  } catch (err) {
    console.error(err);
    app.exit(1);
  }
}
void run();
