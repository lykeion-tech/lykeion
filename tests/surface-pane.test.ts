import { expect, it } from 'vitest';
import { h, nextTick, ref } from 'vue';
import SurfacePane from '../apps/dashboard/src/components/SurfacePane.vue';
import { all, createTreeApp, node, textOf } from './daemon-tree';

it('opens a single surface, returns to the chooser, and restores it after closing', async () => {
  Object.assign(globalThis, {
    window: { innerWidth: 1200, addEventListener() {}, removeEventListener() {} },
  });
  const open = ref(true),
    active = ref<string | null>(null),
    focused = ref(false);
  const app = createTreeApp({
    render: () =>
      h(
        SurfacePane,
        {
          open: open.value,
          active: active.value,
          focused: focused.value,
          surfaces: [{ id: 'files', label: 'Files', icon: 'folder' }],
          'onUpdate:open': (v: boolean) => {
            open.value = v;
          },
          'onUpdate:active': (v: string | null) => {
            active.value = v;
          },
          'onUpdate:focused': (v: boolean) => {
            focused.value = v;
          },
        },
        { default: ({ active }: { active: string }) => h('p', `Content ${active}`) },
      ),
  });
  const root = node('root');
  app.mount(root);
  const click = async (label: string) => {
    const button = all(root).find(
      (n) => n.tag === 'button' && (n.props['aria-label'] === label || textOf(n).trim() === label),
    );
    expect(button).toBeTruthy();
    (button!.props.onClick as () => void)();
    await nextTick();
  };
  expect(textOf(root)).toContain('Open a surface');
  await click('Files');
  expect(textOf(root)).toContain('Content files');
  await click('Maximize pane');
  expect(focused.value).toBe(true);
  await click('Close pane');
  expect(open.value).toBe(false);
  open.value = true;
  await nextTick();
  expect(textOf(root)).toContain('Content files');
  await click('Back to surfaces');
  expect(textOf(root)).toContain('Open a surface');
  app.unmount();
});

it('exposes a keyboard resizable separator and dismissible narrow overlay', async () => {
  const events = new Map<string, (e: any) => void>();
  const oldWindow = globalThis.window;
  Object.assign(globalThis, {
    window: {
      innerWidth: 900,
      addEventListener: (n: string, cb: (e: any) => void) => events.set(n, cb),
      removeEventListener() {},
    },
  });
  const app = createTreeApp({
    render: () =>
      h(SurfacePane, {
        open: true,
        active: null,
        focused: false,
        surfaces: [],
        'onUpdate:open': (v: boolean) => {
          closed = !v;
        },
      }),
  });
  let closed = false;
  const root = node('root');
  app.mount(root);
  await nextTick();
  const separator = all(root).find((n) => n.props.role === 'separator');
  expect(separator).toBeTruthy();
  const before = Number(separator!.props['aria-valuenow']);
  (separator!.props.onKeydown as (e: unknown) => void)({
    key: 'ArrowLeft',
    shiftKey: false,
    preventDefault() {},
  });
  await nextTick();
  expect(Number(separator!.props['aria-valuenow'])).toBe(before + 10);
  const backdrop = all(root).find((n) => n.props['aria-label'] === 'Close pane overlay');
  expect(backdrop).toBeTruthy();
  (backdrop!.props.onClick as () => void)();
  expect(closed).toBe(true);
  app.unmount();
  Object.assign(globalThis, { window: oldWindow });
});

it('drags the separator and retains the preferred width when remounted', async () => {
  const listeners = new Map<string, (e: any) => void>(),
    store = new Map<string, string>();
  Object.assign(globalThis, {
    window: {
      innerWidth: 1200,
      addEventListener: (n: string, cb: (e: any) => void) => listeners.set(n, cb),
      removeEventListener: (n: string) => listeners.delete(n),
    },
    document: { body: { style: { userSelect: '' } } },
    localStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => store.set(k, v),
    },
  });
  const make = () => {
    const root = node('root');
    const app = createTreeApp({
      render: () =>
        h(SurfacePane, {
          open: true,
          active: null,
          focused: false,
          surfaces: [],
          widthKey: 'test-pane',
        }),
    });
    app.mount(root);
    return { app, root };
  };
  const first = make();
  await nextTick();
  const separator = all(first.root).find((n) => n.props.role === 'separator')!;
  (separator.props.onPointerdown as (e: unknown) => void)({
    button: 0,
    isPrimary: true,
    pointerId: 1,
    clientX: 500,
    preventDefault() {},
  });
  listeners.get('pointermove')!({ pointerId: 1, clientX: 450 });
  listeners.get('pointerup')!({ pointerId: 1, clientX: 450 });
  await nextTick();
  expect(separator.props['aria-valuenow']).toBe(414);
  first.app.unmount();
  const second = make();
  await nextTick();
  expect(all(second.root).find((n) => n.props.role === 'separator')!.props['aria-valuenow']).toBe(
    414,
  );
  second.app.unmount();
});

it('dismisses a narrow pane with Escape', async () => {
  let closed = false;
  Object.assign(globalThis, {
    window: {
      innerWidth: 800,
      addEventListener() {},
      removeEventListener() {},
      matchMedia: () => ({ matches: true }),
    },
  });
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(SurfacePane, {
        open: true,
        active: null,
        focused: false,
        surfaces: [],
        'onUpdate:open': (value: boolean) => {
          closed = !value;
        },
      }),
  });
  app.mount(root);
  await nextTick();
  const pane = all(root).find((n) => n.props['aria-label'] === 'Right pane')!;
  expect(pane.props.onKeydown).toBeTruthy();
  (pane.props.onKeydown as (e: unknown) => void)({ key: 'Escape', preventDefault() {} });
  expect(closed).toBe(true);
  app.unmount();
});

it('moves focus into an open pane when its layout becomes narrow', async () => {
  const listeners = new Map<string, () => void>();
  const fakeWindow = {
    innerWidth: 1200,
    addEventListener: (n: string, cb: () => void) => listeners.set(n, cb),
    removeEventListener() {},
  };
  Object.assign(globalThis, { window: fakeWindow, document: { activeElement: null } });
  const root = node('root'),
    app = createTreeApp({
      render: () => h(SurfacePane, { open: true, active: null, focused: false, surfaces: [] }),
    });
  app.mount(root);
  await nextTick();
  let focused = false;
  const pane = all(root).find((n) => n.props['aria-label'] === 'Right pane')!;
  pane.querySelector = () =>
    ({
      focus: () => {
        focused = true;
      },
    }) as never;
  fakeWindow.innerWidth = 800;
  listeners.get('resize')!();
  await nextTick();
  await nextTick();
  expect(focused).toBe(true);
  app.unmount();
});

it('keeps the overlay wide enough for its content on a phone-sized viewport', async () => {
  Object.assign(globalThis, {
    window: { innerWidth: 480, addEventListener() {}, removeEventListener() {} },
  });
  const root = node('root');
  const app = createTreeApp({
    render: () => h(SurfacePane, { open: true, active: null, focused: false, surfaces: [] }),
  });
  app.mount(root);
  await nextTick();
  const pane = all(root).find((n) => n.props['aria-label'] === 'Right pane')!;
  expect((pane.props.style as Record<string, string>)['--surface-width']).toBe('364px');
  app.unmount();
});

it('uses an overlay when the parent study is narrow even on a wide screen', async () => {
  Object.assign(globalThis, {
    window: { innerWidth: 1500, addEventListener() {}, removeEventListener() {} },
  });
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(SurfacePane, {
        open: true,
        active: null,
        focused: false,
        surfaces: [],
        containerWidth: 550,
      }),
  });
  app.mount(root);
  await nextTick();
  const pane = all(root).find((n) => n.props['aria-label'] === 'Right pane')!;
  expect(pane.props.role).toBe('dialog');
  expect(pane.props.class).toContain('surface-overlay');
  app.unmount();
});

it('limits a split pane to leave room in the actual study container', async () => {
  Object.assign(globalThis, {
    window: { innerWidth: 1500, addEventListener() {}, removeEventListener() {} },
  });
  const root = node('root');
  const app = createTreeApp({
    render: () =>
      h(SurfacePane, {
        open: true,
        active: null,
        focused: false,
        surfaces: [],
        containerWidth: 800,
      }),
  });
  app.mount(root);
  await nextTick();
  const sep = all(root).find((n) => n.props.role === 'separator')!;
  expect(sep.props['aria-valuemax']).toBe(400);
  app.unmount();
});
