import { describe, expect, it, vi } from 'vitest';
import type { RouteLocationNormalizedLoaded, RouteRecordNormalized } from 'vue-router';

vi.mock('vue-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('vue-router')>();
  return { ...original, createWebHashHistory: original.createMemoryHistory };
});
vi.mock('lottie-web/build/player/lottie_light', () => ({
  default: { loadAnimation: () => ({ destroy() {} }) },
}));
import { router } from './router';

function resolvedProps(
  record: RouteRecordNormalized,
  view: string,
  route: RouteLocationNormalizedLoaded,
) {
  const value = record.props[view];
  return typeof value === 'function' ? value(route) : value;
}

describe('study destinations', () => {
  it.each([
    ['/studies', 'studies', 'Studies', 'workspace'],
    ['/my-studies', 'my-studies', 'My Studies', 'mine'],
  ])('resolves %s with its own heading and board scope', async (path, page, title, scope) => {
    await router.push(path);
    const route = router.currentRoute.value;
    expect(route.path).toBe(path);
    expect(route.meta).toMatchObject({ page, title });
    expect(resolvedProps(route.matched[0]!, 'default', route)).toEqual({ scope });
  });

  it('uses the same board and detail components for both destinations', () => {
    const workspace = router.resolve('/studies/c1').matched[0]!;
    const personal = router.resolve('/my-studies/c1').matched[0]!;
    expect(personal.components?.default).toBe(workspace.components?.default);
    expect(personal.components?.detail).toBe(workspace.components?.detail);
  });

  it.each(['/studies', '/my-studies'])('passes the detail id and returns to %s', async (base) => {
    await router.push(`${base}/study-123`);
    const route = router.currentRoute.value;
    expect(route.path).toBe(`${base}/study-123`);
    expect(resolvedProps(route.matched[0]!, 'detail', route)).toEqual({ id: 'study-123' });
    await router.push(`/${String(route.meta.page)}`);
    expect(router.currentRoute.value.path).toBe(base);
  });

  it('sends default and unknown paths to Aristotle', async () => {
    await router.push('/');
    expect(router.currentRoute.value.path).toBe('/coresearch');
    await router.push('/unknown');
    expect(router.currentRoute.value.path).toBe('/coresearch');
  });
});
