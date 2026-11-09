import { afterEach, describe, expect, it, vi } from 'vitest';
import { h, nextTick, ref, watch } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import OnboardingView from '../workspace/OnboardingView.vue';
import { createDaemonSession, daemonSessionKey } from '../../lib/daemon-session';
import MachinesView from './MachinesView.vue';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import DaemonSettings from './DaemonSettings.vue';
import type { DaemonSnapshot, DesktopDaemonBridge } from '../../../../desktop/src/daemon-types';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
const snapshot = (extra: Partial<DaemonSnapshot> = {}): DaemonSnapshot => ({
  authenticatedUserID: 'owner',
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
    autoStart?: boolean;
    machine?: boolean;
    empty?: boolean;
    collection?: boolean;
    desktopProfile?: boolean;
  } = {},
) {
  const listeners = new Set<(s: DaemonSnapshot) => void>();
  const unsubscribe = vi.fn();
  let latestSnapshot = options.snapshot ?? snapshot();
  const publish = (value: DaemonSnapshot) => {
    latestSnapshot = value;
    for (const listener of [...listeners]) listener(value);
  };
  const bridge = {
    getSnapshot: vi.fn(async () => latestSnapshot),
    getPreferences: vi
      .fn()
      .mockResolvedValue({ autoStart: options.autoStart ?? true, autoStop: false }),
    setPreferences: options.prefsFail
      ? vi.fn().mockRejectedValue(new Error('disk full'))
      : vi.fn(async (p) => p),
    onStatusChange: vi.fn((fn) => {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
        unsubscribe();
      };
    }),
    restart: vi.fn().mockResolvedValue(snapshot()),
    stop: vi.fn().mockResolvedValue(snapshot({ status: 'stopped', pid: null, uptime: null })),
    start: vi.fn(async () => {
      const value = snapshot({ daemonID: 'new-id' });
      publish(value);
      return value;
    }),
  } as unknown as DesktopDaemonBridge;
  vi.stubGlobal('window', options.browser ? {} : { lykeionDesktop: { daemon: bridge } });
  const id = ref<string | undefined>(options.collection ? undefined : (options.id ?? 'managed-id'));
  const own = ref(options.own ?? true);
  let queryClient: QueryClient | undefined;
  let router: ReturnType<typeof createRouter> | undefined;
  const app = createTreeApp({
    render: () =>
      options.machine
        ? h(MachinesView, { machineId: id.value, embedded: true })
        : h(DaemonSettings, { machineId: id.value!, isOwner: own.value, accountId: 'owner' }),
  });
  if (options.machine) {
    workspaceId.value = 'lab';
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
    });
    queryClient.setQueryData(keys.me, { id: options.own === false ? 'colleague' : 'owner' });
    queryClient.setQueryData(keys.members('lab'), []);
    queryClient.setQueryData(
      keys.runtimes('lab'),
      options.empty
        ? []
        : [
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
          ],
    );
    app.use(VueQueryPlugin, { queryClient });
    router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
    });
    await router.push({
      path: '/settings',
      query: {
        tab: 'machines',
        ...(id.value ? { machine: id.value } : {}),
        ...(options.desktopProfile ? { desktopProfile: '1' } : {}),
      },
    });
    app.use(router);
    cleanups.push(
      watch(
        () => router!.currentRoute.value.query.machine,
        (value) => {
          id.value = typeof value === 'string' ? value : undefined;
        },
        { flush: 'sync' },
      ),
    );
    cleanups.push(() => {
      queryClient!.clear();
      workspaceId.value = '';
    });
  }
  const root = node('root');
  app.mount(root);
  cleanups.push(() => app.unmount());
  if (!options.collection && !(options.machine && options.empty))
    await vi.waitFor(() => expect(all(root).filter((n) => n.tag === 'dd')).toHaveLength(8));
  else await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  await nextTick();
  const button = (label: string) =>
    all(root).find((n) => n.tag === 'button' && n.props['aria-label'] === label)!;
  return {
    root,
    queryClient,
    router,
    id,
    own,
    bridge,
    unsubscribe,
    listener: publish,
    text: () => textOf(root),
    button,
  };
}
describe('Daemon settings mounted section', () => {
  it('keeps a verified unregistered desktop profile reachable through Start and registration', async () => {
    const stopped = snapshot({
      status: 'stopped',
      daemonID: null,
      pid: null,
      uptime: null,
      deviceName: null,
      workspaceCount: null,
    });
    const t = await mount({
      machine: true,
      empty: true,
      collection: true,
      snapshot: stopped,
      autoStart: false,
    });
    await vi.waitFor(() => expect(t.text()).toContain('This machine'));
    const link = all(t.root).find((n) => n.tag === 'a' && textOf(n).includes('This machine'))!;
    expect(link.props.href).toContain('machine=local:placeholder');
    expect(link.props.href).toContain('desktopProfile=1');
    expect(t.text()).not.toContain('0 runtimes');
    await t.router!.replace({
      path: '/settings',
      query: { tab: 'machines', machine: 'local:placeholder', desktopProfile: '1' },
    });
    t.id.value = 'local:placeholder';
    await nextTick();
    await vi.waitFor(() => expect(t.button('Auto-start on launch')?.props.disabled).toBe(false));
    expect(t.button('Auto-start on launch').props['aria-checked']).toBe(false);
    const find = (label: string) =>
      all(t.root).find((n) => n.tag === 'button' && textOf(n).trim() === label)!;
    expect(find('Start').props.disabled).toBe(false);
    expect(find('Rename')).toBeUndefined();
    expect(find('Remove')).toBeUndefined();
    await (t.button('Auto-start on launch').props.onClick as () => Promise<void>)();
    expect(t.bridge.setPreferences).toHaveBeenCalledExactlyOnceWith({
      autoStart: true,
      autoStop: false,
    });
    await (find('Start').props.onClick as () => Promise<void>)();
    expect(t.bridge.start).toHaveBeenCalledTimes(1);
    expect(t.router!.currentRoute.value.query.machine).not.toBe('new-id');
    t.listener(snapshot({ daemonID: 'new-id' }));
    await nextTick();
    expect(t.text()).not.toContain('Computer not found');
    expect(t.text()).toContain('new-id');
    expect(t.text()).not.toContain('0 runtimes');
    t.queryClient!.setQueryData(keys.runtimes('lab'), [
      {
        id: 'new-runtime',
        daemon_id: 'new-id',
        owner_id: 'owner',
        device_name: 'Bench',
        harness: 'claude',
        status: 'online',
        last_seen_at: '2026-10-26T10:00:00Z',
        active_runs: 0,
        bases: {},
        visibility: 'private',
        shared_with: [],
      },
    ]);
    await vi.waitFor(() => expect(t.router!.currentRoute.value.query.machine).toBe('new-id'));
    expect(t.router!.currentRoute.value.query.desktopProfile).toBeUndefined();
    expect(t.text()).not.toContain('Computer not found');
    expect(t.text()).toContain('1 runtime');
  });
  it.each([
    { browser: true },
    { snapshot: snapshot({ authenticatedUserID: null, daemonID: null }) },
    { snapshot: snapshot({ authenticatedUserID: 'previous-owner', daemonID: null }) },
    { snapshot: snapshot({ availability: 'unavailable', daemonID: null }) },
  ])(
    'does not create a controllable local row without verified native ownership %j',
    async (options) => {
      const t = await mount({ machine: true, empty: true, collection: true, ...options });
      expect(t.text()).not.toContain('This machine');
      expect(t.bridge.getPreferences).not.toHaveBeenCalled();
      await t.router!.replace({
        path: '/settings',
        query: { tab: 'machines', machine: 'local:placeholder', desktopProfile: '1' },
      });
      t.id.value = 'local:placeholder';
      await nextTick();
      expect(t.text()).toContain('Computer not found');
      expect(t.bridge.start).not.toHaveBeenCalled();
    },
  );
  it('explains an unavailable desktop platform before considering machine ownership', async () => {
    const t = await mount({
      snapshot: snapshot({
        availability: 'unavailable',
        authenticatedUserID: null,
        daemonID: null,
      }),
    });
    expect(t.text()).toContain(
      'Daemon controls require the macOS or Linux desktop development preview.',
    );
    expect(t.text()).not.toContain('This machine is remote');
    expect(t.bridge.getPreferences).not.toHaveBeenCalled();
  });
  it('revokes the previous account row before exposing the newly verified stopped profile', async () => {
    const t = await mount({ machine: true, empty: true, collection: true });
    expect(t.text()).toContain('Bench');
    t.queryClient!.setQueryData(keys.me, { id: 'next-owner' });
    await nextTick();
    expect(t.text()).not.toContain('Bench');
    t.listener(snapshot());
    await nextTick();
    expect(t.text()).not.toContain('Bench');
    t.listener(
      snapshot({
        authenticatedUserID: 'next-owner',
        daemonID: null,
        status: 'stopped',
        deviceName: null,
        pid: null,
        uptime: null,
        workspaceCount: null,
      }),
    );
    await nextTick();
    expect(t.text()).toContain('This machine');
    await t.router!.replace({
      path: '/settings',
      query: { tab: 'machines', machine: 'local:placeholder', desktopProfile: '1' },
    });
    t.id.value = 'local:placeholder';
    await nextTick();
    await vi.waitFor(() => expect(t.button('Auto-start on launch')?.props.disabled).toBe(false));
    t.listener(snapshot({ authenticatedUserID: null }));
    await nextTick();
    expect(t.text()).toContain('Computer not found');
    expect(t.text()).not.toContain('desktop-profile');
  });
  it('preserves local recovery with a retained native ID and no server runtimes', async () => {
    const t = await mount({
      machine: true,
      empty: true,
      id: 'local:managed-id',
      desktopProfile: true,
      snapshot: snapshot({ status: 'stopped', deviceName: null }),
    });
    await vi.waitFor(() => expect(t.button('Auto-start on launch')?.props.disabled).toBe(false));
    expect(t.text()).toContain('This machine');
    expect(t.text()).not.toContain('0 runtimes');
  });
  it.each(['local:placeholder', 'local:managed-id'])(
    'keeps backend daemon ID %s distinct from the synthetic profile locator',
    async (id) => {
      const t = await mount({ machine: true, id });
      expect(t.text()).toContain('1 runtime');
      expect(t.text()).not.toContain('desktop-profile');
      expect(t.button('Auto-start on launch').props.disabled).toBe(true);
      await t.router!.replace({
        path: '/settings',
        query: { tab: 'machines', machine: id, desktopProfile: '1' },
      });
      await nextTick();
      await vi.waitFor(() => expect(t.button('Auto-start on launch')?.props.disabled).toBe(false));
      expect(t.text()).not.toContain('1 runtime');
      expect(t.text()).toContain('desktop-profile');
    },
  );
  it('never promotes a same-name remote machine to the desktop profile', async () => {
    const t = await mount({ machine: true, id: 'remote-id' });
    expect(t.button('Auto-start on launch').props.disabled).toBe(true);
    expect(t.text()).not.toContain('desktop-profile');
  });
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
    { snapshot: snapshot({ authenticatedUserID: null }) },
    { snapshot: snapshot({ authenticatedUserID: 'previous-owner' }) },
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
    expect(t.text()).toContain('Missing');
    expect(t.text()).not.toContain('remote or managed outside');
    expect(t.text()).toContain('desktop-profile');
    expect(t.button('Auto-start on launch').props.disabled).toBe(true);
    expect(t.bridge.start).not.toHaveBeenCalled();
    expect(t.bridge.restart).not.toHaveBeenCalled();
  });
  it('reloads preferences when verified ownership is restored on the same registered machine', async () => {
    const t = await mount();
    t.listener(snapshot({ authenticatedUserID: null }));
    await nextTick();
    expect(t.button('Auto-start on launch').props.disabled).toBe(true);
    t.listener(snapshot());
    await nextTick();
    await vi.waitFor(() => expect(t.button('Auto-start on launch').props.disabled).toBe(false));
    expect(t.bridge.getPreferences).toHaveBeenCalledTimes(2);
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
      client.setQueryData(keys.laboratories, []);
      client.setQueryData(keys.me, { id: 'alice', name: 'Alice' });
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
