import { afterEach, expect, it, vi } from 'vitest';
import { defineComponent, h, nextTick, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';

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

function identity(id: string) {
  return {
    id,
    name: id,
    email: id + '@lab.test',
    timezone: 'UTC',
    avatar_url: '',
    created_at: '2026-01-01',
    google_sub: null,
    onboarded_at: '2026-01-01',
    career_stage: '',
  };
}
function held<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
async function privateEditor(
  initialMe?: Promise<Response>,
  replacement?: {
    storage: Map<string, string>;
    clear: Promise<object>;
    sameOwner?: boolean;
    finalOwner?: boolean;
    nativeRead?: boolean;
  },
) {
  const storage = replacement?.storage ?? new Map<string, string>();
  const adapter = {
    getItem: (k: string) => storage.get(k) ?? null,
    setItem: (k: string, v: string) => {
      storage.set(k, v);
    },
    removeItem: (k: string) => {
      storage.delete(k);
    },
  };
  vi.stubGlobal('localStorage', adapter);
  vi.stubGlobal('sessionStorage', adapter);
  const native = {
    configureSession: vi.fn(async (_input: { userID: string }) => ({})),
    clearSession: vi.fn(async () => ({})),
    getSnapshot: vi.fn(async () => ({ availability: 'available' })),
  };
  if (replacement) native.clearSession.mockReturnValueOnce(replacement.clear);
  let nativeReads = 0;
  const finishVerification = vi.fn(
    (_id: string | null, receipt?: string, retaining?: boolean) =>
      (!replacement?.nativeRead || receipt === 'native-read-' + nativeReads) &&
      (!retaining || replacement?.finalOwner !== false),
  );
  vi.stubGlobal('window', {
    location: { origin: 'https://app.example' },
    lykeionDesktop: {
      daemon: native,
      verificationPending: Boolean(replacement),
      ...(replacement?.nativeRead
        ? {
            readVerification: async () => {
              const res = await probe();
              if (!res.ok && res.status !== 401) throw new Error('Identity unavailable');
              return {
                receipt: 'native-read-' + ++nativeReads,
                user: res.status === 401 ? null : await res.json(),
              };
            },
          }
        : {}),
      finishVerification,
      canRetainVerification: async () =>
        replacement?.sameOwner === true &&
        !(replacement.finalOwner === false && finishVerification.mock.calls.length),
    },
    matchMedia: () => ({ matches: false }),
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
  const { default: App } = await import('../../App.vue');
  const auth = await import('../../api/auth-loss');
  const { api, desktopGoogleSignIn } = await import('../../api/client');
  const { workspaceId } = await import('../../api/session');
  const verify = held<Response>();
  const probe = vi.fn(async () => Response.json(identity('a')));
  if (replacement && !replacement.sameOwner)
    probe.mockImplementation(async () => Response.json(identity('b')));
  const gatedCalls: string[] = [];
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/auth/verify-code') return verify.promise;
    if (url === '/api/me' && init?.cache === 'no-store') {
      expect(init.credentials).toBe('same-origin');
      expect(init.headers).toBeUndefined();
      return probe();
    }
    if (url === '/auth/logout')
      return new Response(null, { status: 204, headers: { 'X-Lykeion-Local-Logout': '1' } });
    if (auth.verifying.value) gatedCalls.push(url);
    if (url === '/api/me') return initialMe ?? Response.json(identity('a'));
    if (url === '/api/workspaces') return Response.json([{ id: 'w', name: 'Workspace' }]);
    if (url === '/api/cli-token') return Response.json({ token: 'ephemeral' });
    return Response.json([]);
  });
  vi.stubGlobal('fetch', fetch);
  const opens = vi.fn();
  const Editor = defineComponent({
    setup() {
      opens();
      const draft = ref('');
      return () =>
        h('input', {
          value: draft.value,
          onInput: (s: string) => {
            draft.value = s;
          },
        });
    },
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/editor', component: Editor, meta: { bare: true } },
      { path: '/studies', component: { render: () => h('span', 'Studies') }, meta: { bare: true } },
      {
        path: '/signin',
        component: { render: () => h('span', 'Sign in') },
        meta: { bare: true, public: true },
      },
      {
        path: '/invite/:token',
        component: (await import('./AcceptInvite.vue')).default,
        props: true,
        meta: { bare: true },
      },
    ],
  });
  await router.push('/editor');
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const mount = ref(0);
  const app = createTreeApp({ render: () => h(App, { key: mount.value }) });
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: client });
  const root = node('root');
  app.mount(root);
  await router.isReady();
  const editor = () => all(root).find((n) => n.tag === 'input');
  if (!initialMe && !replacement) {
    await vi.waitFor(() => expect(editor()).toBeDefined());
    await vi.waitFor(() => expect(native.configureSession).toHaveBeenCalledTimes(1));
  }
  const original = editor()!;
  if (original) (original.props.onInput as (v: string) => void)('Unsubmitted edit');
  client.setQueryData(['private-a'], 'one-time value');
  const committed = vi.fn();
  const stop = auth.onCredentialsCommitted(committed);
  const retry = () => {
    const button = all(root).find((n) => n.tag === 'button' && textOf(n) === 'Try again');
    expect(button).toBeDefined();
    (button!.props.onClick as () => void)();
  };
  const dispose = () => {
    stop();
    app.unmount();
    client.clear();
  };
  return {
    auth,
    api,
    desktopGoogleSignIn,
    client,
    router,
    root,
    native,
    verify,
    probe,
    gatedCalls,
    editor,
    original,
    opens,
    committed,
    finishVerification,
    retry,
    remount: async () => {
      mount.value++;
      await nextTick();
    },
    dispose,
    fetch,
    workspaceId,
    storage,
  };
}

it('keeps the same mounted editor, cached private data and daemon through verification retries', async () => {
  const t = await privateEditor();
  try {
    t.probe
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(new Response(null, { status: 503 }))
      .mockResolvedValueOnce(Response.json({ id: 'a' }));
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 401 }));
    await expect(verify).rejects.toMatchObject({ status: 401 });
    for (let i = 1; i <= 3; i++) {
      await vi.waitFor(() => expect(t.auth.verification.value?.stage).toBe('retry'));
      expect(t.probe).toHaveBeenCalledTimes(i);
      expect(all(t.root).some((n) => n.tag === 'dialog')).toBe(true);
      expect(t.editor()).toBe(t.original);
      expect(t.editor()?.props.value).toBe('Unsubmitted edit');
      expect(t.workspaceId.value).toBe('w');
      expect(t.client.getQueryData(['private-a'])).toBe('one-time value');
      expect(t.native.clearSession).not.toHaveBeenCalled();
      expect(t.committed).not.toHaveBeenCalled();
      t.retry();
    }
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(t.opens).toHaveBeenCalledOnce();
    expect(t.editor()).toBe(t.original);
    expect(t.original.props.value).toBe('Unsubmitted edit');
    expect(t.native.configureSession).toHaveBeenCalledTimes(1);
    expect(t.committed).not.toHaveBeenCalled();
    expect(t.gatedCalls).toEqual([]);
  } finally {
    t.dispose();
  }
});

it('preserves the verified owner when the App remounts while the OTP is pending', async () => {
  const t = await privateEditor();
  try {
    const lost = vi.fn();
    const stop = t.auth.onAuthenticationLost(lost);
    t.client.setQueryData(['retained-draft', 'a'], t.original.props.value);
    const verify = t.api('POST', '/auth/verify-code', {});
    await t.remount();
    t.verify.resolve(new Response(null, { status: 401 }));
    await expect(verify).rejects.toMatchObject({ status: 401 });
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(lost).not.toHaveBeenCalled();
    expect(t.router.currentRoute.value.path).toBe('/editor');
    expect(t.client.getQueryData(['retained-draft', 'a'])).toBe('Unsubmitted edit');
    expect(t.client.getQueryData(['private-a'])).toBe('one-time value');
    expect(t.native.clearSession).not.toHaveBeenCalled();
    expect(t.committed).not.toHaveBeenCalled();
    stop();
  } finally {
    t.dispose();
  }
});

it('admits the verified identity when an OTP is rejected before initial me resolves', async () => {
  const initial = held<Response>();
  const t = await privateEditor(initial.promise);
  try {
    const lost = vi.fn();
    const stop = t.auth.onAuthenticationLost(lost);
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 401 }));
    await expect(verify).rejects.toMatchObject({ status: 401 });
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    initial.resolve(Response.json(identity('stale')));
    await nextTick();
    expect(lost).not.toHaveBeenCalled();
    expect(t.client.getQueryData(['me'])).toMatchObject({ id: 'a' });
    expect(t.router.currentRoute.value.path).not.toBe('/signin');
    stop();
  } finally {
    t.dispose();
  }
});

it('keeps B gated after successful headers and an aborted body until native clear and navigation succeed', async () => {
  const t = await privateEditor();
  try {
    t.probe.mockImplementation(async () => Response.json(identity('b')));
    const clear = held<object>();
    t.native.clearSession
      .mockRejectedValueOnce(new Error('stop failed'))
      .mockReturnValueOnce(clear.promise);
    let abortBody!: () => void;
    const body = new ReadableStream({
      start(controller) {
        abortBody = () => controller.error(new Error('body aborted'));
      },
    });
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(body));
    await vi.waitFor(() => expect(t.auth.verification.value?.accepted).toBe(true));
    expect(t.editor()).toBe(t.original);
    abortBody();
    await expect(verify).rejects.toThrow('body aborted');
    await vi.waitFor(() => expect(t.auth.verification.value?.stage).toBe('retry'));
    expect(t.native.clearSession).toHaveBeenCalledTimes(1);
    expect(t.committed).not.toHaveBeenCalled();
    t.retry();
    await vi.waitFor(() => expect(t.native.clearSession).toHaveBeenCalledTimes(2));
    expect(t.editor()).toBe(t.original);
    expect(t.native.configureSession).toHaveBeenCalledTimes(1);
    await expect(t.api('GET', '/api/workspaces')).rejects.toMatchObject({ name: 'AbortError' });
    clear.resolve({});
    await vi.waitFor(() => expect(t.native.configureSession).toHaveBeenCalledTimes(2));
    expect(t.native.configureSession.mock.calls[1]?.[0].userID).toBe('b');
    expect(t.auth.verifying.value).toBe(false);
    expect(t.committed).toHaveBeenCalledOnce();
    expect(t.router.currentRoute.value.path).toBe('/studies');
    expect(t.editor()).toBeUndefined();
    expect(t.client.getQueryData(['private-a'])).toBeUndefined();
    expect(t.client.getQueryData(['me'])).toMatchObject({ id: 'b' });
    expect(t.gatedCalls).toEqual([]);
  } finally {
    t.dispose();
  }
});

it('restores quarantine in a replacement document after native clearance failed', async () => {
  const a = await privateEditor();
  a.native.clearSession.mockRejectedValueOnce(new Error('stop failed'));
  a.probe.mockResolvedValueOnce(Response.json(identity('b')));
  const verify = a.api('POST', '/auth/verify-code', {});
  a.verify.resolve(new Response(null, { status: 204 }));
  await verify;
  await vi.waitFor(() => expect(a.auth.verification.value?.stage).toBe('retry'));
  expect(a.finishVerification).not.toHaveBeenCalled();
  a.dispose();
  vi.resetModules();
  const clear = held<object>();
  const b = await privateEditor(undefined, { storage: a.storage, clear: clear.promise });
  try {
    await vi.waitFor(() => expect(b.native.clearSession).toHaveBeenCalledOnce());
    expect(b.auth.verifying.value).toBe(true);
    expect(b.native.configureSession).not.toHaveBeenCalled();
    expect(b.editor()).toBeUndefined();
    await expect(b.api('GET', '/api/workspaces')).rejects.toMatchObject({ name: 'AbortError' });
    clear.resolve({});
    await vi.waitFor(() => expect(b.auth.verifying.value).toBe(false));
    expect(b.client.getQueryData(['me'])).toMatchObject({ id: 'b' });
    expect(b.router.currentRoute.value.path).toBe('/studies');
    expect(b.finishVerification).toHaveBeenCalledOnce();
    expect(b.gatedCalls).toEqual([]);
  } finally {
    b.dispose();
  }
});

it('does not finish a newer recovery when an older released refetch settles', async () => {
  const t = await privateEditor();
  try {
    const pending = held<void>();
    const refetch = vi.spyOn(t.client, 'refetchQueries').mockReturnValueOnce(pending.promise);
    t.probe.mockResolvedValueOnce(Response.json(identity('b')));
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 204 }));
    await verify;
    await vi.waitFor(() => expect(refetch).toHaveBeenCalledOnce());
    t.auth.authenticationRestored();
    pending.resolve();
    await nextTick();
    expect(t.auth.authenticationIsRestoring()).toBe(true);
  } finally {
    t.dispose();
  }
});

it('rejects a stale B identity when its response arrives after a newer account epoch', async () => {
  const t = await privateEditor();
  try {
    const old = held<Response>();
    t.probe.mockReturnValueOnce(old.promise);
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 204 }));
    await verify;
    await vi.waitFor(() => expect(t.probe).toHaveBeenCalledOnce());
    t.auth.advanceAuthenticationEpoch();
    old.resolve(Response.json(identity('b')));
    await nextTick();
    expect(t.auth.verification.value?.stage).toBe('retry');
    expect(t.native.clearSession).not.toHaveBeenCalled();
    t.retry();
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(t.editor()).toBe(t.original);
    expect(t.committed).not.toHaveBeenCalled();
    expect(t.client.getQueryData(['me'])).toMatchObject({ id: 'a' });
  } finally {
    t.dispose();
  }
});

it('clears private state and enters the actual signed-out flow on a verified 401', async () => {
  const t = await privateEditor();
  try {
    t.probe.mockResolvedValueOnce(new Response(null, { status: 401 }));
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 401 }));
    await expect(verify).rejects.toMatchObject({ status: 401 });
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(t.auth.authenticationIsLost()).toBe(true);
    expect(t.router.currentRoute.value.path).toBe('/signin');
    expect(t.editor()).toBeUndefined();
    expect(t.workspaceId.value).toBe('');
    expect(t.client.getQueryData(['private-a'])).toBeUndefined();
    expect(t.native.clearSession).toHaveBeenCalledTimes(1);
    expect(t.gatedCalls).toEqual([]);
  } finally {
    t.dispose();
  }
});

it('starts the saved invitation only after fresh sign-in completes its daemon barrier', async () => {
  const t = await privateEditor();
  try {
    await t.api('POST', '/auth/logout');
    await vi.waitFor(() => expect(t.router.currentRoute.value.path).toBe('/signin'));
    t.storage.set('lykeion.invite', 'new-token');
    const clear = held<object>();
    t.native.clearSession.mockReturnValueOnce(clear.promise);
    t.probe.mockResolvedValueOnce(Response.json(identity('b')));
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 204 }));
    await verify;
    await vi.waitFor(() => expect(t.native.clearSession).toHaveBeenCalledTimes(2));
    const invitationCalls = () =>
      t.fetch.mock.calls.filter(([url]) => url.includes('/invitations/'));
    expect(invitationCalls()).toHaveLength(0);
    clear.resolve({});
    await vi.waitFor(() => expect(invitationCalls()).toHaveLength(1));
    expect(invitationCalls()[0]?.[0]).toBe('/api/invitations/new-token/accept');
    expect(t.storage.get('lykeion.invite')).toBe('');
    expect(t.gatedCalls).toEqual([]);
  } finally {
    t.dispose();
  }
});

it('cannot restore an old server session after failed verification following local-only logout', async () => {
  const t = await privateEditor();
  try {
    await expect(t.api('POST', '/auth/logout')).resolves.toEqual({ localOnly: true });
    await vi.waitFor(() => expect(t.router.currentRoute.value.path).toBe('/signin'));
    const verify = t.api('POST', '/auth/verify-code', {});
    t.verify.resolve(new Response(null, { status: 401 }));
    await expect(verify).rejects.toMatchObject({ status: 401 });
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(t.auth.authenticationIsLost()).toBe(true);
    expect(t.router.currentRoute.value.path).toBe('/signin');
    expect(t.client.getQueryData(['private-a'])).toBeUndefined();
    expect(t.native.configureSession).toHaveBeenCalledTimes(1);
  } finally {
    t.dispose();
  }
});

it('restores the verified owner and its selected tab across document replacement without stopping the daemon', async () => {
  const storage = new Map([
    ['lykeion.verification', JSON.stringify({ accepted: true, wasLost: false })],
    [
      'lykeion.workspace-tabs',
      JSON.stringify({
        account: 'a',
        pendingSwitch: false,
        selected: 9,
        tabs: [
          {
            id: 9,
            path: '/editor',
            title: 'A draft',
            icon: 'tasks',
            history: ['/editor'],
            index: 0,
          },
        ],
      }),
    ],
  ]);
  const t = await privateEditor(undefined, {
    storage,
    clear: Promise.resolve({}),
    sameOwner: true,
    nativeRead: true,
  });
  try {
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(t.native.clearSession).not.toHaveBeenCalled();
    expect(t.router.currentRoute.value.path).toBe('/editor');
    expect(JSON.parse(storage.get('lykeion.workspace-tabs')!)).toMatchObject({
      account: 'a',
      selected: 9,
    });
    expect(t.client.getQueryData(['me'])).toMatchObject({ id: 'a' });
    expect(t.finishVerification).toHaveBeenCalledWith('a', 'native-read-1', true);
    expect(t.gatedCalls).toEqual([]);
  } finally {
    t.dispose();
  }
});

it.each([
  { pendingSwitch: true, finalOwner: true },
  { pendingSwitch: false, finalOwner: false },
])(
  'keeps the replacement document gated through native clear with $pendingSwitch pending switch and $finalOwner final proof',
  async ({ pendingSwitch, finalOwner }) => {
    const storage = new Map([
      ['lykeion.verification', JSON.stringify({ accepted: true, wasLost: false })],
      [
        'lykeion.workspace-tabs',
        JSON.stringify({ account: 'a', selected: 9, pendingSwitch, tabs: [] }),
      ],
    ]);
    const clearing = held<object>();
    const t = await privateEditor(undefined, {
      storage,
      clear: clearing.promise,
      sameOwner: true,
      finalOwner,
    });
    try {
      await vi.waitFor(() => expect(t.native.clearSession).toHaveBeenCalledOnce());
      expect(t.auth.verifying.value).toBe(true);
      expect(t.editor()).toBeUndefined();
      expect(t.gatedCalls).toEqual([]);
      clearing.resolve({});
      await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
      expect(t.router.currentRoute.value.path).toBe('/studies');
      expect(JSON.parse(storage.get('lykeion.workspace-tabs')!)).toMatchObject({
        account: 'a',
        selected: 0,
      });
    } finally {
      t.dispose();
    }
  },
);

it('reads the new identity after logout invalidates the cookie receipt during native clear', async () => {
  const storage = new Map([
    ['lykeion.verification', JSON.stringify({ accepted: true, wasLost: false })],
  ]);
  const clearing = held<object>();
  const t = await privateEditor(undefined, { storage, clear: clearing.promise, nativeRead: true });
  try {
    await vi.waitFor(() => expect(t.native.clearSession).toHaveBeenCalledOnce());
    t.finishVerification.mockImplementationOnce(() => false);
    t.probe.mockResolvedValueOnce(new Response(null, { status: 401 }));
    clearing.resolve({});
    await vi.waitFor(() => expect(t.auth.verifying.value).toBe(false));
    expect(t.probe).toHaveBeenCalledTimes(2);
    expect(t.finishVerification.mock.calls).toEqual([
      ['b', 'native-read-1', false],
      [null, 'native-read-2', false],
    ]);
    expect(t.router.currentRoute.value.path).toBe('/signin');
    expect(t.editor()).toBeUndefined();
    expect(t.client.getQueryData(['me'])).toBeUndefined();
    expect(t.native.configureSession).not.toHaveBeenCalled();
    expect(t.gatedCalls).toEqual([]);
  } finally {
    t.dispose();
  }
});

it('keeps private state gated and offers cancellation while Google sign-in is pending', async () => {
  const t = await privateEditor();
  try {
    const pending = held<void>();
    const reload = vi.fn();
    const cancel = vi.fn(() => pending.reject(new Error('cancelled')));
    Object.assign(window.location, { reload });
    Object.assign(window.lykeionDesktop!, {
      googleSignIn: () => pending.promise,
      cancelGoogleSignIn: cancel,
      reloadAfterAuthentication: () => {
        reload();
        return true;
      },
    });
    const signin = t.desktopGoogleSignIn();
    await vi.waitFor(() => expect(t.auth.verification.value?.google).toBe(true));
    await vi.waitFor(() =>
      expect(all(t.root).some((n) => n.tag === 'button' && textOf(n) === 'Cancel sign-in')).toBe(
        true,
      ),
    );
    expect(t.client.getQueryData(['private-a'])).toBe('one-time value');
    expect(t.native.clearSession).not.toHaveBeenCalled();
    const button = all(t.root).find((n) => n.tag === 'button' && textOf(n) === 'Cancel sign-in')!;
    (button.props.onClick as () => void)();
    await expect(signin).rejects.toThrow('cancelled');
    expect(cancel).toHaveBeenCalledOnce();
    expect(reload).toHaveBeenCalledOnce();
    expect(t.native.clearSession).not.toHaveBeenCalled();
  } finally {
    t.dispose();
  }
});
