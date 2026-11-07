// @vitest-environment node
import { createHash } from 'node:crypto';
import { describe, expect, it, vi } from 'vitest';
import { DesktopAuthGate } from '../apps/desktop/src/desktop-auth-gate';
import { DesktopGoogleLogin } from '../apps/desktop/src/desktop-google-login';

describe('desktop system-browser Google handoff', () => {
  it('checks callback origin/state and exchanges a proof-bound code once', async () => {
    const gate = new DesktopAuthGate();
    const apply = vi.fn(async () => {});
    const rollback = vi.fn(async () => {});
    let challenge = '';
    let listener = '';
    let nonce = '';
    const exchange = vi.fn(async (code: string, verifier: string) => {
      expect(code).toBe(Buffer.alloc(32, 1).toString('base64url'));
      expect(createHash('sha256').update(verifier).digest('base64url')).toBe(challenge);
      return { apply, rollback };
    });
    const open = vi.fn(async (url: string) => {
      const u = new URL(url);
      challenge = u.searchParams.get('desktop_challenge')!;
      nonce = u.searchParams.get('desktop_state')!;
      listener = `http://127.0.0.1:${u.searchParams.get('desktop_port')}/callback`;
      expect(u.origin).toBe('https://lab.example');
      expect(url).not.toContain('token=');
      expect(
        (
          await fetch(listener, {
            method: 'POST',
            headers: { origin: 'https://attacker.example', 'content-type': 'application/json' },
            body: JSON.stringify({ state: nonce, code: Buffer.alloc(32, 1).toString('base64url') }),
          })
        ).status,
      ).toBe(403);
      expect(
        (
          await fetch(listener, {
            method: 'POST',
            headers: { origin: new URL(listener).origin, 'content-type': 'application/json' },
            body: JSON.stringify({
              state: 'x'.repeat(43),
              code: Buffer.alloc(32, 1).toString('base64url'),
            }),
          })
        ).status,
      ).toBe(400);
      const page = await fetch(listener);
      expect(page.headers.get('referrer-policy')).toBe('no-referrer');
      const ok = await fetch(listener, {
        method: 'POST',
        headers: { origin: new URL(listener).origin, 'content-type': 'application/json' },
        body: JSON.stringify({ state: nonce, code: Buffer.alloc(32, 1).toString('base64url') }),
      });
      expect(ok.status).toBe(200);
    });
    const login = new DesktopGoogleLogin('https://lab.example', gate, { open, exchange });
    await login.start();
    expect(exchange).toHaveBeenCalledOnce();
    expect(apply).toHaveBeenCalledOnce();
    expect(rollback).not.toHaveBeenCalled();
  });
  it('does not install a completed exchange after logout begins', async () => {
    const gate = new DesktopAuthGate();
    let finish!: (v: { apply: () => Promise<void>; rollback: () => Promise<void> }) => void;
    const apply = vi.fn(async () => {});
    let state = '';
    let listener = '';
    const login = new DesktopGoogleLogin('https://lab.example', gate, {
      open: async (url) => {
        const u = new URL(url);
        state = u.searchParams.get('desktop_state')!;
        listener = `http://127.0.0.1:${u.searchParams.get('desktop_port')}/callback`;
      },
      exchange: () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    });
    const attempt = login.start();
    await vi.waitFor(() => expect(listener).not.toBe(''));
    const callback = fetch(listener, {
      method: 'POST',
      headers: { origin: new URL(listener).origin, 'content-type': 'application/json' },
      body: JSON.stringify({ state, code: Buffer.alloc(32, 1).toString('base64url') }),
    }).catch(() => null);
    await vi.waitFor(() => expect(finish).toBeDefined());
    await gate.mutation();
    finish({ apply, rollback: async () => {} });
    await attempt.catch(() => {});
    await callback;
    expect(apply).not.toHaveBeenCalled();
  });
});
