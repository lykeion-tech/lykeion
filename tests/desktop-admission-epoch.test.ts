// @vitest-environment node
import { expect, it, vi } from 'vitest';
import type { Session } from 'electron';
vi.mock('electron', () => ({ net: {} }));
import { installDesktopRenderer } from '../apps/desktop/src/renderer-origin';

it('rejects an old-cookie send event delivered after a later recovery epoch', async () => {
  type Listener = (
    details: Record<string, unknown>,
    done: (result: Record<string, unknown>) => void,
  ) => void;
  let before!: Listener;
  let headers!: Listener;
  const session = {
    webRequest: {
      onBeforeRequest: (_filter: unknown, callback: typeof before) => {
        before = callback;
      },
      onBeforeSendHeaders: (callback: typeof headers) => {
        headers = callback;
      },
      onCompleted: () => {},
      onErrorOccurred: () => {},
    },
    protocol: { handle: () => {} },
  } as unknown as Session;
  let epoch = 0;
  installDesktopRenderer(session, 'https://lykeion.ia', '/tmp', {
    continue: () => false,
    finish: () => {},
    networkReady: async () => {},
    networkBlocked: () => false,
    networkEpoch: () => epoch,
  });
  const request = { id: 7, url: 'https://lykeion.ia/api/me', method: 'GET', resourceType: 'xhr' };
  const admission = await new Promise((resolve) => before(request, resolve));
  expect(admission).toEqual({ cancel: false });
  epoch++;
  const result = await new Promise((resolve) =>
    headers({ ...request, requestHeaders: { Cookie: 'lyk_session=old' } }, resolve),
  );
  expect(result).toEqual({ cancel: true });
});
