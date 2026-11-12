import { afterEach, expect, it, vi } from 'vitest';
import { createSSRApp } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query';
import SignInView from './SignInView.vue';
import { notice } from '../../state/notice';

afterEach(() => vi.unstubAllGlobals());
it('shows a local-only logout notice on the sign-in screen', async () => {
  notice.value = 'Signed out on this computer. The server did not confirm sign-out.';
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: SignInView }],
  });
  const app = createSSRApp(SignInView);
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: new QueryClient() });
  expect(await renderToString(app)).toContain(notice.value);
  notice.value = '';
});
it('does not show a prior workspace error on the sign-in screen', async () => {
  notice.value = 'Private study A could not be opened';
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: SignInView }],
  });
  const app = createSSRApp(SignInView);
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: new QueryClient() });
  expect(await renderToString(app)).not.toContain('Private study A');
  notice.value = '';
});
it.each([
  ['packaged desktop', true, true],
  ['desktop preview', true, false],
  ['browser', false, false],
])('shows usable sign-in on %s', async (_name, desktop, bridge) => {
  vi.stubGlobal(
    'window',
    desktop ? { lykeionDesktop: bridge ? { googleSignIn: vi.fn() } : {} } : {},
  );
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: SignInView }],
  });
  const app = createSSRApp(SignInView);
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: new QueryClient() });
  const html = await renderToString(app);
  expect(html).toContain('Continue with email');
  expect(html.includes('Continue with Google')).toBe(!desktop || bridge);
});
