import { expect, test } from '@playwright/test';
import { LAB, labFixture, labSignIn, labWorkspace } from './lab';

test('the lab daemon is listed and can be renamed', async ({ page }) => {
  await page.goto('/#/machines');
  const row = page.getByRole('listitem').filter({ hasText: 'lab-mac' });
  await expect(row).toBeVisible();
  await expect(row).toContainText('Online');
  await expect(row).toContainText('1 runtime');
  await expect(row).toContainText(/All idle|\d+ runs?/);
  await expect(row).toContainText('Ana Ruiz');
  try {
    await expect(row.getByRole('button', { name: 'Machine actions' })).toHaveCount(0);
    await row.getByRole('link').click();
    // An online machine cannot be removed.
    await expect(page.getByRole('button', { name: 'Remove', exact: true })).toHaveCount(0);
    await page.getByRole('button', { name: 'Rename machine', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Rename machine' });
    await dialog.getByRole('textbox', { name: 'Name' }).fill('Lab workstation');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByRole('heading', { name: 'Lab workstation', exact: true })).toBeVisible();
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

test('Add a machine shows the connection commands without a Daemon action', async ({ page }) => {
  await page.goto('/#/machines');
  await page.getByRole('button', { name: 'Add a machine' }).click();
  await expect(
    page.getByRole('dialog', { name: 'Add a machine' }).getByText('lyk login'),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Daemon', exact: true })).toHaveCount(0);
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
async function openMachine(page: import('@playwright/test').Page, name: string) {
  await page.goto('/#/settings?tab=machines');
  await page.getByRole('listitem').filter({ hasText: name }).getByRole('link').click();
}
async function edit(page: import('@playwright/test').Page, name: string, draft: string) {
  await openMachine(page, name);
  await page.getByRole('button', { name: 'Rename machine', exact: true }).click();
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
    await expect(page.getByRole('button', { name: 'Rename machine', exact: true })).toBeFocused();
    await edit(page, 'Renamed two CLI computer', '');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Two CLI computer', exact: true }),
    ).toBeVisible();
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
    const colleague = page.locator('.resource-machines');
    await expect(
      page.getByRole('heading', { name: 'Colleague computer', exact: true }),
    ).toBeVisible();
    await expect(colleague).toContainText('Offline');
    await page.screenshot({ path: `${artifacts}/offline.png`, fullPage: true });
    await openMachine(page, 'Colleague computer');
    await page.screenshot({ path: `${artifacts}/offline-open-menu.png`, fullPage: true });
    page.once('dialog', (dialog) => dialog.accept());
    const refusedDelete = page.waitForResponse(
      (r) => r.request().method() === 'DELETE' && r.url().includes('/api/runtimes/'),
    );
    await page.getByRole('button', { name: 'Remove', exact: true }).click();
    expect((await refusedDelete).status()).toBe(404);
    await expect(colleague.getByRole('alert')).toHaveText(
      'Only the machine’s owner can remove it.',
    );
    await page.screenshot({ path: `${artifacts}/owner-remove-refusal.png`, fullPage: true });
    let releaseRefusal!: () => void;
    const refusalGate = new Promise<void>((resolve) => {
      releaseRefusal = resolve;
    });
    let gotRefusal!: (status: number) => void;
    const heldRefusal = new Promise<number>((resolve) => {
      gotRefusal = resolve;
    });
    let drainedRefusal!: () => void;
    const refusalDrained = new Promise<void>((resolve) => {
      drainedRefusal = resolve;
    });
    await page.route('**/api/runtimes/*', async (route) => {
      if (route.request().method() !== 'DELETE') return route.continue();
      try {
        const response = await route.fetch();
        gotRefusal(response.status());
        await refusalGate;
        await route.fulfill({ response });
      } finally {
        drainedRefusal();
      }
    });
    try {
      await openMachine(page, 'Colleague computer');
      await expect(colleague.getByRole('alert')).toHaveCount(0);
      page.once('dialog', (dialog) => dialog.accept());
      await page.getByRole('button', { name: 'Remove', exact: true }).click();
      expect(await heldRefusal).toBe(404);
      await page.goto('/#/settings?tab=machines');
      await page.getByRole('button', { name: 'Add a machine', exact: true }).click();
      await expect(page.getByRole('dialog', { name: 'Add a machine' })).toBeVisible();
      await page.keyboard.press('Escape');
      await edit(page, 'lab-mac', 'Draft after an old removal refusal');
      releaseRefusal();
      await refusalDrained;
      await page.evaluate(() => new Promise(requestAnimationFrame));
      await expect(page.locator('.resource-machines').getByRole('alert')).toHaveCount(0);
      await expect(page.getByRole('textbox', { name: 'Name' })).toHaveValue(
        'Draft after an old removal refusal',
      );
      await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
      await page.keyboard.press('Escape');
    } finally {
      releaseRefusal();
      await refusalDrained;
      await page.unroute('**/api/runtimes/*');
    }
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
      await openMachine(page, 'Offline owned computer');
      page.once('dialog', (dialog) => dialog.accept());
      await page.getByRole('button', { name: 'Remove', exact: true }).click();
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
    // The first removal reaches the list by a realtime event; a stale list resends it.
    await page.goto('/#/settings?tab=machines');
    const remaining = page.getByRole('listitem').filter({ hasText: 'Offline owned computer' });
    await expect(remaining).toContainText('1 runtime');
    await remaining.getByRole('link').click();
    page.once('dialog', (dialog) => dialog.accept());
    await page.getByRole('button', { name: 'Remove', exact: true }).click();
    await expect(page).toHaveURL(/#\/settings\?tab=machines$/);
    await expect(page.getByRole('button', { name: 'Add a machine', exact: true })).toBeVisible();
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
    'another machine detail',
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
          if (destination === 'another machine detail') {
            const target = (await runtimeList(page, ws)).find(
              (r) => !records.some((x) => x.id === r.id),
            )!;
            await page.goto('/#/machines/' + target.daemon_id);
            await page.getByRole('button', { name: 'Rename machine', exact: true }).click();
            await page.getByRole('textbox', { name: 'Name' }).fill('Current draft');
          } else if (destination === 'same machine after return') {
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

test('Machines active run count follows lifecycle transitions without navigation', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  let subscribed = false;
  await page.routeWebSocket(/\/ws$/, (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((raw) => {
      if (JSON.parse(String(raw)).type === 'subscribe') subscribed = true;
      server.send(raw);
    });
  });
  await page.goto('/#/machines');
  await expect.poll(() => subscribed).toBe(true);
  // The socket's first-open refresh must finish before the lifecycle begins.
  await expect
    .poll(() =>
      page.evaluate(() => {
        const app = document.querySelector('#app') as Element & {
          __vue_app__: {
            _context: {
              provides: Record<
                string,
                { getQueryCache: () => { getAll: () => { state: { fetchStatus: string } }[] } }
              >;
            };
          };
        };
        return app.__vue_app__._context.provides
          .VUE_QUERY_CLIENT!.getQueryCache()
          .getAll()
          .every((q) => q.state.fetchStatus === 'idle');
      }),
    )
    .toBe(true);
  const machine = page.getByRole('listitem').filter({ hasText: 'Recorded transcript' });
  await expect(machine.locator('.runs')).toHaveText('All idle');
  const origin = await page.evaluate(() => performance.timeOrigin);
  expect((await page.request.post(`${LAB}/lab/run/${f.run_id}/start`)).status()).toBe(204);
  await expect(machine.locator('.runs')).toHaveText('1 run');
  expect((await page.request.post(`${LAB}/lab/run/${f.run_id}/finish`)).status()).toBe(204);
  await expect(machine.locator('.runs')).toHaveText('All idle');
  expect(await page.evaluate(() => performance.timeOrigin)).toBe(origin);
});

const refinementArtifacts =
  '.superpowers/sdd/2026-10-15-study-inspector-refinements/task-3-artifacts';

test('computer setup offers supported platform commands, real clipboard, and detects only a newly online machine', async ({
  page,
  context,
}) => {
  const ws = await labWorkspace(page.request);
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/#/machines');
  await expect(page.getByRole('listitem').filter({ hasText: 'lab-mac' })).toBeVisible();
  const trigger = page.getByRole('button', { name: 'Add a machine', exact: true });
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: 'Add a machine', exact: true });
  await expect(dialog.getByText('1. Build the Lykeion CLI', { exact: true })).toBeVisible();
  await expect(dialog.locator('.connection-status')).toHaveText('Waiting for your computer');
  await expect(dialog).toHaveCSS('width', '512px');
  await dialog.getByRole('tab', { name: 'macOS / Linux', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(dialog.getByRole('tab', { name: 'Windows', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await page.keyboard.press('ArrowLeft');
  await expect(dialog.getByRole('tab', { name: 'macOS / Linux', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await dialog.getByRole('button', { name: 'Copy build command' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    'go build -o lyk ./cmd/lyk',
  );
  await dialog.getByRole('button', { name: 'Copy start commands' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    `./lyk login --server '${LAB}' --email you@lab.org\n./lyk daemon start`,
  );
  await dialog.getByRole('tab', { name: 'Windows', exact: true }).click();
  await dialog.getByRole('button', { name: 'Copy build command' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    'go build -o lyk.exe ./cmd/lyk',
  );
  await dialog.getByRole('button', { name: 'Copy start commands' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    `.\\lyk.exe login --server '${LAB}' --email you@lab.org\n.\\lyk.exe daemon run`,
  );
  await dialog.getByText('Can’t open a browser on that computer?', { exact: true }).click();
  await expect(dialog.getByText(/email code in the terminal/)).toBeVisible();
  await dialog.getByText('Can’t open a browser on that computer?', { exact: true }).click();
  await dialog.getByRole('tab', { name: 'macOS / Linux', exact: true }).click();
  await page.screenshot({ path: `${refinementArtifacts}/add-light.png` });
  await page.evaluate(async () => {
    const path = '/src/theme.ts';
    const { applyTheme } = await import(path);
    applyTheme('midnight');
  });
  await page.screenshot({ path: `${refinementArtifacts}/add-dark.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(dialog).toHaveCSS('width', '350px');
  await page.screenshot({ path: `${refinementArtifacts}/add-narrow.png` });
  await page.setViewportSize({ width: 1920, height: 1080 });
  const records = await registered(page.request, ws, 'Freshly connected computer');
  try {
    await expect(dialog.locator('.connection-status')).toHaveText(
      'Connected: Freshly connected computer',
    );
    await dialog.getByRole('link', { name: 'View computer' }).click();
    await expect(page).toHaveURL(new RegExp('tab=machines&machine=' + records[0]!.daemon_id + '$'));
    await page
      .getByRole('navigation', { name: 'Settings', exact: true })
      .getByRole('button', { name: 'Machines', exact: true })
      .click();
    await trigger.click();
    await expect(dialog.locator('.connection-status')).toHaveText('Waiting for your computer');
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(trigger).toBeFocused();
  } finally {
    for (const r of records)
      await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) });
  }
});

test('machine detail uses actual versions, canonical rename, keyboard links and live runtime refresh', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request);
  const records = await registered(page.request, ws, 'Detail computer');
  try {
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.goto('/#/machines');
    const link = page.getByRole('link', { name: /Detail computer/ });
    await expect(link).toBeVisible();
    await link.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(new RegExp('tab=machines&machine=' + records[0]!.daemon_id + '$'));
    await expect(page.getByRole('heading', { name: 'Detail computer', exact: true })).toBeVisible();
    const table = page.getByRole('table', { name: 'Runtimes' });
    await expect(table.getByRole('row')).toHaveCount(3);
    await expect(table.getByRole('row').filter({ hasText: 'Claude Code' })).toContainText('2.3.4');
    await expect(table.getByRole('row').filter({ hasText: 'Codex' })).toContainText('1.2.3');
    await expect(page.getByText('This device', { exact: true })).toHaveCount(0);
    await expect(page.getByText('Managed by Desktop', { exact: true })).toHaveCount(0);
    for (const name of ['View logs', 'Restart', 'Stop', 'Add custom runtime'])
      await expect(page.getByRole('button', { name, exact: true })).toBeDisabled();
    await expect(
      page.getByText(
        'Remote controls and custom runtimes are unavailable. Use Daemon instructions on the computer.',
        { exact: true },
      ),
    ).toBeVisible();
    await page.screenshot({ path: `${refinementArtifacts}/detail-light.png` });
    await page.evaluate(async () => {
      const path = '/src/theme.ts';
      const { applyTheme } = await import(path);
      applyTheme('midnight');
    });
    await page.screenshot({ path: `${refinementArtifacts}/detail-dark.png` });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    await page.screenshot({ path: `${refinementArtifacts}/detail-narrow.png` });
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.getByRole('button', { name: 'Rename machine', exact: true }).click();
    await page.getByRole('textbox', { name: 'Name' }).fill('Renamed detail computer');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(
      page.getByRole('heading', { name: 'Renamed detail computer', exact: true }),
    ).toBeVisible();
    expect(
      (await runtimeList(page, ws))
        .filter((r) => records.some((x) => x.id === r.id))
        .map((r) => r.custom_name),
    ).toEqual(['Renamed detail computer', 'Renamed detail computer']);
    const runtime = records.find((r) => r.harness === 'codex')!;
    expect(
      (await page.request.delete(`/api/runtimes/${runtime.id}`, { headers: headers(ws) })).status(),
    ).toBe(204);
    await expect(table.getByRole('row')).toHaveCount(2);
    await expect(table).not.toContainText('1.2.3');
    await page
      .getByRole('navigation', { name: 'Settings', exact: true })
      .getByRole('button', { name: 'Machines', exact: true })
      .click();
    await expect(
      page.getByRole('listitem').filter({ hasText: 'Renamed detail computer' }),
    ).toContainText('1 runtime');
  } finally {
    for (const r of records)
      await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) });
  }
});

test('supported daemon-reported providers retain actual versions and unreported values', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request);
  const token = await (
    await page.request.post('/api/tokens', { data: { name: 'Task16 runtime test' } })
  ).json();
  let records: MachineRuntime[] = [];
  try {
    const response = await page.request.post('/api/daemon/register', {
      headers: { Authorization: `Bearer ${token.token}` },
      data: {
        workspace_id: ws,
        daemon_id: `task16-${Date.now()}`,
        device_name: 'Supported provider computer',
        harnesses: ['claude', 'codex'],
        versions: { claude: 'actual-claude' },
      },
    });
    expect(response.status()).toBe(200);
    records = await response.json();
    await page.goto(`/#/machines/${records[0]!.daemon_id}`);
    const table = page.getByRole('table', { name: 'Runtimes' });
    for (const label of ['Claude Code', 'Codex']) await expect(table).toContainText(label);
    const claude = table.getByRole('row').filter({ hasText: 'Claude Code' });
    await expect(claude).toContainText('actual-claude');
    const codex = table.getByRole('row').filter({ hasText: 'Codex' });
    await expect(codex.locator('.runtime-version')).toHaveText('Not reported');
    await codex.getByRole('button', { name: 'Backend details' }).click();
    const detail = page.getByRole('dialog', { name: 'Codex backend details' });
    await expect(detail).toContainText('Not reported');
    await expect(detail).toContainText(
      records.find((r) => r.harness === 'codex')!.status === 'online' ? 'Online' : 'Offline',
    );
    await page.screenshot({ path: 'test-results/ui2-task16/machine-backend.png' });
  } finally {
    await page.request.delete(`/api/tokens/${token.id}`);
    for (const r of records)
      await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) });
  }
});

test('machine detail explains a failed sandbox and clears the reason after a ready heartbeat', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request);
  const records = await registered(page.request, ws, 'Sandbox readiness computer');
  const token = await (
    await page.request.post('/api/tokens', { data: { name: 'Sandbox heartbeat browser test' } })
  ).json();
  const heartbeat = async (sandbox: string, active = records) => {
    const response = await page.request.post('/api/daemon/heartbeat', {
      headers: { Authorization: `Bearer ${token.token}` },
      data: {
        runtimes: active.map((r) => ({ workspace_id: ws, runtime_id: r.id })),
        bases: { sandbox },
      },
    });
    expect(response.status()).toBe(200);
  };
  try {
    await page.goto(`/#/machines/${records[0]!.daemon_id}`);
    await expect(
      page.getByRole('heading', { name: 'Sandbox readiness computer', exact: true }),
    ).toBeVisible();
    const failure = page.getByRole('alert').filter({ hasText: "Can't run agents:" });
    await expect(failure).toHaveCount(0);
    await heartbeat('failed: install bubblewrap');
    await expect(failure).toHaveText("Can't run agents: install bubblewrap");
    await heartbeat(
      'ready',
      records.filter((r) => r.harness === 'claude'),
    );
    await expect(failure).toHaveCount(0);
  } finally {
    expect((await page.request.delete(`/api/tokens/${token.id}`)).status()).toBe(204);
    for (const r of records)
      await page.request.delete(`/api/runtimes/${r.id}`, { headers: headers(ws) });
  }
});
