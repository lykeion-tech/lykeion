// @vitest-environment node
import { expect, it, vi } from 'vitest';
import type { Session } from 'electron';
import { clearAbandonedLogout } from '../apps/desktop/src/desktop-auth-logout';

it('refuses ambiguous same-name cookies without removing them', async () => {
  const remove = vi.fn();
  const session = {
    cookies: {
      get: vi.fn(async () => [
        { hostOnly: true, path: '/' },
        { hostOnly: false, path: '/' },
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
      get: vi.fn(async () => [{ hostOnly: true, path: '/' }]),
      remove: vi.fn(async () => {}),
      flushStore: vi.fn(),
    },
  } as unknown as Session;
  await expect(clearAbandonedLogout(session, 'https://lykeion.ia')).rejects.toThrow(
    'could not be cleared',
  );
});
