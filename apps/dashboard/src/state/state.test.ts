import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';

let stored: Map<string, string>;
function memoryStorage() {
  return {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => void stored.set(key, value),
  };
}
const unavailable = {
  getItem: () => {
    throw new Error('SecurityError');
  },
  setItem: () => {
    throw new Error('QuotaExceededError');
  },
};

beforeEach(() => {
  vi.resetModules();
  stored = new Map();
  vi.stubGlobal('localStorage', memoryStorage());
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe('storage helper', () => {
  it('falls back and reports failure when storage is unavailable', async () => {
    vi.stubGlobal('localStorage', unavailable);
    const storage = await import('../storage');
    expect(storage.readText('lykeion.api-url')).toBeUndefined();
    expect(storage.readJSON('lykeion.profile', { name: '' })).toEqual({ name: '' });
    expect(storage.writeJSON('lykeion.profile', { name: 'Ada' })).toBe(false);
    expect(storage.writeText('lykeion.theme', 'dark')).toBe(false);
  });
  it('falls back on bad JSON and a stored null, and leaves the stored value alone', async () => {
    const storage = await import('../storage');
    stored.set('lykeion.pinned-tasks', '{not json');
    stored.set('lykeion.profile', 'null');
    expect(storage.readJSON('lykeion.pinned-tasks', [])).toEqual([]);
    expect(storage.readJSON('lykeion.profile', 'fallback')).toBe('fallback');
    expect(stored.get('lykeion.pinned-tasks')).toBe('{not json');
  });
  it('round-trips JSON and text, and reads a missing key as null', async () => {
    const storage = await import('../storage');
    expect(storage.writeJSON('lykeion.saved-views', [{ id: 'a' }])).toBe(true);
    expect(stored.get('lykeion.saved-views')).toBe('[{"id":"a"}]');
    expect(storage.readJSON('lykeion.saved-views', [])).toEqual([{ id: 'a' }]);
    expect(storage.readText('lykeion.api-url')).toBeNull();
  });
});

describe('pins', () => {
  it('loads saved pins, keeps the stored format and survives a reload', async () => {
    stored.set('lykeion.pinned-tasks', '["t1",7,"t2"]');
    const { usePins } = await import('./pins');
    const { pins, togglePin } = usePins();
    expect(pins.value).toEqual(['t1', 't2']);
    expect(togglePin('t1')).toBe(true);
    expect(togglePin('t3')).toBe(true);
    expect(stored.get('lykeion.pinned-tasks')).toBe('["t2","t3"]');
    vi.resetModules();
    const reloaded = await import('./pins');
    expect(reloaded.usePins().pins.value).toEqual(['t2', 't3']);
  });
  it('leaves the pins unchanged when the write fails', async () => {
    const { usePins } = await import('./pins');
    const { pins, togglePin } = usePins();
    vi.stubGlobal('localStorage', unavailable);
    expect(togglePin('t1')).toBe(false);
    expect(pins.value).toEqual([]);
  });
});

describe('preferences', () => {
  beforeEach(() => {
    const root = { dataset: {} as Record<string, string>, style: { setProperty: () => {} } };
    vi.stubGlobal('document', { documentElement: root });
  });
  it('normalizes saved preferences and writes them back in the same format', async () => {
    stored.set(
      'lykeion.preferences',
      JSON.stringify({ theme: 'midnight', replyMode: 'new', pinComments: false, extra: 1 }),
    );
    const { usePreferences } = await import('./preferences');
    const prefs = usePreferences();
    expect(prefs.theme).toBe('midnight');
    expect(prefs.replyMode).toBe('after');
    expect(prefs.pinComments).toBe(false);
    expect(prefs.createFields).toEqual({
      agent: ['Project'],
      manual: ['Status', 'Priority', 'Assignee', 'Labels', 'Project'],
    });
    prefs.taskOpen = 'preview';
    prefs.theme = 'ash';
    await nextTick();
    const saved = JSON.parse(stored.get('lykeion.preferences')!);
    expect(saved).toMatchObject({ theme: 'ash', taskOpen: 'preview', extra: 1 });
    expect(stored.get('lykeion.theme')).toBe('light');
    vi.resetModules();
    const reloaded = await import('./preferences');
    expect(reloaded.usePreferences()).toMatchObject({ theme: 'ash', taskOpen: 'preview' });
  });
  it('uses the defaults when the saved value is unreadable', async () => {
    stored.set('lykeion.preferences', '{broken');
    const { usePreferences } = await import('./preferences');
    expect(usePreferences()).toMatchObject({ theme: 'ash', pinComments: true, taskOpen: 'page' });
  });
});

describe('workspace tab defaults', () => {
  it('opens every new tab directly on Studies with the Studies title', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const state = useWorkspaceTabs();
    state.openTab('/teams', 'Teams', 'network');
    state.openTab();
    state.openTab();
    expect(
      state.tabs.value
        .slice(1)
        .map((tab) => ({ path: tab.path, title: tab.title, history: tab.history })),
    ).toEqual([
      { path: '/studies', title: 'Studies', history: ['/studies'] },
      { path: '/studies', title: 'Studies', history: ['/studies'] },
    ]);
    expect(state.selected.value).toBe(state.tabs.value[2]!.id);
  });

  it('restores tabs only after the same account is known and clears them on logout', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const state = useWorkspaceTabs();
    state.setAccount('account-a');
    state.openTab('/studies/private', 'Private study A', 'tasks');
    await nextTick();
    vi.resetModules();
    const reloaded = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(reloaded.tabs.value).toEqual([]);
    reloaded.setAccount('account-a');
    expect(reloaded.tabs.value.map((tab) => tab.title)).toEqual(['Private study A']);
    reloaded.clearAccount();
    reloaded.setAccount('account-b');
    expect(reloaded.tabs.value).toEqual([]);
    expect([...stored.values()].join(' ')).not.toContain('Private study A');
  });

  it('drops prior-account tabs on a direct switch and never adopts legacy tabs', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    stored.set(
      'lykeion.tabs',
      JSON.stringify([
        {
          id: 1,
          path: '/studies/secret',
          title: 'Legacy secret',
          icon: 'tasks',
          history: ['/studies/secret'],
          index: 0,
        },
      ]),
    );
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const state = useWorkspaceTabs();
    state.setAccount('account-a');
    expect(state.tabs.value).toEqual([]);
    state.openTab('/studies/secret', 'Secret A', 'tasks');
    state.setAccount('account-b');
    expect(state.tabs.value).toEqual([]);
    expect([...stored.values()].join(' ')).not.toContain('Secret A');
    expect([...stored.values()].join(' ')).not.toContain('Legacy secret');
  });

  it('reports a saved-owner mismatch before a cold reload can reopen a private route', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    stored.set(
      'lykeion.workspace-tabs',
      JSON.stringify({ account: 'account-a', tabs: [], selected: 0 }),
    );
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const state = useWorkspaceTabs();
    expect(state.setAccount('account-b')).toBe(true);
    expect(state.tabs.value).toEqual([]);
    vi.resetModules();
    const interrupted = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(interrupted.setAccount('account-b')).toBe(true);
    interrupted.confirmAccountSwitch();
    vi.resetModules();
    const completed = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(completed.setAccount('account-b')).toBe(false);
  });

  it('retains a migration fence across reloads before identity is known', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    stored.set(
      'lykeion.tabs',
      JSON.stringify([{ path: '/coresearch/a?file=private.tex', title: 'Private A' }]),
    );
    let state = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(state.hasPendingSwitch()).toBe(true);
    vi.resetModules();
    state = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(state.hasPendingSwitch()).toBe(true);
    vi.resetModules();
    state = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(state.hasPendingSwitch()).toBe(true);
    expect(state.setAccount('account-b')).toBe(true);
    expect(state.tabs.value).toEqual([]);
    expect([...stored.values()].join(' ')).not.toContain('private.tex');
  });

  it('removes legacy private titles while preserving the fence when storage cannot grow', async () => {
    stored.set(
      'lykeion.tabs',
      JSON.stringify([{ path: '/studies/a?file=private.tex', title: 'Private A' }]),
    );
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => {
        if (value.length > (stored.get(key)?.length ?? 0)) throw new Error('QuotaExceededError');
        stored.set(key, value);
      },
    });
    let state = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(state.hasPendingSwitch()).toBe(true);
    expect([...stored.values()].join(' ')).not.toContain('private.tex');
    vi.resetModules();
    state = (await import('./workspace-tabs')).useWorkspaceTabs();
    expect(state.hasPendingSwitch()).toBe(true);
    expect(state.setAccount('account-b')).toBe(true);
    state.clearAccount();
    expect([...stored.values()].join(' ')).not.toContain('Private A');
  });

  it('blocks private invitation and onboarding views while identity is unverified', async () => {
    const { accountViewBlocked } = await import('./workspace-tabs');
    expect(accountViewBlocked(false, undefined, true)).toBe(true);
    expect(accountViewBlocked(false, 'account-b', true)).toBe(true);
    expect(accountViewBlocked(false, 'account-b', false)).toBe(false);
    expect(accountViewBlocked(true, undefined, true)).toBe(false);
  });

  it('keeps the account switch fenced after a failed identity refresh until a successful retry', async () => {
    const { verifyAccountSwitch } = await import('./workspace-tabs');
    const reset = vi.fn().mockResolvedValue(undefined);
    const identify = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue('account-b');
    const navigate = vi.fn().mockResolvedValue(true);
    await expect(
      verifyAccountSwitch('account-b', () => true, reset, identify, navigate),
    ).rejects.toThrow('offline');
    expect(navigate).not.toHaveBeenCalled();
    await expect(
      verifyAccountSwitch('account-b', () => true, reset, identify, navigate),
    ).resolves.toBe(true);
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  it('does not navigate when a newer account transition overtakes a refresh', async () => {
    const { verifyAccountSwitch } = await import('./workspace-tabs');
    let current = true;
    const navigate = vi.fn().mockResolvedValue(true);
    const result = await verifyAccountSwitch(
      'account-b',
      () => current,
      async () => {
        current = false;
      },
      async () => 'account-b',
      navigate,
    );
    expect(result).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('clears tab titles immediately when the authentication session is lost', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const { authenticationLost } = await import('../api/auth-loss');
    const state = useWorkspaceTabs();
    state.setAccount('account-a');
    state.openTab('/studies/private', 'Private A', 'tasks');
    authenticationLost();
    expect(state.tabs.value).toEqual([]);
    expect([...stored.values()].join(' ')).not.toContain('Private A');
  });

  it('clears prior-account tabs when a new sign-in is verified', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const { authenticationRestored } = await import('../api/auth-loss');
    const state = useWorkspaceTabs();
    state.setAccount('account-a');
    state.openTab('/studies/private', 'Private A', 'tasks');
    authenticationRestored();
    expect(state.tabs.value).toEqual([]);
    state.setAccount('account-b');
    expect(state.tabs.value).toEqual([]);
    expect([...stored.values()].join(' ')).not.toContain('Private A');
  });

  it('allows a verified new sign-in to resume its invitation after auth loss', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const { authenticationLost, authenticationRestored } = await import('../api/auth-loss');
    const state = useWorkspaceTabs();
    state.setAccount('account-a');
    state.openTab('/studies/private', 'Private A', 'tasks');
    authenticationLost();
    authenticationRestored();
    expect(state.setAccount('account-b')).toBe(false);
    expect(state.tabs.value).toEqual([]);
    expect([...stored.values()].join(' ')).not.toContain('Private A');
  });

  it('ignores a delayed 401 from the prior account after a direct switch', async () => {
    vi.stubGlobal('sessionStorage', memoryStorage());
    const { useWorkspaceTabs } = await import('./workspace-tabs');
    const { authenticationEpoch, authenticationLost, advanceAuthenticationEpoch } =
      await import('../api/auth-loss');
    const state = useWorkspaceTabs();
    state.setAccount('account-a');
    const oldRequest = authenticationEpoch();
    state.setAccount('account-b');
    advanceAuthenticationEpoch();
    state.openTab('/studies/new', 'Account B study', 'tasks');
    authenticationLost(oldRequest);
    expect(state.tabs.value.map((tab) => tab.title)).toEqual(['Account B study']);
  });
});
