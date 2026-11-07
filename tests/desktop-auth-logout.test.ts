// @vitest-environment node
import { expect, it, vi } from 'vitest';
import type { Session } from 'electron';
import { clearAbandonedLogout } from '../apps/desktop/src/desktop-auth-logout';

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
