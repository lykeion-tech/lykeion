import { afterEach, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createTreeApp, node } from '../../../../../tests/daemon-tree';

vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
vi.mock('../../api/realtime', () => ({
  useRealtimeSync: () => ({ reconnecting: ref(false), lost: ref(false) }),
}));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

it('waits for the new account before accepting a fresh invitation after legacy migration', async () => {
  const data = new Map<string, string>();
  const storage = {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => void data.set(key, value),
  };
  vi.stubGlobal('sessionStorage', storage);
  vi.stubGlobal('localStorage', storage);
  vi.stubGlobal('window', {
    lykeionDesktop: undefined,
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
  data.set('lykeion.tabs', JSON.stringify([{ path: '/studies/private-a', title: 'Private A' }]));
  let release!: (value: Response) => void;
  const firstIdentity = new Promise<Response>((resolve) => {
    release = resolve;
  });
  let identityRequests = 0;
  const acceptedFrom: string[] = [];
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/me') {
      identityRequests++;
      return identityRequests === 1
        ? firstIdentity
        : Response.json({ id: 'b', onboarded_at: 'yes' });
    }
    if (url === '/api/workspaces') return Response.json([{ id: 'workspace-b', name: 'B' }]);
    if (url.includes('/invitations/')) {
      acceptedFrom.push(router.currentRoute.value.fullPath);
      return new Promise<Response>(() => {});
    }
    if (init?.method === 'POST') return new Response(null, { status: 204 });
    return Response.json([]);
  });
  vi.stubGlobal('fetch', fetch);
  const [{ default: App }, { default: AcceptInvite }] = await Promise.all([
    import('../../App.vue'),
    import('./AcceptInvite.vue'),
  ]);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/invite/:token', component: AcceptInvite, props: true, meta: { bare: true } },
      { path: '/studies', component: { render: () => null } },
      { path: '/signin', component: { render: () => null }, meta: { bare: true, public: true } },
    ],
  });
  await router.push('/invite/recipient-token?file=private-a.tex#private-a');
  await router.isReady();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const app = createTreeApp({ render: () => h(App) });
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: client });
  app.mount(node('root'));
  await nextTick();
  expect(fetch.mock.calls.some(([url]) => url.includes('/invitations/'))).toBe(false);
  release(Response.json({ id: 'b', onboarded_at: 'yes' }));
  await vi.waitFor(() =>
    expect(fetch.mock.calls.filter(([url]) => url.includes('/invitations/'))).toHaveLength(1),
  );
  expect(fetch.mock.calls.find(([url]) => url.includes('/invitations/'))?.[0]).toContain(
    'recipient-token',
  );
  expect(acceptedFrom).toEqual(['/invite/recipient-token']);
  app.unmount();
  client.clear();
});
