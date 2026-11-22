import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { version } from '../../../../../package.json';
import {
  isNewerRelease,
  markReleaseSeen,
  releaseHighlights,
  shouldShowRelease,
} from './release-highlights';

let stored: Map<string, string>;
beforeEach(() => {
  stored = new Map();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => void stored.set(key, value),
  });
});
afterEach(() => vi.unstubAllGlobals());

it('shows once, until the version is marked seen', () => {
  expect(releaseHighlights?.length).toBeGreaterThan(0);
  expect(shouldShowRelease('a')).toBe(true);
  markReleaseSeen('a');
  expect(shouldShowRelease('a')).toBe(false);
  expect(shouldShowRelease('b')).toBe(true);
});

it('shows again only for a newer version', () => {
  stored.set('lykeion.releaseHighlights:a', '0.0.1');
  expect(shouldShowRelease('a')).toBe(true);
  stored.set('lykeion.releaseHighlights:a', version);
  expect(shouldShowRelease('a')).toBe(false);
  stored.set('lykeion.releaseHighlights:a', '99.0.0');
  expect(shouldShowRelease('a')).toBe(false);
  expect(isNewerRelease('0.1.10', '0.1.9')).toBe(true);
});

it('shows nothing when storage is unavailable', () => {
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw new Error('blocked');
    },
  });
  expect(shouldShowRelease('a')).toBe(false);
});
