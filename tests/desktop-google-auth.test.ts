import { describe, it, expect, vi } from 'vitest';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';
import { DesktopGoogleAuth } from '../apps/desktop/src/desktop-google-auth';

describe('desktop Google authentication', () => {
  it('exchanges privately and writes only the selected-origin session cookie', async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ticket: 'T'.repeat(43) }), { status: 200 }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600 }), { status: 200 }),
      );
    const open = vi.fn().mockResolvedValue(undefined);
    const set = vi.fn().mockResolvedValue(undefined);
    const flush = vi.fn().mockResolvedValue(undefined);
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open,
      set,
      flush,
      current: () => true,
      capture: async () => {},
    });
    await flow.run();
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
      set,
      flush: async () => {},
      current: () => current,
      capture: async () => {},
    });
    const pending = flow.run();
    await vi.waitFor(() => expect(complete).toBeTypeOf('function'));
    current = false;
    flow.cancel();
    complete(
      new Response(JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600 }), { status: 200 }),
    );
    await expect(pending).rejects.toThrow();
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
        new Response(JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600 }), { status: 200 }),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      set: () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    const pending = flow.run().finally(() => gate.finishMutation(-1));
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
        new Response(JSON.stringify({ token: 'abc.def.xyz', expires_in: 3600 }), { status: 200 }),
      );
    const flow = new DesktopGoogleAuth('https://lykeion.example', {
      request,
      open: async () => {},
      set,
      flush: async () => {},
      current: () => true,
      capture: async () => {},
    });
    let settled = false;
    const pending = flow.run().finally(() => {
      settled = true;
    });
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'));
    flow.cancel();
    await Promise.resolve();
    expect(settled).toBe(false);
    finish();
    await expect(pending).rejects.toThrow();
  });
});
