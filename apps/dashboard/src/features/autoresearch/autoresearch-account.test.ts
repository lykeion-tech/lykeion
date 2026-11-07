import { afterEach, expect, it, vi } from 'vitest';
import { defineComponent, h, nextTick, ref } from 'vue';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createMemoryHistory, createRouter } from 'vue-router';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import {
  authenticationLost,
  authenticationRestored,
  finishAuthenticationRecovery,
} from '../../api/auth-loss';
import { api } from '../../api/client';

const fixture = vi.hoisted(() => ({ detail: null as unknown }));
import { notice } from '../../state/notice';

vi.mock('vue', async (original) => {
  const actual = await original<typeof import('vue')>();
  return {
    ...actual,
    Teleport: {
      setup(_: unknown, { slots }: { slots: { default?: () => unknown } }) {
        return () => actual.h('div', slots.default?.() as never);
      },
    },
  };
});
vi.mock('../../api/queries', () => {
  const query = () => ({
    data: ref([]),
    isSuccess: ref(true),
    isPending: ref(false),
    error: ref(null),
  });
  return {
    useAgents: query,
    useAutoresearch: () => ({ ...query(), data: ref(fixture.detail) }),
    useAutoresearches: query,
    useAutoresearchRuns: query,
    useMe: () => ({ data: ref({ timezone: 'UTC' }) }),
  };
});
vi.mock('../../ui/Modal.vue', () => ({
  default: defineComponent({
    setup:
      (_, { slots }) =>
      () =>
        h('div', slots.default?.()),
  }),
}));
vi.mock('../../ui/AssignPicker.vue', () => ({
  default: defineComponent({
    emits: ['update:modelValue'],
    setup: (_, { emit }) => {
      return () =>
        h(
          'button',
          {
            type: 'button',
            onClick: () => emit('update:modelValue', { type: 'agent', agent_id: 'a', teams: {} }),
          },
          'Choose agent',
        );
    },
  }),
}));
afterEach(() => {
  vi.unstubAllGlobals();
  notice.value = '';
  finishAuthenticationRecovery();
});

it('does not navigate or show an old trigger error after the account changes', async () => {
  vi.stubGlobal('document', {
    addEventListener() {},
    removeEventListener() {},
    querySelector: () => null,
  });
  vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
  vi.stubGlobal('Document', class Document {});
  vi.stubGlobal('ShadowRoot', class ShadowRoot {});
  let release!: (response: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      if (url === '/api/autoresearch') return Promise.resolve(Response.json({ id: 'private-a' }));
      if (url.endsWith('/triggers'))
        return new Promise<Response>((resolve) => {
          release = resolve;
        });
      return Promise.resolve(Response.json([]));
    }),
  );
  const { default: View } = await import('./AutoresearchView.vue');
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: View }],
  });
  await router.push('/autoresearch');
  await router.isReady();
  const root = node('root');
  const app = createTreeApp({ render: () => h(View) });
  app.use(router);
  app.use(VueQueryPlugin, { queryClient: new QueryClient() });
  app.mount(root);
  await nextTick();
  const open = all(root).find(
    (n) => n.tag === 'button' && textOf(n).includes('Start from scratch'),
  )!;
  (open.props.onClick as () => void)();
  await nextTick();
  const choose = all(root).find((n) => n.tag === 'button' && textOf(n) === 'Choose agent')!;
  (choose.props.onClick as () => void)();
  await nextTick();
  const name = all(root).find(
    (n) => n.tag === 'input' && n.props['aria-label'] === 'Autoresearch name',
  )!;
  const runbook = all(root).find(
    (n) => n.tag === 'textarea' && n.props['aria-label'] === 'Runbook',
  )!;
  (name.props['onUpdate:modelValue'] as (value: string) => void)('Private A');
  (runbook.props['onUpdate:modelValue'] as (value: string) => void)('Run');
  await nextTick();
  const form = all(root).find((n) => n.tag === 'form')!;
  const promise = (form.props.onSubmit as (event: Event) => Promise<void>)({
    preventDefault() {},
  } as Event);
  await vi.waitFor(() => expect(release).toBeTypeOf('function'));
  authenticationRestored();
  release(Response.json({ url: 'https://private.example' }));
  await promise;
  await nextTick();
  expect(router.currentRoute.value.path).toBe('/autoresearch');
  expect(notice.value).toBe('');
  app.unmount();
});

it.each(['loss', 'logout', 'switch'])(
  'retains the one-time webhook after failed verification and clears it on %s',
  async (transition) => {
    authenticationRestored();
    finishAuthenticationRecovery();
    vi.stubGlobal('document', {
      addEventListener() {},
      removeEventListener() {},
      querySelector: () => null,
    });
    vi.stubGlobal('window', { addEventListener() {}, removeEventListener() {} });
    vi.stubGlobal('Document', class Document {});
    vi.stubGlobal('ShadowRoot', class ShadowRoot {});
    vi.stubGlobal('location', { origin: 'https://app.example' });
    let verifySucceeds = false;
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url === '/api/autoresearch') return Promise.resolve(Response.json({ id: 'private-a' }));
        if (url.endsWith('/triggers'))
          return Promise.resolve(Response.json({ url: 'https://hooks.example/secret-a' }));
        if (url === '/auth/verify-code')
          return Promise.resolve(
            verifySucceeds
              ? Response.json({})
              : Response.json({ error: 'Invalid code' }, { status: 401 }),
          );
        if (url === '/auth/logout') return Promise.resolve(new Response(null, { status: 204 }));
        return Promise.resolve(Response.json([]));
      }),
    );
    const { default: View } = await import('./AutoresearchView.vue');
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: View }],
    });
    await router.push('/autoresearch');
    await router.isReady();
    const root = node('root');
    const app = createTreeApp({ render: () => h(View) });
    app.use(router);
    app.use(VueQueryPlugin, { queryClient: new QueryClient() });
    app.mount(root);
    await nextTick();
    (
      all(root).find((n) => n.tag === 'button' && textOf(n).includes('Start from scratch'))!.props
        .onClick as () => void
    )();
    await nextTick();
    (
      all(root).find((n) => n.tag === 'button' && textOf(n) === 'Choose agent')!.props
        .onClick as () => void
    )();
    await nextTick();
    (
      all(root).find((n) => n.props['aria-label'] === 'Autoresearch name')!.props[
        'onUpdate:modelValue'
      ] as (s: string) => void
    )('Private A');
    (
      all(root).find((n) => n.props['aria-label'] === 'Runbook')!.props['onUpdate:modelValue'] as (
        s: string,
      ) => void
    )('Run');
    (
      all(root).find((n) => n.tag === 'button' && textOf(n).trim() === 'Webhook')!.props
        .onClick as () => void
    )();
    await nextTick();
    await (all(root).find((n) => n.tag === 'form')!.props.onSubmit as (e: Event) => Promise<void>)({
      preventDefault() {},
    } as Event);
    app.unmount();
    fixture.detail = { id: 'private-a', title: 'Private A', triggers: [] };
    const detailRoot = node('root');
    const detailApp = createTreeApp({ render: () => h(View, { id: 'private-a' }) });
    detailApp.use(router);
    detailApp.use(VueQueryPlugin, { queryClient: new QueryClient() });
    detailApp.mount(detailRoot);
    await nextTick();
    const secret = () => all(detailRoot).find((n) => n.props['aria-label'] === 'Webhook URL');
    expect(secret()?.props.value).toBe('https://hooks.example/secret-a');
    await expect(api('POST', '/auth/verify-code', {})).rejects.toThrow();
    await nextTick();
    expect(secret()?.props.value).toBe('https://hooks.example/secret-a');
    if (transition === 'loss') authenticationLost();
    if (transition === 'logout') await api('POST', '/auth/logout');
    if (transition === 'switch') {
      verifySucceeds = true;
      await api('POST', '/auth/verify-code', {});
    }
    await nextTick();
    expect(secret()).toBeUndefined();
    detailApp.unmount();
    fixture.detail = null;
  },
);
