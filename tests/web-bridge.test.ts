import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  isOfficialMarketingHost,
  paths,
  resolveDashboardCtaHref,
  resolvePostAuthDestination,
} from '../apps/web/src/bridge';

describe('landing to dashboard bridge', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it('sends signed-out visitors to sign-in', () => {
    expect(
      resolveDashboardCtaHref({
        isAuthenticated: false,
        workspaceListReady: true,
        workspaces: [{}],
        hasOnboarded: true,
      }),
    ).toBe(paths.login);
  });

  it('points at the dashboard root while the workspace list loads', () => {
    expect(
      resolveDashboardCtaHref({
        isAuthenticated: true,
        workspaceListReady: false,
        workspaces: [],
        hasOnboarded: true,
      }),
    ).toBe('/app/');
  });

  it('resolves onboarding, a new workspace or the studies board', () => {
    expect(resolvePostAuthDestination([{}], false)).toBe('/app/#/onboarding');
    expect(resolvePostAuthDestination([], true)).toBe('/app/#/onboarding?step=workspace');
    expect(resolvePostAuthDestination([{}], true)).toBe('/app/#/studies');
  });

  it('treats unlisted hosts as app hosts', () => {
    expect(isOfficialMarketingHost('127.0.0.1')).toBe(false);
  });

  it('reads identity then workspaces for a signed-in visitor', async () => {
    const fetch = vi.fn(async (path: string) =>
      Response.json(path === '/api/me' ? { onboarded_at: '2026-10-31T00:00:00Z' } : [{ id: 'w' }]),
    );
    vi.stubGlobal('fetch', fetch);
    const bridge = await import('../apps/web/src/bridge');
    const session = await bridge.loadSession();
    expect(fetch.mock.calls.map(([path]) => path)).toEqual(['/api/me', '/api/workspaces']);
    expect(session.destination).toBe('/app/#/studies');
  });

  it('keeps a refused identity signed out without asking for workspaces', async () => {
    const fetch = vi.fn(async () => new Response('{}', { status: 401 }));
    vi.stubGlobal('fetch', fetch);
    const bridge = await import('../apps/web/src/bridge');
    const session = await bridge.loadSession();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(session.user).toBeNull();
    expect(session.destination).toBeNull();
  });
});
