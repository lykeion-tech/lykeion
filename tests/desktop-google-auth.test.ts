import { describe, it, expect, vi } from 'vitest';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';
import { DesktopGoogleAuth } from '../apps/desktop/src/desktop-google-auth';

async function enterCode(flow: DesktopGoogleAuth) {
  await vi.waitFor(async () => expect(await flow.submitCode('A'.repeat(43))).toBe(true));
}

describe('desktop Google authentication', () => {
  it('cancels a stalled browser launch without exchanging or writing a cookie', async () => {
    let finish!: () => void;
    const open = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      );
    const set = vi.fn();
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open,
      confirm: async () => true,
      set,
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run();
    const rejected = expect(pending).rejects.toThrow();
    await vi.waitFor(() => expect(open).toHaveBeenCalledOnce());
    flow.cancel();
    await rejected;
    finish();
    await Promise.resolve();
    expect(request).toHaveBeenCalledTimes(1);
    expect(set).not.toHaveBeenCalled();
  });
  it('lets the user correct a wrong browser code without losing the pending flow', async () => {
    const open = vi.fn().mockResolvedValue(undefined);
    const set = vi.fn();
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(new Response('', { status: 400 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
          { status: 200 },
        ),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open,
      confirm: async () => true,
      set,
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run();
    await vi.waitFor(() => expect(open).toHaveBeenCalledOnce());
    expect(await flow.submitCode('A'.repeat(42) + 'E')).toBe(false);
    expect(request).toHaveBeenCalledTimes(2);
    expect(JSON.parse(String(request.mock.calls[1]![1].body)).code).toBe('A'.repeat(42) + 'E');
    expect(set).not.toHaveBeenCalled();
    await enterCode(flow);
    await pending;
    expect(request).toHaveBeenCalledTimes(3);
    expect(set).toHaveBeenCalledOnce();
  });
  it('rejects deceptive account names before showing a dialog or setting a cookie', async () => {
    const confirm = vi.fn();
    const set = vi.fn();
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            token: 'abc.def.xyz',
            expires_in: 3600,
            email: 'user@safe.test\u202eevil',
          }),
          { status: 200 },
        ),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      confirm,
      set,
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run();
    const rejected = expect(pending).rejects.toThrow('Invalid sign-in response');
    await enterCode(flow);
    await rejected;
    expect(confirm).not.toHaveBeenCalled();
    expect(set).not.toHaveBeenCalled();
  });
  it('does not install a session if account confirmation is cancelled mid-dialog', async () => {
    let dialogSignal: AbortSignal | undefined;
    const set = vi.fn();
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
          { status: 200 },
        ),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      confirm: (_email, signal) => {
        dialogSignal = signal;
        return new Promise<boolean>(() => {});
      },
      set,
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run();
    const rejected = expect(pending).rejects.toThrow();
    await enterCode(flow);
    await vi.waitFor(() => expect(dialogSignal).toBeDefined());
    flow.cancel();
    await rejected;
    expect(dialogSignal?.aborted).toBe(true);
    expect(set).not.toHaveBeenCalled();
  });
  it('rejects codes without a pending flow and cancellation cannot start an exchange', async () => {
    const open = vi.fn().mockResolvedValue(undefined);
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open,
      confirm: async () => true,
      set: async () => {},
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    expect(await flow.submitCode('A'.repeat(43))).toBe(false);
    const pending = flow.run();
    const rejected = expect(pending).rejects.toThrow();
    await vi.waitFor(() => expect(open).toHaveBeenCalledOnce());
    expect(await flow.submitCode('bad')).toBe(false);
    flow.cancel();
    await rejected;
    expect(await flow.submitCode('A'.repeat(43))).toBe(false);
    expect(request).toHaveBeenCalledTimes(1);
  });
  it('waits for a browser-only code before privately exchanging the ticket', async () => {
    const open = vi.fn().mockResolvedValue(undefined);
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
          { status: 200 },
        ),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open,
      confirm: async () => true,
      set: async () => {},
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run();
    await vi.waitFor(() => expect(open).toHaveBeenCalledOnce());
    expect(request).toHaveBeenCalledTimes(1);
    expect(await flow.submitCode('A'.repeat(43))).toBe(true);
    await pending;
    const body = JSON.parse(String(request.mock.calls[1]![1].body));
    expect(body.code).toBe('A'.repeat(43));
    expect(body.ticket).toBe('T'.repeat(43));
    expect(body.verifier).toMatch(/^[A-Za-z0-9_-]{43}$/);
  });
  it('exchanges privately and writes only the selected-origin session cookie', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
          { status: 200 },
        ),
      );
    const open = vi.fn().mockResolvedValue(undefined);
    const set = vi.fn().mockResolvedValue(undefined);
    const flush = vi.fn().mockResolvedValue(undefined);
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open,
      confirm: async () => true,
      set,
      flush,
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run();
    await enterCode(flow);
    await pending;
    expect(open).toHaveBeenCalledWith(
      'https://lykeion.example/auth/google?desktop_ticket=' + 'T'.repeat(43),
    );
    expect(set).toHaveBeenCalledWith(
      expect.objectContaining({
        url: 'https://lykeion.example',
        name: 'lyk_session',
        value: 'abc.def.xyz',
        httpOnly: true,
        secure: true,
        path: '/',
      }),
    );
    expect(flush).toHaveBeenCalledOnce();
  });
  it('does not write a cookie if logout invalidates the attempt before exchange completes', async () => {
    let complete!: (response: Response) => void;
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            complete = resolve;
          }),
      );
    const set = vi.fn();
    let current = true;
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      confirm: async () => true,
      set,
      flush: async () => {},
      current: () => current,
      capture: async () => {},
    });
    const pending = flow.run();
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 0));
    const submitted = flow.submitCode('A'.repeat(43));
    await vi.waitFor(() => expect(complete).toBeTypeOf('function'));
    current = false;
    flow.cancel();
    complete(
      new Response(
        JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
        { status: 200 },
      ),
    );
    await expect(pending).rejects.toThrow();
    expect(await submitted).toBe(false);
    expect(set).not.toHaveBeenCalled();
  });
  it('holds logout behind an in-flight cookie write until that write is drained', async () => {
    let finish!: () => void;
    const gate = new DesktopAuthGate();
    const reservation = gate.reserve('/auth/verify-code')!;
    expect(gate.continueMutation(-1, reservation, '/auth/verify-code')).toBe(true);
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
          { status: 200 },
        ),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      confirm: async () => true,
      set: () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run().finally(() => gate.finishMutation(-1));
    await enterCode(flow);
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'));
    const logout = gate.reserve('/auth/logout')!;
    expect(gate.continueMutation(-2, logout, '/auth/logout')).toBe(true);
    flow.cancel();
    await expect(gate.waitForLogoutTurn(-2, 10)).rejects.toThrow();
    finish();
    await expect(pending).rejects.toThrow();
    await expect(gate.waitForLogoutTurn(-2, 50)).resolves.toBeUndefined();
    gate.finishMutation(-2);
  });
  it('waits for an already started cookie write to drain after cancellation', async () => {
    let finish!: () => void;
    const set = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600, email: 'user@example.org' }),
          { status: 200 },
        ),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      confirm: async () => true,
      set,
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    let settled = false;
    const pending = flow.run().finally(() => {
      settled = true;
    });
    await enterCode(flow);
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'));
    flow.cancel();
    await Promise.resolve();
    expect(settled).toBe(false);
    finish();
    await expect(pending).rejects.toThrow();
  });
});
