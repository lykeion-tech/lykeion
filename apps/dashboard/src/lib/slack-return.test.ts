import { beforeEach, expect, it, vi } from 'vitest';
import { createMemoryHistory, createRouter } from 'vue-router';
import { landedOn, rememberSlackLink, takeSlackLink } from './slack-return';

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

it('lands on the saved Slack link with its query intact after sign-in', async () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
  });
  await router.push('/signin');
  rememberSlackLink('/slack/link', '/slack/link?workspace=w&token=t');
  const target = takeSlackLink();
  await router.replace(target);
  expect(landedOn(router.currentRoute.value, target)).toBe(true);
  expect(router.currentRoute.value.query).toEqual({ workspace: 'w', token: 't' });
  expect(landedOn({ path: '/signin' }, '/studies')).toBe(false);
});
