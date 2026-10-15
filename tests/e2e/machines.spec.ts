import { expect, test } from '@playwright/test';
import { labFixture, labSignIn, labWorkspace } from './lab';

test('the lab daemon is listed and can be renamed', async ({ page }) => {
  await page.goto('/#/machines');
  const row = page.getByRole('listitem').filter({ hasText: 'lab-mac' });
  await expect(row).toBeVisible();
  await expect(row).toContainText('Online');
  await expect(row).toContainText('1 runtime');
  await expect(row).toContainText(/All idle|\d+ runs?/);
  await expect(row).toContainText('Ana Ruiz');
  try {
    await row.getByRole('button', { name: 'Machine actions' }).click();
    // An online machine cannot be removed.
    await expect(page.getByRole('menuitem', { name: 'Remove' })).toHaveCount(0);
    await page.getByRole('menuitem', { name: 'Rename…' }).click();
    const dialog = page.getByRole('dialog', { name: 'Rename machine' });
    await dialog.getByRole('textbox', { name: 'Name' }).fill('Lab workstation');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('listitem').filter({ hasText: 'Lab workstation' })).toBeVisible();
  } finally {
    // Later specs name the machine by its device name.
    const ws = await labWorkspace(page.request);
    const runtimes = (await (
      await page.request.get('/api/runtimes', { headers: { 'X-Workspace-ID': ws } })
    ).json()) as { id: string }[];
    for (const r of runtimes)
      await page.request.patch(`/api/runtimes/${r.id}`, {
        headers: { 'X-Workspace-ID': ws },
        data: { custom_name: '' },
      });
  }
});

test('Add a computer and Daemon show the commands', async ({ page }) => {
  await page.goto('/#/machines');
  await page.getByRole('button', { name: 'Add a computer' }).click();
  await expect(
    page.getByRole('dialog', { name: 'Add a computer' }).getByText('lyk login'),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Daemon' }).click();
  await expect(
    page.getByRole('dialog', { name: 'Daemon' }).getByText('lyk daemon start'),
  ).toBeVisible();
});

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-19-artifacts';
type MachineRuntime = {
  id: string;
  daemon_id: string;
  harness: string;
  custom_name: string | null;
  status: string;
  cli_version: string;
  active_runs: number;
};
const headers = (ws: string) => ({ 'X-Workspace-ID': ws });
async function registered(
  request: import('@playwright/test').APIRequestContext,
  ws: string,
  name: string,
) {
  const tokenResponse = await request.post('/api/tokens', {
    data: { name: 'Machines browser test' },
  });
  expect(tokenResponse.status()).toBe(201);
  const token = await tokenResponse.json();
  try {
    const response = await request.post('/api/daemon/register', {
      headers: { Authorization: `Bearer ${token.token}` },
      data: {
        workspace_id: ws,
        daemon_id: `machine-${Date.now()}`,
        device_name: name,
        harnesses: ['codex', 'claude'],
        versions: { codex: '1.2.3', claude: '2.3.4' },
      },
    });
    expect(response.status()).toBe(200);
    return (await response.json()) as MachineRuntime[];
  } finally {
    expect((await request.delete(`/api/tokens/${token.id}`)).status()).toBe(204);
  }
}
async function runtimeList(page: import('@playwright/test').Page, ws: string) {
  const response = await page.request.get('/api/runtimes', { headers: headers(ws) });
  expect(response.status()).toBe(200);
  return (await response.json()) as MachineRuntime[];
}
async function edit(page: import('@playwright/test').Page, name: string, draft: string) {
  await page
    .getByRole('listitem')
    .filter({ hasText: name })
    .getByRole('button', { name: 'Machine actions' })
    .click();
  await page.getByRole('menuitem', { name: 'Rename…' }).click();
  await page.getByRole('textbox', { name: 'Name' }).fill(draft);
}

test('two real runtimes group into one machine with versions, rename together, and clear to device name', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request);
  const records = await registered(page.request, ws, 'Two CLI computer');
  try {
    await page.goto('/#/machines');
    const row = page.getByRole('listitem').filter({ hasText: 'Two CLI computer' });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText('2 runtimes');
    await expect(row).toContainText('All idle');
    await expect(row.locator('i').nth(0)).toHaveAttribute('title', 'Claude Code 2.3.4');
    await expect(row.locator('i').nth(1)).toHaveAttribute('title', 'Codex 1.2.3');
    await expect(page.getByText('This device')).toHaveCount(0);
    await page.screenshot({ path: `${artifacts}/online.png`, fullPage: true });
    await edit(page, 'Two CLI computer', 'Renamed two CLI computer');
    await page.screenshot({ path: `${artifacts}/rename.png`, fullPage: true });
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(
      (await runtimeList(page, ws))
        .filter((r) => records.some((x) => x.id === r.id))
        .map((r) => r.custom_name),
    ).toEqual(['Renamed two CLI computer', 'Renamed two CLI computer']);
    await edit(page, 'Renamed two CLI computer', '');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(row).toBeVisible();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    expect(
      (await runtimeList(page, ws))
        .filter((r) => records.some((x) => x.id === r.id))
        .every((r) => r.custom_name === null),
    ).toBe(true);
  } finally {
    for (const r of records)
      expect(
        (await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) })).status(),
      ).toBe(204);
  }
});

test('another owner gets real rename/remove refusals; offline own machines can be removed', async ({
  page,
  browser,
}) => {
  test.setTimeout(120_000);
  const ws = await labWorkspace(page.request);
  const own = await registered(page.request, ws, 'Offline owned computer');
  const other = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  let theirs: MachineRuntime[] = [];
  try {
    const email = 'machine-owner@lab.test';
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
    theirs = await registered(other.request, ws, 'Colleague computer');
    await page.goto('/#/machines');
    await edit(page, 'Colleague computer', 'Refused name');
    const refused = page.waitForResponse(
      (r) => r.request().method() === 'PATCH' && r.url().includes('/api/runtimes/'),
    );
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    expect((await refused).status()).toBe(404);
    await expect(page.getByRole('alert')).toHaveText('Only the machine’s owner can rename it.');
    await page.screenshot({ path: `${artifacts}/owner-rename-refusal.png`, fullPage: true });
    await page.keyboard.press('Escape');
    await expect
      .poll(
        async () =>
          (await runtimeList(page, ws))
            .filter((r) => [...own, ...theirs].some((x) => x.id === r.id))
            .every((r) => r.status === 'offline'),
        { timeout: 100_000, intervals: [500, 1000] },
      )
      .toBe(true);
    await page.reload();
    const colleague = page.getByRole('listitem').filter({ hasText: 'Colleague computer' });
    await expect(colleague).toContainText('Offline');
    await page.screenshot({ path: `${artifacts}/offline.png`, fullPage: true });
    await colleague.getByRole('button', { name: 'Machine actions' }).click();
    await page.screenshot({ path: `${artifacts}/offline-open-menu.png`, fullPage: true });
    page.once('dialog', (dialog) => dialog.accept());
    const refusedDelete = page.waitForResponse(
      (r) => r.request().method() === 'DELETE' && r.url().includes('/api/runtimes/'),
    );
    await page.getByRole('menuitem', { name: 'Remove' }).click();
    expect((await refusedDelete).status()).toBe(404);
    await expect(page.getByText('Only the machine’s owner can remove it.')).toBeVisible();
    await page.screenshot({ path: `${artifacts}/owner-remove-refusal.png`, fullPage: true });
    let releaseDelete!: () => void;
    const deleteGate = new Promise<void>((resolve) => {
      releaseDelete = resolve;
    });
    let deleted!: (status: number) => void;
    const deleteAccepted = new Promise<number>((resolve) => {
      deleted = resolve;
    });
    let drainedDelete!: () => void;
    const deleteDrained = new Promise<void>((resolve) => {
      drainedDelete = resolve;
    });
    let deleteCount = 0;
    await page.route('**/api/runtimes/*', async (route) => {
      if (route.request().method() !== 'DELETE') return route.continue();
      deleteCount++;
      if (deleteCount > 1) return route.continue();
      try {
        const response = await route.fetch();
        deleted(response.status());
        await deleteGate;
        await route.fulfill({ response });
      } finally {
        drainedDelete();
      }
    });
    try {
      await page
        .getByRole('listitem')
        .filter({ hasText: 'Offline owned computer' })
        .getByRole('button', { name: 'Machine actions' })
        .click();
      page.once('dialog', (dialog) => dialog.accept());
      await page.getByRole('menuitem', { name: 'Remove' }).click();
      expect(await deleteAccepted).toBe(204);
      await page.goto('/#/inbox');
      await expect(page.getByRole('navigation', { name: 'Inbox items' })).toBeVisible();
      await page.goto('/#/machines');
      await edit(page, 'lab-mac', 'Draft after an old removal');
      releaseDelete();
      await deleteDrained;
      await page.evaluate(() => new Promise(requestAnimationFrame));
      expect(deleteCount).toBe(1);
      expect(
        (await runtimeList(page, ws)).filter((r) => own.some((x) => x.id === r.id)),
      ).toHaveLength(1);
      await expect(page.getByRole('textbox', { name: 'Name' })).toHaveValue(
        'Draft after an old removal',
      );
      await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
      await page.keyboard.press('Escape');
    } finally {
      releaseDelete();
      await deleteDrained;
      await page.unroute('**/api/runtimes/*');
    }
    await page
      .getByRole('listitem')
      .filter({ hasText: 'Offline owned computer' })
      .getByRole('button', { name: 'Machine actions' })
      .click();
    page.once('dialog', (dialog) => dialog.accept());
    await page.getByRole('menuitem', { name: 'Remove' }).click();
    await expect(
      page.getByRole('listitem').filter({ hasText: 'Offline owned computer' }),
    ).toHaveCount(0);
    expect((await runtimeList(page, ws)).some((r) => own.some((x) => x.id === r.id))).toBe(false);
    expect(
      (await runtimeList(page, ws)).filter((r) => theirs.some((x) => x.id === r.id)),
    ).toHaveLength(2);
  } finally {
    for (const r of own)
      await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) });
    for (const r of theirs)
      expect(
        (await other.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) })).status(),
      ).toBe(204);
    await other.close();
  }
});

for (const outcome of ['accepted', 'refused'] as const) {
  for (const destination of [
    'another machine',
    'same machine after return',
    'another workspace',
  ] as const) {
    test(`a delayed ${outcome} multi-runtime rename cannot continue into ${destination}`, async ({
      page,
    }) => {
      const ws = await labWorkspace(page.request);
      const records = await registered(page.request, ws, 'Old machine visit');
      const other = await labFixture(page.request);
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      let received!: (status: number) => void;
      const accepted = new Promise<number>((resolve) => {
        received = resolve;
      });
      let drain!: () => void;
      const drained = new Promise<void>((resolve) => {
        drain = resolve;
      });
      const writes: string[] = [];
      let first = true;
      await page.route('**/api/runtimes/*', async (route) => {
        if (route.request().method() !== 'PATCH') return route.continue();
        writes.push(route.request().headers()['x-workspace-id']!);
        if (!first) return route.continue();
        first = false;
        try {
          const response = await route.fetch(
            outcome === 'refused'
              ? { headers: { ...route.request().headers(), 'X-Workspace-ID': other.workspace_id } }
              : undefined,
          );
          received(response.status());
          await gate;
          await route.fulfill({ response });
        } finally {
          drain();
        }
      });
      try {
        await page.goto('/#/machines');
        await edit(page, 'Old machine visit', 'Original dispatched name');
        await page.getByRole('button', { name: 'Save', exact: true }).click();
        expect(await accepted).toBe(outcome === 'accepted' ? 204 : 404);
        if (destination === 'another workspace') {
          await page.keyboard.press('Escape');
          await page
            .getByRole('complementary', { name: 'Workspace navigation' })
            .getByRole('button', { name: /Beta-cell lab/ })
            .click();
          await page
            .getByRole('menu', { name: 'Workspace' })
            .getByRole('menuitem', { name: /Browser fixtures/ })
            .first()
            .click();
          await expect(page).toHaveURL(/#\/studies$/);
          await page.goto('/#/machines');
          await expect(page.getByRole('heading', { name: 'Machines', exact: true })).toBeVisible();
          await expect(page.getByRole('dialog')).toHaveCount(0);
        } else {
          await page.keyboard.press('Escape');
          if (destination === 'same machine after return') {
            await page.goto('/#/inbox');
            await expect(page.getByRole('navigation', { name: 'Inbox items' })).toBeVisible();
            await page.goto('/#/machines');
            await edit(
              page,
              outcome === 'accepted' ? 'Original dispatched name' : 'Old machine visit',
              'Current draft',
            );
          } else {
            await edit(page, 'lab-mac', 'Current draft');
          }
        }
        release();
        await drained;
        await page.evaluate(() => new Promise(requestAnimationFrame));
        await expect(page.getByRole('alert')).toHaveCount(0);
        expect(writes).toEqual([ws]);
        const stored = (await runtimeList(page, ws)).filter((r) =>
          records.some((x) => x.id === r.id),
        );
        expect(stored.filter((r) => r.custom_name === 'Original dispatched name')).toHaveLength(
          outcome === 'accepted' ? 1 : 0,
        );
        expect(stored.filter((r) => r.custom_name === null)).toHaveLength(
          outcome === 'accepted' ? 1 : 2,
        );
        expect(
          (await runtimeList(page, other.workspace_id)).some((r) =>
            records.some((x) => x.id === r.id),
          ),
        ).toBe(false);
        if (destination !== 'another workspace') {
          await expect(page.getByRole('textbox', { name: 'Name' })).toHaveValue('Current draft');
          await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
        }
      } finally {
        release();
        await drained;
        for (const r of records)
          expect(
            (await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) })).status(),
          ).toBe(204);
        expect((await page.request.delete(`/api/workspaces/${other.workspace_id}`)).status()).toBe(
          204,
        );
      }
    });
  }
}
