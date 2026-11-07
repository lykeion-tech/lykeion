import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  advanceAuthenticationEpoch,
  authenticationEpoch,
  authenticationRestored,
  verification,
} from '../api/auth-loss';
import { createDashboardQueryClient } from '../api/query-client';
import { createMemoryHistory, createRouter } from 'vue-router';
import { api, ApiError } from '../api/client';
import { createDaemonSession } from './daemon-session';
import type { DesktopDaemonBridge } from '../../../desktop/src/daemon-types';
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}
const disposers: (() => void)[] = [];
afterEach(() => {
  disposers.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
});
function setup(bridgeEnabled = true) {
  const bridge = {
    configureSession: vi.fn().mockResolvedValue({}),
    clearSession: vi.fn().mockResolvedValue({}),
    getSnapshot: vi.fn().mockResolvedValue({ availability: 'available' }),
  } as unknown as DesktopDaemonBridge;
  const token = vi.fn().mockResolvedValue({ token: 'ephemeral' });
  const reportError = vi.fn();
  const onConfigured = vi.fn();
  const session = createDaemonSession({
    bridge: bridgeEnabled ? bridge : undefined,
    apiOrigin: 'http://127.0.0.1:8080',
    token,
    reportError,
    onConfigured,
  });
  disposers.push(session.dispose);
  return {
    bridge,
    token,
    reportError,
    onConfigured,
    session,
  };
}
describe('desktop daemon authenticated session', () => {
  it('does not mint or clear daemon credentials when capability is unavailable', async () => {
    const t = setup();
    vi.mocked(t.bridge.getSnapshot).mockResolvedValue({ availability: 'unavailable' } as never);
    await t.session.update('alice');
    await t.session.clear();
    expect(t.token).not.toHaveBeenCalled();
    expect(t.bridge.configureSession).not.toHaveBeenCalled();
    expect(t.bridge.clearSession).toHaveBeenCalledTimes(1);
  });
  it('hands off an ephemeral token once per authenticated identity', async () => {
    const t = setup();
    await t.session.update('alice');
    await t.session.update('alice');
    expect(t.bridge.configureSession).toHaveBeenCalledExactlyOnceWith({
      apiOrigin: 'http://127.0.0.1:8080',
      userID: 'alice',
      sessionToken: 'ephemeral',
    });
    expect(t.token).toHaveBeenCalledTimes(1);
    expect(t.onConfigured).toHaveBeenCalledTimes(1);
  });
  it('does not request tokens in a browser or clear while auth is initially pending', async () => {
    const browser = setup(false);
    await browser.session.update('alice');
    await browser.session.clear();
    expect(browser.token).not.toHaveBeenCalled();
    const t = setup();
    await t.session.update(undefined);
    expect(t.bridge.clearSession).not.toHaveBeenCalled();
    await t.session.update(undefined, true);
    expect(t.bridge.clearSession).toHaveBeenCalledTimes(1);
  });
  it.each(['logout', '401', 'switch', 'dispose'])('fences pending JWT on %s', async (action) => {
    const t = setup();
    const jwt = deferred<{ token: string }>();
    t.token.mockReturnValueOnce(jwt.promise);
    const old = t.session.update('alice');
    await vi.waitFor(() => expect(t.token).toHaveBeenCalledTimes(1));
    if (action === 'logout') await t.session.clear();
    else if (action === '401') await t.session.update('alice', true);
    else if (action === 'switch') await t.session.update('bob');
    else t.session.dispose();
    jwt.resolve({ token: 'alice-token' });
    await old;
    expect(t.bridge.configureSession).not.toHaveBeenCalledWith(
      expect.objectContaining({ userID: 'alice' }),
    );
    if (action === 'switch') {
      expect(t.bridge.clearSession).toHaveBeenCalledTimes(1);
      expect(t.bridge.configureSession).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({ userID: 'bob' }),
      );
    }
  });
  it('resumes a pending same-owner handoff without waiting for its stale token', async () => {
    const t = setup();
    const old = deferred<{ token: string }>();
    t.token.mockReturnValueOnce(old.promise);
    const update = t.session.update('a');
    await vi.waitFor(() => expect(t.token).toHaveBeenCalledOnce());
    verification.value = { epoch: authenticationEpoch(), stage: 'checking', accepted: false };
    verification.value = undefined;
    await t.session.update('a');
    old.resolve({ token: 'old' });
    await update;
    expect(t.bridge.configureSession).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ userID: 'a', sessionToken: 'ephemeral' }),
    );
    expect(t.bridge.clearSession).not.toHaveBeenCalled();
  });
  it('fences the previous pending token and waits for the native clear before configuring B', async () => {
    const t = setup();
    const jwt = deferred<{ token: string }>();
    const clear = deferred<Awaited<ReturnType<DesktopDaemonBridge['clearSession']>>>();
    t.token.mockReturnValueOnce(jwt.promise);
    vi.mocked(t.bridge.clearSession).mockReturnValueOnce(clear.promise);
    const updateA = t.session.update('a');
    await vi.waitFor(() => expect(t.token).toHaveBeenCalledTimes(1));
    verification.value = { epoch: authenticationEpoch(), stage: 'checking', accepted: true };
    try {
      const fence = t.session.clearForIdentityChange();
      jwt.resolve({ token: 'stale' });
      await updateA;
      await t.session.update('b');
      expect(t.bridge.configureSession).not.toHaveBeenCalled();
      expect(t.token).toHaveBeenCalledTimes(1);
      clear.resolve({} as never);
      await fence;
    } finally {
      verification.value = undefined;
    }
    await t.session.update('b');
    expect(t.bridge.configureSession).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ userID: 'b' }),
    );
  });
  it('preserves a restored identity across pending/transient auth refreshes', async () => {
    const t = setup();
    await t.session.update('alice');
    await t.session.update(undefined);
    await t.session.update('alice');
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(1);
    expect(t.bridge.clearSession).not.toHaveBeenCalled();
  });
  it('does not configure another account after a failed clear and surfaces safe errors', async () => {
    const t = setup();
    await t.session.update('alice');
    vi.mocked(t.bridge.clearSession).mockRejectedValue(new Error('secret-token'));
    await t.session.update('bob');
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(1);
    expect(t.reportError).toHaveBeenCalledWith(expect.stringContaining('could not be stopped'));
    expect(JSON.stringify(t.reportError.mock.calls)).not.toContain('secret-token');
    await expect(t.session.clear()).rejects.toThrow('could not be stopped');
  });
  it('makes token failures visible without exposing token response details', async () => {
    const t = setup();
    t.token.mockRejectedValue(new Error('secret-token'));
    await t.session.update('alice');
    expect(t.bridge.configureSession).not.toHaveBeenCalled();
    expect(t.reportError).toHaveBeenCalledWith(expect.stringContaining('could not be configured'));
    expect(JSON.stringify(t.reportError.mock.calls)).not.toContain('secret-token');
  });
  it('retries a failed same-account handoff but never repeats a successful handoff', async () => {
    const t = setup();
    t.token.mockRejectedValueOnce(new Error('temporary network outage'));
    await t.session.update('alice');
    expect(t.bridge.configureSession).not.toHaveBeenCalled();
    await t.session.update('alice');
    await t.session.update('alice');
    expect(t.token).toHaveBeenCalledTimes(2);
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(1);
    expect(t.bridge.clearSession).not.toHaveBeenCalled();
  });
  it('global QueryCache read401 fences a pending token and clears native session before redirect', async () => {
    const t = setup();
    const jwt = deferred<{ token: string }>();
    t.token.mockReturnValueOnce(jwt.promise);
    const old = t.session.update('alice');
    await Promise.resolve();
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
    });
    await router.push('/settings');
    const client = createDashboardQueryClient(router);
    await expect(
      client.fetchQuery({
        queryKey: ['runtimes'],
        queryFn: async () => {
          throw new ApiError(401, 'signed out');
        },
      }),
    ).rejects.toThrow('signed out');
    expect(t.bridge.clearSession).toHaveBeenCalledTimes(1);
    jwt.resolve({ token: 'alice-old-token' });
    await old;
    expect(t.bridge.configureSession).not.toHaveBeenCalled();
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/signin'));
    client.clear();
  });

  it('ignores a prior account query 401 after an identity switch', async () => {
    const t = setup();
    const response = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(response.promise));
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
    });
    await router.push('/settings');
    const client = createDashboardQueryClient(router);
    const old = client.fetchQuery({
      queryKey: ['old-account'],
      queryFn: () => api('GET', '/api/me'),
    });
    await Promise.resolve();
    advanceAuthenticationEpoch();
    response.resolve(new Response('{"error":"old session"}', { status: 401 }));
    await expect(old).rejects.toThrow('old session');
    expect(t.bridge.clearSession).not.toHaveBeenCalled();
    expect(router.currentRoute.value.path).toBe('/settings');
    client.clear();
  });

  it('fences and clears a native session on a cli-token POST401', async () => {
    const t = setup();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('{"error":"signed out"}', { status: 401 })),
    );
    t.token.mockImplementationOnce(() => api('POST', '/api/cli-token'));
    await t.session.update('alice');
    expect(t.bridge.clearSession).toHaveBeenCalledTimes(1);
    expect(t.bridge.configureSession).not.toHaveBeenCalled();
  });
  it.each(['logout', '401'])(
    'rejects stale same-account query refreshes after %s',
    async (reason) => {
      const t = setup();
      await t.session.update('alice');
      if (reason === 'logout') await t.session.clear();
      else await t.session.update(undefined, true);
      await t.session.update('alice');
      await t.session.update(undefined);
      await t.session.update('alice');
      expect(t.bridge.configureSession).toHaveBeenCalledTimes(1);
      expect(t.token).toHaveBeenCalledTimes(1);
    },
  );

  it('allows a deliberate fresh sign-in after logout while preserving a failed-clear barrier', async () => {
    const t = setup();
    await t.session.update('alice');
    await t.session.clear();
    authenticationRestored();
    await t.session.update('bob');
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(2);
    vi.mocked(t.bridge.clearSession).mockRejectedValueOnce(new Error('stop failed'));
    await expect(t.session.clear()).rejects.toThrow('could not be stopped');
    authenticationRestored();
    await t.session.update('charlie');
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(2);
  });
  it('ignores late API401 from a canceled read started before deliberate fresh sign-in', async () => {
    const t = setup();
    await t.session.update('alice');
    const response = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(response.promise));
    const old = api('GET', '/api/runtimes');
    await t.session.clear();
    authenticationRestored();
    await t.session.update('bob');
    response.resolve(new Response('{"error":"old session"}', { status: 401 }));
    await expect(old).rejects.toThrow('old session');
    expect(t.bridge.clearSession).toHaveBeenCalledTimes(1);
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(2);
    await t.session.update('bob');
    expect(t.bridge.configureSession).toHaveBeenCalledTimes(2);
  });
});
