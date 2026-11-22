import { beforeEach, expect, it, vi } from 'vitest';
import { rememberSlackLink, takeSlackLink } from './slack-return';

const saved = new Map<string, string>();
beforeEach(() => {
  saved.clear();
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => saved.get(k) ?? null,
    setItem: (k: string, v: string) => void saved.set(k, v),
  });
});
it('returns the signed-out Slack link with its token once, and ignores other routes', () => {
  rememberSlackLink('/studies', '/studies');
  expect(takeSlackLink()).toBe('');
  rememberSlackLink('/slack/link', '/slack/link?workspace=w&token=t');
  expect(takeSlackLink()).toBe('/slack/link?workspace=w&token=t');
  expect(takeSlackLink()).toBe('');
});
