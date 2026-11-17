// @vitest-environment node
import { mkdtempSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { expect, it, vi } from 'vitest';
vi.mock('electron', () => ({ Notification: { isSupported: () => false } }));
import { startDesktopUpdater } from '../apps/desktop/src/desktop-updater';

it('reports whether a check found nothing, downloaded a version, or failed', async () => {
  const listeners: Record<string, (value: { version: string }) => void> = {};
  const updater = {
    on: (event: string, listener: (value: { version: string }) => void) =>
      (listeners[event] = listener),
    checkForUpdates: vi.fn(async () => null as { downloadPromise?: Promise<unknown> } | null),
  };
  const directory = mkdtempSync(path.join(os.tmpdir(), 'lyk-updater-'));
  writeFileSync(
    path.join(directory, 'electron-updater.cjs'),
    'module.exports = { autoUpdater: globalThis.__testUpdater };',
  );
  Object.assign(globalThis, { __testUpdater: updater });
  vi.spyOn(console, 'error').mockImplementation(() => {});
  const { stop, check } = startDesktopUpdater(directory, false);
  stop();

  await expect(check()).resolves.toEqual({ state: 'current' });

  updater.checkForUpdates.mockImplementationOnce(async () => ({
    downloadPromise: Promise.resolve().then(() =>
      listeners['update-downloaded']!({ version: '0.1.2' }),
    ),
  }));
  await expect(check()).resolves.toEqual({ state: 'downloaded', version: '0.1.2' });

  updater.checkForUpdates.mockRejectedValueOnce(new Error('offline'));
  await expect(check()).resolves.toEqual({ state: 'error' });
});
