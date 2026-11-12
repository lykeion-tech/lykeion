// @vitest-environment node
import { expect, it, vi } from 'vitest';
import { DesktopVerification } from '../apps/desktop/src/desktop-verification';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';

it('persists logout before issuing its reservation and rejects storage failure', () => {
  const gate = new DesktopAuthGate(() => {
    throw new Error('storage failure');
  });
  expect(() => gate.reserve('/auth/logout')).toThrow('storage failure');
  expect(gate.reserve('/auth/verify-code')).toBeTruthy();
});

it('holds later verification until logout has drained older writes and recovered', async () => {
  const gate = new DesktopAuthGate();
  const older = gate.reserve('/auth/verify-code')!;
  expect(gate.continueMutation(-1, older, '/auth/verify-code')).toBe(true);
  const logout = gate.reserve('/auth/logout')!;
  expect(gate.reserve('/auth/verify-code')).toBeNull();
  expect(gate.continueMutation(-2, logout, '/auth/logout')).toBe(true);
  let logoutReady = false;
  const turn = gate.waitForLogoutTurn(-2).then(() => {
    logoutReady = true;
  });
  await Promise.resolve();
  expect(logoutReady).toBe(false);
  gate.finishMutation(-1);
  await turn;
  expect(logoutReady).toBe(true);
  gate.finishMutation(-2);
  gate.beginLogoutRecovery();
  let cleaned = false;
  const recovery = gate.recoverAbandonedLogout(async () => {
    cleaned = true;
  });
  await Promise.resolve();
  await recovery;
  expect(cleaned).toBe(true);
  expect(gate.reserve('/auth/verify-code')).toBeTruthy();
});

it('transfers only an admitted, path-bound request across shutdown', async () => {
  const gate = new DesktopAuthGate();
  const token = gate.reserve('/auth/logout')!;
  let finished = false;
  const shutdown = gate
    .shutdown(async () => {
      throw new Error('daemon failed');
    })
    .then((result) => {
      finished = true;
      return result;
    });
  expect(gate.reserve('/auth/logout')).toBeNull();
  expect(gate.continueMutation(-1, token, '/auth/verify-code')).toBe(false);
  expect(gate.continueMutation(-1, token, '/auth/logout')).toBe(true);
  expect(gate.continueMutation(-2, token, '/auth/logout')).toBe(false);
  await Promise.resolve();
  expect(finished).toBe(false);
  gate.finishMutation(-1);
  const result = await shutdown;
  expect(result[0]?.status).toBe('fulfilled');
  expect(result[1]?.status).toBe('rejected');
});

it('bounds stalled shutdown and retries without admitting new requests', async () => {
  const gate = new DesktopAuthGate();
  const token = gate.reserve('/auth/logout')!;
  expect((await gate.shutdown(async () => {}, 20))[0]?.status).toBe('rejected');
  expect(gate.reserve('/auth/logout')).toBeNull();
  gate.release(token);
  expect(
    (
      await gate.shutdown(
        async () => {},
        20,
        async () => {},
      )
    )[0]?.status,
  ).toBe('fulfilled');
});

it('invalidates abandoned logout and clears only after claimed writes finish', async () => {
  const gate = new DesktopAuthGate();
  const active = gate.reserve('/auth/verify-code')!;
  const stale = gate.reserve('/auth/logout')!;
  expect(gate.continueMutation(-1, active, '/auth/verify-code')).toBe(true);
  gate.abandonUnclaimed();
  expect(gate.continueMutation(-2, stale, '/auth/logout')).toBe(false);
  let cleared = false;
  const shutdown = gate.shutdown(
    async () => {},
    1000,
    async () => {
      cleared = true;
    },
  );
  await Promise.resolve();
  expect(cleared).toBe(false);
  gate.finishMutation(-1);
  expect((await shutdown)[0]?.status).toBe('fulfilled');
  expect(cleared).toBe(true);
});

it('reports abandoned verification once so a subsequent quit can succeed', async () => {
  const gate = new DesktopAuthGate();
  const stale = gate.reserve('/auth/verify-code')!;
  gate.abandonUnclaimed();
  expect(gate.continueMutation(-1, stale, '/auth/verify-code')).toBe(false);
  expect((await gate.shutdown(async () => {}, 100))[0]?.status).toBe('rejected');
  expect((await gate.shutdown(async () => {}, 100))[0]?.status).toBe('fulfilled');
});

it('allows verification to be retried after the interruption is acknowledged', () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/verify-code');
  gate.abandonUnclaimed();
  expect(gate.reserve('/auth/verify-code')).toBeNull();
  expect(gate.acknowledgeInterruptedVerification()).toBe(true);
  expect(gate.reserve('/auth/verify-code')).toBeTruthy();
  expect(gate.acknowledgeInterruptedVerification()).toBe(false);
});

it('retains failed logout recovery for a later retry', async () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/logout');
  gate.abandonUnclaimed();
  const failed = await gate.shutdown(
    async () => {},
    100,
    async () => {
      throw new Error('storage failure');
    },
  );
  expect(failed[0]?.status).toBe('rejected');
  let cleared = false;
  const retry = await gate.shutdown(
    async () => {},
    100,
    async () => {
      cleared = true;
    },
  );
  expect(retry[0]?.status).toBe('fulfilled');
  expect(cleared).toBe(true);
});

it('allows cleanup retry after a transient logout failure without admitting verification', async () => {
  const gate = new DesktopAuthGate();
  const token = gate.reserve('/auth/logout')!;
  expect(gate.release(token)).toBe(true);
  await expect(
    gate.recoverAbandonedLogout(async () => {
      throw new Error('temporary');
    }),
  ).rejects.toThrow('temporary');
  expect(gate.reserve('/auth/verify-code')).toBeNull();
  await gate.recoverAbandonedLogout(async () => {});
  expect(gate.networkBlocked()).toBe(false);
  expect(gate.reserve('/auth/verify-code')).toBeTruthy();
});

it('holds reopened-window network until abandoned logout is cleared', async () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/logout');
  gate.abandonUnclaimed();
  let finish!: () => void;
  const recovery = gate.recoverAbandonedLogout(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  let admitted = false;
  const request = gate.networkReady().then(() => {
    admitted = true;
  });
  await Promise.resolve();
  expect(admitted).toBe(false);
  finish();
  await recovery;
  await request;
  expect(admitted).toBe(true);
});

it('bounds a hung cookie cleanup without starting a conflicting retry', async () => {
  const gate = new DesktopAuthGate();
  gate.reserve('/auth/logout');
  gate.abandonUnclaimed();
  let finish!: () => void;
  let calls = 0;
  const clear = () => {
    calls++;
    return new Promise<void>((resolve) => {
      finish = resolve;
    });
  };
  await expect(gate.recoverAbandonedLogout(clear, 20)).rejects.toThrow('did not finish');
  await expect(gate.recoverAbandonedLogout(clear, 20)).rejects.toThrow('did not finish');
  expect(calls).toBe(1);
  expect(gate.networkBlocked()).toBe(true);
  finish();
  await gate.recoverAbandonedLogout(clear, 100);
  expect(gate.networkBlocked()).toBe(false);
});

it('allows retention only when the verification has drained and no logout is reserved', () => {
  const gate = new DesktopAuthGate();
  const verification = gate.reserve('/auth/verify-code')!;
  expect(gate.canFinishVerification()).toBe(false);
  gate.continueMutation(-1, verification, '/auth/verify-code');
  gate.finishMutation(-1);
  expect(gate.canFinishVerification()).toBe(true);
  gate.reserve('/auth/logout');
  expect(gate.canFinishVerification()).toBe(false);
});

it('binds retention to both browser cookies, native owner, reservation and replacement document', async () => {
  const cookies = {
    get: vi.fn(async () => [
      {
        name: 'lyk_session',
        value: 'old',
        path: '/',
        domain: 'lab.test',
        hostOnly: true,
        httpOnly: true,
        secure: true,
        session: true,
        sameSite: 'lax' as const,
      },
    ]),
  };
  let document = 'old-doc';
  let ready = true;
  let native = true;
  let identity: unknown = { id: 'a' };
  const session = { cookies, fetch: async () => Response.json(identity) };
  const proof = new DesktopVerification(
    'https://lab.test',
    session,
    () => document,
    () => ready,
  );
  const verify = async () => native;
  const probe = vi
    .spyOn(globalThis, 'fetch')
    .mockImplementation(async () => Response.json(identity));
  try {
    for (const headers of [
      { Cookie: 'lyk_session=forged' },
      { Cookie: 'lyk_session=old; lyk_session=old' },
      {},
    ]) {
      proof.reserve('r', document, { current: () => native, verify });
      await proof.capture('r', headers as Record<string, string>);
      expect(probe).not.toHaveBeenCalled();
    }
    proof.reserve('r', document, { current: () => native, verify });
    await proof.capture('stale', { Cookie: 'lyk_session=old' });
    expect(probe).not.toHaveBeenCalled();
    await proof.capture('r', { Cookie: 'other=private; lyk_session=old' });
    expect(probe).toHaveBeenCalledWith(
      'https://lab.test/api/me',
      expect.objectContaining({
        headers: { Cookie: 'lyk_session=old' },
        redirect: 'error',
      }),
    );
    expect(await proof.prepare(document, 'a')).toBe(false);
    document = 'new-doc';
    identity = { id: 'b' };
    await proof.read(document);
    expect(await proof.prepare(document, 'a')).toBe(false);
    identity = { id: [] };
    await expect(proof.read(document)).rejects.toThrow();
    expect(await proof.prepare(document, 'a')).toBe(false);
    identity = { id: 'a' };
    const { receipt } = await proof.read(document);
    expect(proof.matches(document, 'a', receipt)).toBe(true);
    expect(await proof.prepare(document, 'a')).toBe(true);
    native = false;
    expect(proof.canFinish(document, 'a')).toBe(false);
    native = true;
    ready = false;
    expect(proof.canFinish(document, 'a')).toBe(false);
    ready = true;
    document = 'newer-doc';
    expect(proof.canFinish('new-doc', 'a')).toBe(false);
    await proof.read(document);
    expect(await proof.prepare(document, 'a')).toBe(true);
    proof.cookieChanged();
    expect(proof.canFinish(document, 'a')).toBe(false);
    expect(proof.matches(document, 'a', receipt)).toBe(false);
    await proof.read(document);
    expect(await proof.prepare(document, 'a')).toBe(true);
    proof.invalidate();
    expect(proof.canFinish(document, 'a')).toBe(false);
    expect(await proof.prepare(document, 'a')).toBe(false);
  } finally {
    probe.mockRestore();
  }
});

it('reads a refused sign-in without an attempt and rejects stale identity reads from a departed document', async () => {
  let document = 'first';
  let complete!: (response: Response) => void;
  const session = {
    cookies: { get: async () => [] },
    fetch: vi.fn(async () => new Response(null, { status: 401 })),
  };
  const proof = new DesktopVerification(
    'https://lab.test',
    session,
    () => document,
    () => true,
  );
  session.fetch.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        complete = resolve;
      }),
  );
  const old = proof.read(document);
  await vi.waitFor(() => expect(session.fetch).toHaveBeenCalledOnce());
  document = 'replacement';
  const signedOut = await proof.read(document);
  complete(Response.json({ id: 'a' }));
  await expect(old).rejects.toThrow('changed');
  expect(signedOut.user).toBeNull();
  expect(proof.matches(document, null, signedOut.receipt)).toBe(true);
  expect(await proof.prepare(document, 'a')).toBe(false);
  proof.invalidate();
  expect(proof.matches(document, null, signedOut.receipt)).toBe(false);
  expect(session.fetch).toHaveBeenCalledWith(
    'https://lab.test/api/me',
    expect.objectContaining({
      bypassCustomProtocolHandlers: true,
      credentials: 'include',
      cache: 'no-store',
      redirect: 'error',
    }),
  );
});

it('authenticates packaged controls from the current partition and fences admission across cookie or document changes', async () => {
  let doc = 'first';
  let ready = true;
  let allowed = true;
  const session = {
    cookies: {
      get: async () => [
        {
          name: 'lyk_session',
          value: 'fixture',
          domain: 'lab.test',
          path: '/',
          hostOnly: true,
          httpOnly: true,
          secure: true,
          session: true,
          sameSite: 'lax' as const,
        },
      ],
    },
    fetch: vi.fn(async () => Response.json({ id: 'alice' })),
  };
  const verification = new DesktopVerification(
    'https://lab.test',
    session,
    () => doc,
    () => ready,
  );
  const admitted = verification.daemonAuthorization(() => allowed);
  await expect(admitted('bob')).rejects.toThrow();
  const lease = await admitted('alice');
  expect(lease()).toBe(true);
  allowed = false;
  expect(lease()).toBe(false);
  allowed = true;
  verification.cookieChanged();
  expect(lease()).toBe(false);
  await expect(admitted('alice')).rejects.toThrow();
  const second = verification.daemonAuthorization(() => allowed);
  doc = 'other';
  await expect(second('alice')).rejects.toThrow();
  ready = false;
  await expect(verification.daemonAuthorization(() => true)('alice')).rejects.toThrow();
  expect(session.fetch).toHaveBeenCalledWith(
    'https://lab.test/api/me',
    expect.objectContaining({
      credentials: 'include',
      redirect: 'error',
      bypassCustomProtocolHandlers: true,
    }),
  );
});
