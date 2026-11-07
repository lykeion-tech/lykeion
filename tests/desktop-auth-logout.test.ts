// @vitest-environment node
import { expect, it, vi } from 'vitest';
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { LogoutIntent } from '../apps/desktop/src/desktop-logout-intent';
import type { Session } from 'electron';
import {
  clearAbandonedLogout,
  finalizeLogoutShutdown,
  recoverLogoutIntent,
  waitForLogoutRecovery,
} from '../apps/desktop/src/desktop-auth-logout';

it('refuses ambiguous same-name cookies without removing them', async () => {
  const remove = vi.fn();
  const session = {
    cookies: {
      get: vi.fn(async () => [
        { hostOnly: true, path: '/', domain: 'lykeion.ia' },
        { hostOnly: false, path: '/', domain: '.lykeion.ia' },
      ]),
      remove,
      flushStore: vi.fn(),
    },
  } as unknown as Session;
  await expect(clearAbandonedLogout(session, 'https://lykeion.ia')).rejects.toThrow('Ambiguous');
  expect(remove).not.toHaveBeenCalled();
});
it('fails if removal does not actually clear the host-only cookie', async () => {
  const session = {
    cookies: {
      get: vi.fn(async () => [{ hostOnly: true, path: '/', domain: 'lykeion.ia' }]),
      remove: vi.fn(async () => {}),
      flushStore: vi.fn(),
    },
  } as unknown as Session;
  await expect(clearAbandonedLogout(session, 'https://lykeion.ia')).rejects.toThrow(
    'could not be cleared',
  );
});
it.each([
  ['domain', { hostOnly: false, path: '/', domain: '.lykeion.ia', value: 'valid-looking' }],
  ['api path', { hostOnly: true, path: '/api', domain: 'lykeion.ia', value: 'valid-looking' }],
  ['ws path', { hostOnly: true, path: '/ws', domain: 'lykeion.ia', value: 'valid-looking' }],
])('refuses a %s session cookie before reopening', async (_name, cookie) => {
  const remove = vi.fn();
  const session = {
    cookies: { get: vi.fn(async () => [cookie]), remove, flushStore: vi.fn() },
  } as unknown as Session;
  await expect(clearAbandonedLogout(session, 'https://lykeion.ia')).rejects.toThrow();
  expect(remove).not.toHaveBeenCalled();
});

it('persists a per-origin logout intent until explicitly cleared', () => {
  const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-intent-'));
  try {
    const first = new LogoutIntent(directory, 'https://lab.example');
    const other = new LogoutIntent(directory, 'https://other.example');
    expect(first.exists()).toBe(false);
    first.mark();
    expect(first.exists()).toBe(true);
    expect(other.exists()).toBe(false);
    expect(readFileSync(first.path, 'utf8')).not.toContain('lab.example');
    expect(new LogoutIntent(directory, 'https://lab.example').exists()).toBe(true);
    first.clear();
    expect(first.exists()).toBe(false);
    first.clear();
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

it('rejects a symlinked marker without touching its target', () => {
  const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-intent-'));
  try {
    const intent = new LogoutIntent(directory, 'https://lab.example');
    intent.mark();
    rmSync(intent.path);
    const target = join(directory, 'unrelated');
    writeFileSync(target, 'preserve');
    symlinkSync(target, intent.path);
    expect(() => intent.exists()).toThrow();
    expect(() => intent.mark()).toThrow();
    expect(() => intent.clear()).toThrow();
    expect(readFileSync(target, 'utf8')).toBe('preserve');
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

it.skipIf(process.platform === 'win32')(
  'retains logout intent when its process is killed after admission',
  () => {
    const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-crash-'));
    try {
      const moduleURL = pathToFileURL(
        join(process.cwd(), 'apps/desktop/src/desktop-logout-intent.ts'),
      );
      const child = spawnSync(
        process.execPath,
        [
          '--experimental-strip-types',
          '--input-type=module',
          '-e',
          `import { LogoutIntent } from ${JSON.stringify(moduleURL.href)}; new LogoutIntent(process.argv[1], 'https://lab.example').mark(); process.kill(process.pid, 'SIGKILL');`,
          directory,
        ],
        { encoding: 'utf8' },
      );
      expect(child.signal).toBe('SIGKILL');
      expect(new LogoutIntent(directory, 'https://lab.example').exists()).toBe(true);
      expect(new LogoutIntent(directory, 'https://other.example').exists()).toBe(false);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  },
);

it('keeps the intent until its selected cookie is cleared, verified and flushed', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-recovery-'));
  try {
    const intent = new LogoutIntent(directory, 'https://lab.example');
    intent.mark();
    let present = true;
    const flush = vi
      .fn()
      .mockRejectedValueOnce(new Error('flush failed'))
      .mockResolvedValue(undefined);
    const session = {
      cookies: {
        get: vi.fn(async () =>
          present ? [{ hostOnly: true, path: '/', domain: 'lab.example' }] : [],
        ),
        remove: vi.fn(async () => {
          present = false;
        }),
        flushStore: flush,
      },
    } as unknown as Session;
    await expect(recoverLogoutIntent(intent, session, 'https://lab.example')).rejects.toThrow(
      'flush failed',
    );
    expect(intent.exists()).toBe(true);
    await recoverLogoutIntent(intent, session, 'https://lab.example');
    expect(intent.exists()).toBe(false);
    expect(flush).toHaveBeenCalledTimes(2);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

it('does not flush or remove an intent while startup recovery is still running', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-quit-'));
  try {
    const intent = new LogoutIntent(directory, 'https://lab.example');
    intent.mark();
    let finish!: () => void;
    const pending = new Promise<void>((resolve) => {
      finish = resolve;
    });
    const flushStore = vi.fn(async () => {});
    const session = { cookies: { flushStore } } as unknown as Session;
    const shutdown = finalizeLogoutShutdown(pending, session, intent);
    await Promise.resolve();
    expect(intent.exists()).toBe(true);
    expect(flushStore).not.toHaveBeenCalled();
    finish();
    await shutdown;
    expect(intent.exists()).toBe(false);
    expect(flushStore).toHaveBeenCalledOnce();
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

it('times out startup recovery without clearing its marker', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'lyk-logout-timeout-'));
  try {
    const intent = new LogoutIntent(directory, 'https://lab.example');
    intent.mark();
    await expect(waitForLogoutRecovery(new Promise<void>(() => {}), 10)).rejects.toThrow(
      'did not finish',
    );
    expect(intent.exists()).toBe(true);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
