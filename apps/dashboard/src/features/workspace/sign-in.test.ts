import { afterEach, expect, it, vi } from 'vitest';
import { createSSRApp } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { createMemoryHistory, createRouter } from 'vue-router';
import { VueQueryPlugin, QueryClient } from '@tanstack/vue-query';
import SignInView from './SignInView.vue';

afterEach(() => vi.unstubAllGlobals());
it.each([
  ['desktop', true],
  ['browser', false],
])('shows usable sign-in on %s', async (_name, desktop) => {
  vi.stubGlobal('window', desktop ? { lykeionDesktop: {} } : {});
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: SignInView }],
  });
  const app = createSSRApp(SignInView);
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: new QueryClient() });
  const html = await renderToString(app);
  expect(html).toContain('Continue with email');
  expect(html.includes('Continue with Google')).toBe(!desktop);
});
