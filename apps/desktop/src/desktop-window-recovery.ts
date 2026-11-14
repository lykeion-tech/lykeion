import type { BrowserWindow } from 'electron';
import { desktopEntryURL } from './renderer-origin.js';

export function loadDesktopWindow(
  owner: BrowserWindow,
  origin: string,
  isCurrent: () => boolean,
  askRetry: (signal: AbortSignal) => Promise<boolean>,
) {
  const entry = desktopEntryURL(origin);
  let revision = 0;
  let failed = false;
  let prompt: AbortController | undefined;
  function pause() {
    prompt?.abort();
    prompt = undefined;
  }
  function reset() {
    revision++;
    failed = false;
    pause();
  }
  function load() {
    const attempt = ++revision;
    void owner.loadURL(entry).catch((error: { code?: string; errno?: number }) => {
      if (attempt === revision && error?.code !== 'ERR_ABORTED' && error?.errno !== -3)
        reportFailure();
    });
  }
  function reportFailure() {
    revision++; // A load rejection following this crash belongs to the same incident.
    failed = true;
    void offerRetry();
  }
  async function offerRetry() {
    if (!failed || prompt || !isCurrent()) return;
    const controller = new AbortController();
    prompt = controller;
    try {
      const retry = await askRetry(controller.signal);
      if (controller.signal.aborted || !isCurrent()) return;
      failed = false;
      if (retry) load();
    } catch {
      // A native sheet can disappear with its parent window.
    } finally {
      if (prompt === controller) prompt = undefined;
    }
  }
  owner.on('close', reset);
  owner.on('closed', reset);
  owner.webContents.on('render-process-gone', (_event, details) => {
    if (details.reason !== 'clean-exit') reportFailure();
  });
  owner.webContents.on('did-navigate', (_event, url, status) => {
    if (failed && status >= 200 && status < 300 && url.split('#')[0] === entry) reset();
  });
  load();
  return { pause, resume: offerRetry };
}
