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
    const storage = await import('../apps/dashboard/src/storage');
    expect(storage.readText('lykeion.api-url')).toBeUndefined();
    expect(storage.readJSON('lykeion.profile', { name: '' })).toEqual({ name: '' });
    expect(storage.writeJSON('lykeion.profile', { name: 'Ada' })).toBe(false);
    expect(storage.writeText('lykeion.theme', 'dark')).toBe(false);
  });
  it('falls back on bad JSON and a stored null, and leaves the stored value alone', async () => {
    const storage = await import('../apps/dashboard/src/storage');
    stored.set('lykeion.pinned-tasks', '{not json');
    stored.set('lykeion.profile', 'null');
    expect(storage.readJSON('lykeion.pinned-tasks', [])).toEqual([]);
    expect(storage.readJSON('lykeion.profile', 'fallback')).toBe('fallback');
    expect(stored.get('lykeion.pinned-tasks')).toBe('{not json');
  });
  it('round-trips JSON and text, and reads a missing key as null', async () => {
    const storage = await import('../apps/dashboard/src/storage');
    expect(storage.writeJSON('lykeion.saved-views', [{ id: 'a' }])).toBe(true);
    expect(stored.get('lykeion.saved-views')).toBe('[{"id":"a"}]');
    expect(storage.readJSON('lykeion.saved-views', [])).toEqual([{ id: 'a' }]);
    expect(storage.readText('lykeion.api-url')).toBeNull();
  });
});

describe('pins', () => {
  it('loads saved pins, keeps the stored format and survives a reload', async () => {
    stored.set('lykeion.pinned-tasks', '["t1",7,"t2"]');
    const { usePins } = await import('../apps/dashboard/src/state/pins');
    const { pins, togglePin } = usePins();
    expect(pins.value).toEqual(['t1', 't2']);
    expect(togglePin('t1')).toBe(true);
    expect(togglePin('t3')).toBe(true);
    expect(stored.get('lykeion.pinned-tasks')).toBe('["t2","t3"]');
    vi.resetModules();
    const reloaded = await import('../apps/dashboard/src/state/pins');
    expect(reloaded.usePins().pins.value).toEqual(['t2', 't3']);
  });
  it('leaves the pins unchanged when the write fails', async () => {
    const { usePins } = await import('../apps/dashboard/src/state/pins');
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
    const { usePreferences } = await import('../apps/dashboard/src/state/preferences');
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
    const reloaded = await import('../apps/dashboard/src/state/preferences');
    expect(reloaded.usePreferences()).toMatchObject({ theme: 'ash', taskOpen: 'preview' });
  });
  it('uses the defaults when the saved value is unreadable', async () => {
    stored.set('lykeion.preferences', '{broken');
    const { usePreferences } = await import('../apps/dashboard/src/state/preferences');
    expect(usePreferences()).toMatchObject({ theme: 'ash', pinComments: true, taskOpen: 'page' });
  });
});
