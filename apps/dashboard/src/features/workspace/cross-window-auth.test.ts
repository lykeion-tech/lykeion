import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

const changed = vi.hoisted(() => ({ listener: undefined as undefined | (() => void) }));
vi.mock('../../api/auth-sync', () => ({
  announceReturnedGoogleAuthentication: () => {},
  onExternalAuthenticationChanged: (listener: () => void) => {
    changed.listener = listener;
    return () => {
      changed.listener = undefined;
    };
  },
  announceAuthenticationChanged: () => {},
}));
vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
vi.mock('../aristotle-window/AristotleWindow.vue', () => ({ default: { render: () => null } }));
vi.mock('../../api/realtime', () => ({
  useRealtimeSync: () => ({ reconnecting: ref(false), lost: ref(false) }),
}));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
  changed.listener = undefined;
});

it.each([false, true])(
  'conceals the prior session until revalidation succeeds (retry=%s)',
  async (retry) => {
    const data = new Map<string, string>();
    const storage = {
      getItem: (key: string) => data.get(key) ?? null,
      setItem: (key: string, value: string) => void data.set(key, value),
    };
    vi.stubGlobal('sessionStorage', storage);
    vi.stubGlobal('localStorage', storage);
    vi.stubGlobal('window', {
      location: { origin: 'https://app.example' },
      matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
      addEventListener() {},
      removeEventListener() {},
    });
    vi.stubGlobal('document', {
      title: '',
      baseURI: 'https://app.example/',
      querySelector: () => null,
      documentElement: { dataset: {}, style: { setProperty() {} } },
    });
    vi.stubGlobal('matchMedia', () => ({ matches: false }));
    let nextIdentity: Promise<Response> | undefined;
    let release!: (value: Response) => void;
    const fetch = vi.fn(async (url: string) => {
      if (url === '/api/me')
        return nextIdentity
          ? (await nextIdentity).clone()
          : Response.json({ id: 'a', onboarded_at: 'yes' });
      if (url === '/api/workspaces') return Response.json([{ id: 'workspace', name: 'Workspace' }]);
      return Response.json([]);
    });
    vi.stubGlobal('fetch', fetch);
    const { default: App } = await import('../../App.vue');
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/private', component: { render: () => h('div', 'Account A private study') } },
        { path: '/studies', component: { render: () => h('div', 'Studies') } },
        {
          path: '/signin',
          component: { render: () => h('div', 'Sign in') },
          meta: { public: true, bare: true },
        },
      ],
    });
    await router.push('/private');
    await router.isReady();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const root = node('root');
    const app = createTreeApp({ render: () => h(App) });
    app.use(router);
    app.use(VueQueryPlugin, { queryClient: client });
    app.mount(root);
    await vi.waitFor(() => expect(textOf(root)).toContain('Lykeion'));
    nextIdentity = new Promise<Response>((resolve) => {
      release = resolve;
    });
    changed.listener!();
    await nextTick();
    expect(textOf(root)).not.toContain('Lykeion');
    expect(textOf(root)).toContain('Verifying your account');
    release(
      retry ? new Response(null, { status: 503 }) : Response.json({ id: 'b', onboarded_at: 'yes' }),
    );
    if (retry) {
      await vi.waitFor(() => expect(textOf(root)).toContain('Could not verify your account'));
      expect(textOf(root)).not.toContain('Lykeion');
      nextIdentity = Promise.resolve(Response.json({ id: 'b', onboarded_at: 'yes' }));
      const button = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Try again');
      expect(button).toBeDefined();
      await (button!.props.onClick as () => Promise<void>)();
    }
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/studies'));
    app.unmount();
    client.clear();
  },
);
