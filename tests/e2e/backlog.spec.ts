import { expect, test, type Page, type APIRequestContext } from '@playwright/test';
import { LAB, labSignIn } from './lab';
import { writeFile } from 'node:fs/promises';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-15-artifacts';
type Study = { id: string; position: number; number: number; board_column: string; ending: string };
const headers = (ws: string) => ({ 'X-Workspace-ID': ws });
async function wip(request: APIRequestContext, ws: string, limit: number) {
  expect(
    (
      await request.patch(`/api/workspaces/${ws}/settings`, {
        data: { settings: { wip_limit: limit } },
      })
    ).status(),
  ).toBe(204);
}
async function workspace(page: Page) {
  const response = await page.request.post('/api/workspaces', {
    data: { name: 'Backlog lab', slug: `backlog-${Date.now()}` },
  });
  expect(response.status()).toBe(201);
  const ws = (await response.json()).id as string;
  await wip(page.request, ws, 0);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
  return ws;
}
async function create(request: APIRequestContext, ws: string, title: string): Promise<Study> {
  const response = await request.post('/api/cards', {
    headers: headers(ws),
    data: { question: `${title} ${Date.now()}?`, title },
  });
  expect(response.status()).toBe(201);
  return response.json();
}
async function cards(request: APIRequestContext, ws: string): Promise<Study[]> {
  const response = await request.get('/api/cards', { headers: headers(ws) });
  expect(response.status()).toBe(200);
  return response.json();
}
const backlog = (p: Page) => p.getByRole('region', { name: 'Backlog' });
const card = (p: Page, id: string) => backlog(p).locator(`[data-card="${id}"]`);
const order = (p: Page) =>
  backlog(p)
    .locator('.task-card')
    .evaluateAll((els) => els.map((e) => e.getAttribute('data-card')));
async function action(p: Page, id: string, name: string) {
  await card(p, id).click({ button: 'right' });
  await p
    .getByRole('menu', { name: 'Study actions' })
    .getByRole('menuitem', { name, exact: true })
    .click();
}

test('dragging a Backlog study to the top persists and WIP pull starts it first', async ({
  page,
}) => {
  const ws = await workspace(page);
  try {
    const first = await create(page.request, ws, 'Gut microbiota shifts under SSRIs');
    const second = await create(page.request, ws, 'Metformin and senescent beta cells');
    await page.goto('/#/studies');
    await expect(backlog(page).locator('.task-card').first()).toHaveAttribute(
      'data-card',
      second.id,
    );
    await card(page, first.id).dragTo(card(page, second.id), { targetPosition: { x: 20, y: 4 } });
    await expect(backlog(page).locator('.task-card').first()).toHaveAttribute(
      'data-card',
      first.id,
    );
    await page.reload();
    await expect(backlog(page).locator('.task-card').first()).toHaveAttribute(
      'data-card',
      first.id,
    );
    await page.screenshot({ path: `${artifacts}/reordered.png`, fullPage: true });
    const active = (await cards(page.request, ws)).filter(
      (c) => !c.ending && !['backlog', 'done'].includes(c.board_column),
    ).length;
    await wip(page.request, ws, active + 1);
    await expect(card(page, first.id)).toHaveCount(0);
    await expect(card(page, second.id)).toBeVisible();
    expect((await cards(page.request, ws)).find((c) => c.id === first.id)!.board_column).not.toBe(
      'backlog',
    );
  } finally {
    await wip(page.request, ws, 100);
  }
});

test('the Backlog menu edits, moves to top and deletes with real confirmation', async ({
  page,
}) => {
  const ws = await workspace(page);
  try {
    const lower = await create(page.request, ws, 'Caffeine and AF recurrence');
    const upper = await create(page.request, ws, 'Statin use and ALS progression');
    await page.goto('/#/studies');
    await card(page, lower.id).focus();
    await page.keyboard.press('Shift+F10');
    await expect(page.getByRole('menu', { name: 'Study actions' })).toBeVisible();
    await page.screenshot({ path: `${artifacts}/study-menu.png`, fullPage: true });
    await page.getByRole('menuitem', { name: 'Move to top' }).click();
    await expect(backlog(page).locator('.task-card').first()).toHaveAttribute(
      'data-card',
      lower.id,
    );
    await expect
      .poll(async () => (await cards(page.request, ws)).find((c) => c.id === lower.id)!.position)
      .toBeLessThan(upper.position);
    expect((await cards(page.request, ws)).find((c) => c.id === lower.id)!.position).toBeLessThan(
      upper.position,
    );
    await action(page, lower.id, 'Edit…');
    const dialog = page.getByRole('dialog', { name: 'Edit study' });
    await expect(dialog.getByRole('textbox', { name: 'Research question' })).toHaveValue(
      /Caffeine and AF recurrence/,
    );
    await dialog
      .getByRole('textbox', { name: 'Study title' })
      .fill('Caffeine and atrial fibrillation');
    await page.screenshot({ path: `${artifacts}/edit-menu-entry.png`, fullPage: true });
    await dialog.getByRole('button', { name: /Save study/ }).click();
    await expect(card(page, lower.id)).toContainText('Caffeine and atrial fibrillation');
    page.once('dialog', (d) => d.dismiss());
    await action(page, upper.id, 'Delete…');
    await expect(card(page, upper.id)).toBeVisible();
    page.once('dialog', (d) => {
      expect(d.message()).toContain('This cannot be undone');
      return d.accept();
    });
    await action(page, upper.id, 'Delete…');
    await expect(card(page, upper.id)).toHaveCount(0);
    expect(
      (await page.request.get(`/api/cards/${upper.id}`, { headers: headers(ws) })).status(),
    ).toBe(404);
  } finally {
    await wip(page.request, ws, 100);
  }
});

test('two distinct people make accepted writes to the same card and live boards reflect the later position', async ({
  page,
  browser,
}) => {
  const ws = await workspace(page);
  const other = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  let releaseFirst = () => {};
  const firstGate = new Promise<void>((resolve) => {
    releaseFirst = resolve;
  });
  try {
    const email = 'backlog-colleague@lab.test';
    const invitation = await page.request.post(`/api/workspaces/${ws}/invitations`, {
      headers: headers(ws),
      data: { email, role: 'member' },
    });
    expect(invitation.status()).toBe(201);
    await labSignIn(other.request, email);
    expect(
      (
        await other.request.post(`/api/invitations/${(await invitation.json()).token}/accept`)
      ).status(),
    ).toBe(200);
    expect(
      (
        await other.request.patch('/api/me', {
          data: { name: 'Backlog colleague', onboarded: true },
        })
      ).status(),
    ).toBe(200);
    const [one, two] = await Promise.all([
      page.request.get('/api/me'),
      other.request.get('/api/me'),
    ]);
    const [person1, person2] = await Promise.all([one.json(), two.json()]);
    expect(person1.id).not.toBe(person2.id);
    const memberships = await (
      await page.request.get(`/api/workspaces/${ws}/members`, { headers: headers(ws) })
    ).json();
    expect(memberships.map((m: { user_id: string }) => m.user_id)).toEqual(
      expect.arrayContaining([person1.id, person2.id]),
    );
    const moved = await create(page.request, ws, 'Zinc and islet amyloid');
    await create(page.request, ws, 'Vitamin D and beta-cell mass');
    const top = await create(page.request, ws, 'Sleep and insulin secretion');
    const page2 = await other.newPage();
    await page2.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws);
    await Promise.all([page.goto('/#/studies'), page2.goto('/#/studies')]);
    await expect(card(page2, moved.id)).toBeVisible();
    const initial = await order(page);
    const origins = await Promise.all([
      page.evaluate(() => performance.timeOrigin),
      page2.evaluate(() => performance.timeOrigin),
    ]);
    let firstPosition = 0;
    await page.route(`**/api/cards/${moved.id}`, async (route) => {
      if (route.request().method() !== 'PATCH') return route.continue();
      firstPosition = route.request().postDataJSON().position;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      expect((await response.json()).position).toBe(firstPosition);
      await firstGate;
      await route.fulfill({ response });
    });
    // The first accepted response remains held while the other person writes the same card.
    await action(page, moved.id, 'Move to top');
    await expect
      .poll(async () => (await cards(page.request, ws)).find((c) => c.id === moved.id)!.position)
      .toBeLessThan(top.position);
    await expect(backlog(page2).locator('.task-card').first()).toHaveAttribute(
      'data-card',
      moved.id,
    );
    const laterResponse = page2.waitForResponse(
      (r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/cards/${moved.id}`),
    );
    const remaining = card(page2, top.id);
    const targetBox = (await remaining.boundingBox())!;
    await card(page2, moved.id).dragTo(remaining, {
      targetPosition: { x: 20, y: targetBox.height - 4 },
    });
    const accepted = await laterResponse;
    expect(accepted.status()).toBe(200);
    const laterPosition = accepted.request().postDataJSON().position as number;
    expect((await accepted.json()).position).toBe(laterPosition);
    expect(laterPosition).not.toBe(firstPosition);
    releaseFirst();
    const server = (await cards(page.request, ws))
      .filter((c) => c.board_column === 'backlog')
      .sort((a, b) => a.position - b.position || a.number - b.number);
    expect(server.find((c) => c.id === moved.id)!.position).toBe(laterPosition);
    const final = server.map((c) => c.id);
    expect(final).not.toEqual(initial);
    await expect.poll(() => order(page)).toEqual(final);
    await expect.poll(() => order(page2)).toEqual(final);
    expect(await page.evaluate(() => performance.timeOrigin)).toBe(origins[0]);
    expect(await page2.evaluate(() => performance.timeOrigin)).toBe(origins[1]);
    await page.screenshot({ path: `${artifacts}/two-people-converged.png`, fullPage: true });
    await page2.screenshot({ path: `${artifacts}/second-person-converged.png`, fullPage: true });
    await writeFile(
      `${artifacts}/concurrency.json`,
      JSON.stringify(
        {
          workspace: ws,
          people: [person1.id, person2.id],
          card: moved.id,
          initialOrder: initial,
          firstAcceptedPosition: firstPosition,
          laterAcceptedPosition: laterPosition,
          finalOrder: final,
          originsBefore: origins,
          originsAfter: await Promise.all([
            page.evaluate(() => performance.timeOrigin),
            page2.evaluate(() => performance.timeOrigin),
          ]),
        },
        null,
        2,
      ),
    );
  } finally {
    releaseFirst();
    await page.unrouteAll({ behavior: 'wait' });
    await other.close();
    await wip(page.request, ws, 100);
  }
});

test('a real refusal restores the optimistic order and explains why; active studies cannot be dragged or deleted', async ({
  page,
}) => {
  const ws = await workspace(page);
  let releaseReads = () => {};
  let releaseWrite = () => {};
  const readsGate = new Promise<void>((resolve) => {
    releaseReads = resolve;
  });
  try {
    const lower = await create(page.request, ws, 'Backlog refusal');
    const upper = await create(page.request, ws, 'Retained neighbour');
    await page.goto('/#/studies');
    await expect(card(page, lower.id)).toBeVisible();
    const initial = await order(page);
    const writeGate = new Promise<void>((resolve) => {
      releaseWrite = resolve;
    });
    let received = false;
    await page.route(`**/api/cards/${lower.id}`, async (route) => {
      if (route.request().method() !== 'PATCH') return route.continue();
      received = true;
      await writeGate;
      const response = await route.fetch();
      expect(response.status()).toBe(409);
      await route.fulfill({ response });
    });
    await action(page, lower.id, 'Move to top');
    await expect.poll(() => received).toBe(true);
    await expect(backlog(page).locator('.task-card').first()).toHaveAttribute(
      'data-card',
      lower.id,
    );
    await page.route('**/api/cards', async (route) => {
      const response = await route.fetch();
      await readsGate;
      await route.fulfill({ response });
    });
    expect((await page.request.post(`${LAB}/lab/hypotheses/${lower.id}`)).status()).toBe(204);
    releaseWrite();
    await expect(page.locator('.board-action-error')).toContainText('Backlog');
    await expect.poll(() => order(page)).toEqual(initial);
    await page.screenshot({ path: `${artifacts}/refusal-rollback.png`, fullPage: true });
    expect((await cards(page.request, ws)).find((c) => c.id === lower.id)!.position).toBe(
      lower.position,
    );
    page.once('dialog', (d) => d.accept());
    const refusedDelete = page.waitForResponse(
      (r) => r.request().method() === 'DELETE' && r.url().endsWith(`/api/cards/${lower.id}`),
    );
    await action(page, lower.id, 'Delete…');
    expect((await refusedDelete).status()).toBe(409);
    await expect(page.locator('.board-action-error')).toContainText('Backlog');
    await expect(card(page, lower.id)).toBeVisible();
    releaseReads();
    await expect(card(page, lower.id)).toHaveCount(0);
    const active = page
      .getByRole('region', { name: 'Hypotheses' })
      .locator(`[data-card="${lower.id}"]`);
    await expect(active).toBeVisible();
    await expect(active).not.toHaveAttribute('draggable', 'true');
    await active.click({ button: 'right' });
    await expect(page.getByRole('menu', { name: 'Study actions' })).toHaveCount(0);
    expect(
      (await page.request.delete(`/api/cards/${lower.id}`, { headers: headers(ws) })).status(),
    ).toBe(409);
    await expect(card(page, upper.id)).toBeVisible();
    const beforeCrossColumn = (await cards(page.request, ws)).find((c) => c.id === upper.id)!;
    await card(page, upper.id).dragTo(active);
    await expect(card(page, upper.id)).toBeVisible();
    expect((await cards(page.request, ws)).find((c) => c.id === upper.id)).toMatchObject({
      board_column: 'backlog',
      position: beforeCrossColumn.position,
    });
  } finally {
    releaseWrite();
    releaseReads();
    await page.unrouteAll({ behavior: 'wait' });
    await wip(page.request, ws, 100);
  }
});

test('a delayed old-workspace refusal cannot recreate its cleared cache or change the new board', async ({
  page,
}) => {
  const ws = await workspace(page);
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let destination = '';
  try {
    const moved = await create(page.request, ws, 'Old workspace study');
    await create(page.request, ws, 'Old workspace neighbour');
    const response = await page.request.post('/api/workspaces', {
      data: { name: 'Destination lab', slug: `destination-${Date.now()}` },
    });
    expect(response.status()).toBe(201);
    destination = (await response.json()).id as string;
    await wip(page.request, destination, 0);
    const destinationCard = await create(page.request, destination, 'Destination study');
    await page.goto('/#/studies');
    await expect(card(page, moved.id)).toBeVisible();
    let waiting = false;
    await page.route(`**/api/cards/${moved.id}`, async (route) => {
      if (route.request().method() !== 'PATCH') return route.continue();
      expect(route.request().headers()['x-workspace-id']).toBe(ws);
      expect((await page.request.post(`${LAB}/lab/hypotheses/${moved.id}`)).status()).toBe(204);
      const actual = await route.fetch();
      expect(actual.status()).toBe(409);
      waiting = true;
      await gate;
      await route.fulfill({ response: actual });
    });
    await action(page, moved.id, 'Move to top');
    await expect.poll(() => waiting).toBe(true);
    await page.evaluate(async (id) => {
      const path = '/src/api/session.ts';
      (await import(path)).workspaceId.value = id;
    }, destination);
    await expect(card(page, destinationCard.id)).toBeVisible();
    release();
    await page.unrouteAll({ behavior: 'wait' });
    await expect.poll(() => order(page)).toEqual([destinationCard.id]);
    const oldScopeKeys = await page.evaluate((id) => {
      const app = document.querySelector('#app') as Element & {
        __vue_app__: {
          _context: {
            provides: Record<
              string,
              { getQueryCache: () => { getAll: () => { queryKey: unknown[] }[] } }
            >;
          };
        };
      };
      return app.__vue_app__._context.provides
        .VUE_QUERY_CLIENT!.getQueryCache()
        .getAll()
        .filter((q) => q.queryKey[0] === 'ws' && q.queryKey[1] === id)
        .map((q) => q.queryKey);
    }, ws);
    expect(oldScopeKeys).toEqual([]);
  } finally {
    release();
    await page.unrouteAll({ behavior: 'wait' });
    await wip(page.request, ws, 100);
    if (destination) await wip(page.request, destination, 100);
  }
});

test('a deleted predecessor stays canonical while an unrelated title edit succeeds', async ({
  page,
}) => {
  const ws = await workspace(page);
  const predecessor = await create(page.request, ws, 'Retained predecessor');
  const res = await page.request.post('/api/cards', {
    headers: headers(ws),
    data: {
      title: 'Dependent retained study',
      question: `Retained provenance ${Date.now()}?`,
      follows_from: [predecessor.id],
    },
  });
  expect(res.status()).toBe(201);
  const dependent = await res.json();
  expect(
    (await page.request.delete(`/api/cards/${predecessor.id}`, { headers: headers(ws) })).status(),
  ).toBe(204);
  await page.goto('/#/studies');
  await action(page, dependent.id, 'Edit…');
  const dialog = page.getByRole('dialog', { name: 'Edit study' });
  await dialog.getByRole('textbox', { name: 'Study title' }).fill('Updated dependent title');
  const saved = page.waitForResponse(
    (r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/cards/${dependent.id}`),
  );
  await dialog.getByRole('button', { name: /Save study/ }).click();
  expect((await saved).status()).toBe(200);
  await expect(dialog).toHaveCount(0);
  const canonical = await page.request.get(`/api/cards/${dependent.id}`, { headers: headers(ws) });
  expect(await canonical.json()).toMatchObject({
    title: 'Updated dependent title',
    follows_from: [predecessor.id],
  });
});

test('study context-menu Escape restores the originating card focus', async ({ page }) => {
  const ws = await workspace(page);
  const study = await create(page.request, ws, 'Keyboard context study');
  await page.goto('/#/studies');
  await card(page, study.id).focus();
  await page.keyboard.press('Shift+F10');
  await expect(page.getByRole('menuitem', { name: 'Open', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(card(page, study.id)).toBeFocused();
});
