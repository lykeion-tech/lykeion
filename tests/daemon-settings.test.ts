import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import OnboardingView from '../apps/dashboard/src/views/OnboardingView.vue';
import { createDaemonSession, daemonSessionKey } from '../apps/dashboard/src/lib/daemon-session';
import MachinesView from '../apps/dashboard/src/views/MachinesView.vue';
import { keys } from '../apps/dashboard/src/api/keys';
import { workspaceId } from '../apps/dashboard/src/api/session';
import DaemonSettings from '../apps/dashboard/src/components/DaemonSettings.vue';
import type { DaemonSnapshot, DesktopDaemonBridge } from '../apps/desktop/src/daemon-types';
import { all, createTreeApp, node, textOf } from './daemon-tree';
vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
const snapshot = (extra: Partial<DaemonSnapshot> = {}): DaemonSnapshot => ({
  status: 'running',
  availability: 'available',
  cliAvailable: true,
  error: null,
  pid: 712,
  uptime: 3661,
  daemonID: 'managed-id',
  profile: 'desktop-profile',
  serverURL: 'http://127.0.0.1:8080',
  deviceName: 'Bench',
  workspaceCount: 0,
  os: 'darwin',
  ...extra,
});
const cleanups: (() => void)[] = [];
afterEach(() => {
  cleanups.splice(0).forEach((fn) => fn());
  vi.unstubAllGlobals();
});
async function mount(
  options: {
    browser?: boolean;
    own?: boolean;
    id?: string;
    snapshot?: DaemonSnapshot;
    prefsFail?: boolean;
    machine?: boolean;
  } = {},
) {
  let listener!: (s: DaemonSnapshot) => void;
  const unsubscribe = vi.fn();
  const bridge = {
    getSnapshot: vi.fn().mockResolvedValue(options.snapshot ?? snapshot()),
    getPreferences: vi.fn().mockResolvedValue({ autoStart: true, autoStop: false }),
    setPreferences: options.prefsFail
      ? vi.fn().mockRejectedValue(new Error('disk full'))
      : vi.fn(async (p) => p),
    onStatusChange: vi.fn((fn) => {
      listener = fn;
      return unsubscribe;
    }),
    restart: vi.fn().mockResolvedValue(snapshot()),
    stop: vi.fn().mockResolvedValue(snapshot({ status: 'stopped', pid: null, uptime: null })),
    start: vi.fn(),
  } as unknown as DesktopDaemonBridge;
  vi.stubGlobal('window', options.browser ? {} : { lykeionDesktop: { daemon: bridge } });
  const id = ref(options.id ?? 'managed-id');
  const own = ref(options.own ?? true);
  const app = createTreeApp({
    render: () =>
      options.machine
        ? h(MachinesView, { machineId: id.value, embedded: true })
        : h(DaemonSettings, { machineId: id.value, isOwner: own.value }),
  });
  if (options.machine) {
    workspaceId.value = 'lab';
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
    });
    queryClient.setQueryData(keys.me, { id: options.own === false ? 'colleague' : 'owner' });
    queryClient.setQueryData(keys.members('lab'), []);
    queryClient.setQueryData(keys.runtimes('lab'), [
      {
        id: 'runtime',
        daemon_id: id.value,
        owner_id: 'owner',
        device_name: 'Bench',
        harness: 'claude',
        status: 'offline',
        last_seen_at: '2026-10-26T10:00:00Z',
        active_runs: 0,
        bases: {},
        visibility: 'private',
        shared_with: [],
      },
    ]);
    app.use(VueQueryPlugin, { queryClient });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
    });
    await router.push('/settings');
    app.use(router);
    cleanups.push(() => {
      queryClient.clear();
      workspaceId.value = '';
    });
  }
  const root = node('root');
  app.mount(root);
  cleanups.push(() => app.unmount());
  await vi.waitFor(() => expect(all(root).filter((n) => n.tag === 'dd')).toHaveLength(8));
  await nextTick();
  await nextTick();
  const button = (label: string) =>
    all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === label)!;
  return {
    root,
    id,
    own,
    bridge,
    unsubscribe,
    listener: (s: DaemonSnapshot) => listener(s),
    text: () => textOf(root),
    button,
  };
}
describe('Daemon settings mounted section', () => {
  it('renders all eight live diagnostic facts including zero workspaces', async () => {
    const t = await mount();
    expect(
      all(t.root)
        .filter((n) => n.tag === 'dt')
        .map(textOf),
    ).toEqual([
      'State',
      'Uptime',
      'PID',
      'Daemon ID',
      'Profile',
      'Server URL',
      'Device name',
      'Workspaces',
    ]);
    expect(
      all(t.root)
        .filter((n) => n.tag === 'dd')
        .map(textOf),
    ).toEqual([
      'Running',
      '1h 1m 1s',
      '712',
      'managed-id',
      'desktop-profile',
      'http://127.0.0.1:8080',
      'Bench',
      '0',
    ]);
    expect(t.text()).toContain('Available');
    expect(t.button('Auto-start on launch').props['aria-checked']).toBe(true);
  });
  it.each([
    { browser: true },
    { id: 'remote-id' },
    { own: false },
    { snapshot: snapshot({ daemonID: null }) },
  ])('shows unknown remote/browser/external facts and disabled preferences %j', async (options) => {
    const t = await mount(options);
    expect(
      all(t.root)
        .filter((n) => n.tag === 'dd')
        .map(textOf),
    ).toEqual(Array(8).fill('—'));
    expect(t.button('Auto-start on launch').props.disabled).toBe(true);
    expect(t.text()).not.toContain('desktop-profile');
    expect(t.text()).not.toContain('Available');
    expect(t.bridge.getPreferences).not.toHaveBeenCalled();
  });
  it('retains stopped managed identity while unknown process facts remain dashes', async () => {
    const t = await mount({
      snapshot: snapshot({
        status: 'stopped',
        pid: null,
        uptime: null,
        deviceName: null,
        workspaceCount: null,
      }),
    });
    expect(
      all(t.root)
        .filter((n) => n.tag === 'dd')
        .map(textOf),
    ).toEqual([
      'Stopped',
      '—',
      '—',
      'managed-id',
      'desktop-profile',
      'http://127.0.0.1:8080',
      '—',
      '—',
    ]);
    expect(t.button('Auto-start on launch').props.disabled).toBe(false);
  });
  it('shows development CLI build guidance and disables controls when CLI is missing', async () => {
    const t = await mount({ snapshot: snapshot({ cliAvailable: false }) });
    expect(t.text()).toContain('npm run desktop:dev');
    expect(t.button('Auto-start on launch').props.disabled).toBe(true);
  });
  it('saves immediately without changing the process', async () => {
    const t = await mount();
    await (t.button('Auto-stop on quit').props.onClick as () => Promise<void>)();
    await nextTick();
    expect(t.bridge.setPreferences).toHaveBeenCalledExactlyOnceWith({
      autoStart: true,
      autoStop: true,
    });
    expect(t.bridge.start).not.toHaveBeenCalled();
    expect(t.bridge.stop).not.toHaveBeenCalled();
    expect(t.bridge.restart).not.toHaveBeenCalled();
    expect(t.button('Auto-stop on quit').props['aria-checked']).toBe(true);
  });
  it('keeps last confirmed preferences on a failed save', async () => {
    const t = await mount({ prefsFail: true });
    await (t.button('Auto-start on launch').props.onClick as () => Promise<void>)();
    await nextTick();
    expect(t.button('Auto-start on launch').props['aria-checked']).toBe(true);
    expect(t.text()).toContain('could not be saved');
  });
  it('fences late callbacks and preference results across machine changes and unsubscribes', async () => {
    const t = await mount();
    let resolve!: (p: { autoStart: boolean; autoStop: boolean }) => void;
    vi.mocked(t.bridge.setPreferences).mockReturnValue(new Promise((r) => (resolve = r)));
    const save = (t.button('Auto-stop on quit').props.onClick as () => Promise<void>)();
    t.id.value = 'remote-id';
    await nextTick();
    t.listener(snapshot());
    resolve({ autoStart: false, autoStop: true });
    await save;
    await nextTick();
    expect(t.text()).not.toContain('desktop-profile');
    expect(t.button('Auto-stop on quit').props['aria-checked']).toBe(false);
    cleanups.splice(0).forEach((fn) => fn());
    expect(t.unsubscribe).toHaveBeenCalled();
    t.listener(snapshot());
    await nextTick();
    expect(textOf(t.root)).toBe('');
  });
  it.each(['running', 'stopped'] as const)(
    'enables existing header lifecycle controls for a matching %s daemon',
    async (status) => {
      const t = await mount({ machine: true, snapshot: snapshot({ status }) });
      const find = (text: string) =>
        all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === text)!;
      expect(find('Restart').props.disabled).toBe(false);
      expect(find('Stop').props.disabled).toBe(status === 'stopped');
      expect(find('View logs').props.disabled).toBeDefined();
      expect(t.text()).not.toContain('Remote controls and custom runtimes are unavailable.');
      await (find('Restart').props.onClick as () => Promise<void>)();
      await nextTick();
      expect(t.bridge.restart).toHaveBeenCalledTimes(1);
      await (find('Stop').props.onClick as () => Promise<void>)();
      await nextTick();
      expect(t.bridge.stop).toHaveBeenCalledTimes(1);
      expect(find('Stop').props.disabled).toBe(true);
      const order = t.text();
      expect(order.indexOf('Daemon instructions')).toBeLessThan(
        order.indexOf('Auto-start on launch'),
      );
    },
  );
  it.each([
    { id: 'remote-id' },
    { own: false },
    { snapshot: snapshot({ status: 'unknown' }) },
    { snapshot: snapshot({ cliAvailable: false }) },
  ])('keeps header controls disabled for an unverified context %j', async (options) => {
    const t = await mount({ ...options, machine: true });
    for (const label of ['Restart', 'Stop']) {
      const button = all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === label)!;
      expect(button.props.disabled).toBe(true);
      await (button.props.onClick as () => Promise<void>)();
    }
    expect(t.bridge.restart).not.toHaveBeenCalled();
    expect(t.bridge.stop).not.toHaveBeenCalled();
  });
  it.each([false, true])(
    'onboarding logout fences pending JWT and awaits native clear (failure=%s)',
    async (failure) => {
      vi.stubGlobal('window', {});
      vi.stubGlobal('document', { baseURI: 'http://127.0.0.1/' });
      const fetch = vi.fn(async () => new Response('[]'));
      vi.stubGlobal('fetch', fetch);
      let finishJWT!: (v: { token: string }) => void;
      let finishClear!: () => void;
      const native = {
        configureSession: vi.fn(),
        clearSession: failure
          ? vi.fn().mockRejectedValue(new Error('private-detail'))
          : vi.fn(() => new Promise<void>((resolve) => (finishClear = resolve))),
      };
      const session = createDaemonSession({
        bridge: native as unknown as DesktopDaemonBridge,
        apiOrigin: 'http://127.0.0.1:8080',
        token: () => new Promise((resolve) => (finishJWT = resolve)),
        reportError: () => {},
      });
      const handoff = session.update('alice');
      await Promise.resolve();
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
      });
      client.setQueryData(keys.workspaces, []);
      const router = createRouter({
        history: createMemoryHistory(),
        routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
      });
      await router.push('/onboarding');
      const root = node('root');
      const app = createTreeApp({ render: () => h(OnboardingView) });
      app.use(router);
      app.use(VueQueryPlugin, { queryClient: client });
      app.provide(daemonSessionKey, session);
      app.mount(root);
      cleanups.push(() => {
        app.unmount();
        client.clear();
        session.dispose();
      });
      const logout = all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Log out')!;
      const click = (logout.props.onClick as () => Promise<void>)();
      expect(native.clearSession).toHaveBeenCalledTimes(1);
      expect(fetch.mock.calls).toHaveLength(0);
      finishJWT({ token: 'stale-alice-token' });
      await handoff;
      expect(native.configureSession).not.toHaveBeenCalled();
      if (!failure) finishClear();
      await click;
      await nextTick();
      if (failure) {
        expect(fetch.mock.calls).toHaveLength(0);
        expect(textOf(root)).toContain('could not be stopped');
        expect(textOf(root)).not.toContain('private-detail');
      } else {
        expect(fetch).toHaveBeenCalledWith(
          '/auth/logout',
          expect.objectContaining({ method: 'POST' }),
        );
        expect(router.currentRoute.value.path).toBe('/signin');
      }
    },
  );
});
