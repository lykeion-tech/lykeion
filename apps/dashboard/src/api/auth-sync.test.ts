import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.resetModules();
  vi.unstubAllGlobals();
});

it('notifies another window without transmitting identities or rebroadcasting', async () => {
  const channels: FakeChannel[] = [];
  const sent: unknown[] = [];
  class FakeChannel {
    onmessage: ((event: { data: unknown }) => void) | null = null;
    constructor(readonly name: string) {
      channels.push(this);
    }
    postMessage(data: unknown) {
      sent.push(data);
      for (const other of channels)
        if (other !== this && other.name === this.name) other.onmessage?.({ data });
    }
    close() {}
  }
  vi.stubGlobal('window', { BroadcastChannel: FakeChannel });
  const a = await import('./auth-sync');
  vi.resetModules();
  const b = await import('./auth-sync');
  const receivedA = vi.fn();
  const receivedB = vi.fn();
  const offA = a.onExternalAuthenticationChanged(receivedA);
  const offB = b.onExternalAuthenticationChanged(receivedB);
  a.announceAuthenticationChanged();
  expect(receivedA).not.toHaveBeenCalled();
  expect(receivedB).toHaveBeenCalledTimes(1);
  expect(sent).toEqual(['changed']);
  expect(channels).toHaveLength(2);
  offA();
  offB();
});

it('announces successful login and logout but not failed authentication', async () => {
  const sent: unknown[] = [];
  class FakeChannel {
    onmessage = null;
    postMessage(data: unknown) {
      sent.push(data);
    }
    close() {}
  }
  vi.stubGlobal('window', { BroadcastChannel: FakeChannel });
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(new Response(null, { status: 204 }))
    .mockResolvedValueOnce(new Response(null, { status: 400 }))
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./client');
  await api('POST', '/auth/verify-code', {});
  await expect(api('POST', '/auth/verify-code', {})).rejects.toThrow();
  await api('POST', '/auth/logout');
  expect(sent).toEqual(['changed', 'changed']);
});

it('falls back to storage events when broadcast channels are unavailable', async () => {
  let receiver: ((event: { key: string; newValue: string }) => void) | undefined;
  const write = vi.fn();
  vi.stubGlobal('window', {
    addEventListener: (_event: string, listener: typeof receiver) => {
      receiver = listener;
    },
    removeEventListener: () => {},
    localStorage: { setItem: write },
  });
  const sync = await import('./auth-sync');
  const received = vi.fn();
  const off = sync.onExternalAuthenticationChanged(received);
  sync.announceAuthenticationChanged();
  expect(write.mock.calls[0]?.[0]).toBe('lykeion.auth-changed');
  receiver?.({ key: 'other', newValue: '1' });
  receiver?.({ key: 'lykeion.auth-changed', newValue: '1' });
  expect(received).toHaveBeenCalledTimes(1);
  off();
});

it('announces a returned Google browser flow once without storing account data', async () => {
  const data = new Map<string, string>();
  const sent: unknown[] = [];
  class FakeChannel {
    onmessage = null;
    postMessage(value: unknown) {
      sent.push(value);
    }
    close() {}
  }
  vi.stubGlobal('window', {
    BroadcastChannel: FakeChannel,
    sessionStorage: {
      setItem: (key: string, value: string) => data.set(key, value),
      getItem: (key: string) => data.get(key) ?? null,
      removeItem: (key: string) => data.delete(key),
    },
  });
  const sync = await import('./auth-sync');
  sync.markGoogleAuthenticationPending();
  expect([...data.values()]).toEqual(['1']);
  sync.announceReturnedGoogleAuthentication();
  sync.announceReturnedGoogleAuthentication();
  expect(sent).toEqual(['changed']);
  expect(data.size).toBe(0);
});
