import { expect, test, type Page } from '@playwright/test';
import { labSignIn } from './lab';
test.use({ storageState: { cookies: [], origins: [] } });
const steps = ['Welcome', 'Workspace', 'Skills', 'Connectors', 'Machines'];
const shots = 'test-results/ui2-task16';
async function newcomer(page: Page, label: string) {
  await labSignIn(page.request, `onboarding-${label}@lab.test`);
  await page.goto('/#/onboarding');
  await expect(page.locator('.steps li')).toHaveText(steps);
}
test('five steps, Back and skips create a real workspace without a profile save', async ({
  page,
}) => {
  await newcomer(page, 'five');
  await page.getByRole('button', { name: 'Set up your lab' }).click();
  await expect(page.getByRole('textbox', { name: 'Research context' })).toHaveCount(0);
  await expect(page.getByRole('combobox', { name: 'Your career stage' })).toHaveCount(0);
  await page.getByRole('textbox', { name: 'Workspace name' }).fill('Zebrafish lab');
  await expect(page.getByRole('textbox', { name: 'URL' })).toHaveValue('zebrafish-lab');
  await page.getByRole('textbox', { name: 'Card prefix' }).fill('ZFISH');
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByRole('heading', { name: 'Skills', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Connectors', exact: true })).toBeVisible();
  await expect(page.getByRole('switch')).toHaveCount(0);
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(page.locator('[aria-current=step]')).toHaveText('Skills');
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await page.getByRole('button', { name: 'Skip', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Machines' })).toBeVisible();
  await expect(page.getByText('lyk daemon start')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Your lab is ready' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Enter workspace' }).click();
  await expect(page).toHaveURL(/#\/studies$/);
  const me = await (await page.request.get('/api/me')).json();
  expect(me.onboarded_at).not.toBeNull();
  expect(me.career_stage).toBe('');
  const [ws] = await (await page.request.get('/api/workspaces')).json();
  expect(ws).toMatchObject({ display_prefix: 'ZFISH', research_context: '' });
});
test('failed workspace creation stays on Workspace and retries once', async ({ page }) => {
  await newcomer(page, 'retry');
  await page.getByRole('button', { name: 'Set up your lab' }).click();
  await page.getByRole('textbox', { name: 'Workspace name' }).fill('Retry lab');
  let failed = false;
  await page.route('**/api/workspaces', async (route) => {
    if (route.request().method() === 'POST' && !failed) {
      failed = true;
      await route.abort('failed');
    } else await route.continue();
  });
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.locator('[aria-current=step]')).toHaveText('Workspace');
  expect(await (await page.request.get('/api/workspaces')).json()).toEqual([]);
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.locator('[aria-current=step]')).toHaveText('Skills');
  expect(await (await page.request.get('/api/workspaces')).json()).toHaveLength(1);
});
test('existing-member resume preserves saved career and context and Settings still edits career', async ({
  browser,
  page,
}) => {
  const owner = await browser.newContext({ storageState: 'test-results/ui-lab/session.json' });
  let workspace: { id: string; name: string } | undefined;
  try {
    const response = await owner.request.post('/api/workspaces', {
      data: { name: 'Member lab', slug: 'member-lab' },
    });
    expect(response.status()).toBe(201);
    workspace = await response.json();
    const ws = workspace!;
    expect(
      (
        await owner.request.patch(`/api/workspaces/${ws.id}`, {
          data: { research_context: 'Saved research context' },
        })
      ).status(),
    ).toBe(200);
    const email = 'onboarding-member@lab.test';
    const invitation = await owner.request.post(`/api/workspaces/${ws.id}/invitations`, {
      headers: { 'X-Workspace-ID': ws.id },
      data: { email, role: 'member' },
    });
    expect(invitation.status()).toBe(201);
    await labSignIn(page.request, email);
    expect(
      (
        await page.request.post(`/api/invitations/${(await invitation.json()).token}/accept`)
      ).status(),
    ).toBe(200);
    expect(
      (await page.request.patch('/api/me', { data: { career_stage: 'postdoc' } })).status(),
    ).toBe(200);
    await page.goto('/#/onboarding?step=workspace');
    await expect(page.locator('[aria-current=step]')).toHaveText('Workspace');
    const writes: string[] = [];
    page.on('request', (r) => {
      if (r.method() === 'PATCH') writes.push(r.url());
    });
    await page.getByRole('radio', { name: ws.name, exact: true }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.getByRole('button', { name: 'Skip', exact: true }).click();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await expect(page.locator('[aria-current=step]')).toHaveText('Machines');
    expect(writes).toEqual([]);
    expect((await (await page.request.get('/api/me')).json()).career_stage).toBe('postdoc');
    expect(
      (await (await owner.request.get(`/api/workspaces/${ws.id}`)).json()).research_context,
    ).toBe('Saved research context');
    await page.getByRole('button', { name: 'Enter workspace' }).click();
    await expect(page).toHaveURL(/#\/studies$/);
    await page.goto('/#/settings?tab=profile');
    await page.getByRole('combobox', { name: 'Your career stage' }).selectOption('phd');
    await expect
      .poll(async () => (await (await page.request.get('/api/me')).json()).career_stage)
      .toBe('phd');
  } finally {
    if (workspace) await owner.request.delete(`/api/workspaces/${workspace.id}`);
    await owner.close();
  }
});
test('bypass without a workspace resumes at the new Workspace step', async ({ page }) => {
  await newcomer(page, 'bypass');
  await page.getByRole('button', { name: "I've done this before" }).click();
  await expect(page).toHaveURL(/#\/onboarding\?step=workspace$/);
  await expect(page.locator('[aria-current=step]')).toHaveText('Workspace');
});
test('library pages read real shared data with four palettes and reachable responsive actions', async ({
  page,
}) => {
  await labSignIn(page.request, 'onboarding-library@lab.test');
  const response = await page.request.post('/api/workspaces', {
    data: { name: 'Library lab', slug: 'library-lab' },
  });
  expect(response.status()).toBe(201);
  const ws = await response.json();
  const headers = { 'X-Workspace-ID': ws.id };
  expect(
    (
      await page.request.post('/api/skills', {
        headers,
        data: { name: 'library-introduction', description: 'A real workspace skill.' },
      })
    ).status(),
  ).toBe(201);
  const connectors = await (await page.request.get('/api/connectors', { headers })).json();
  expect((await page.request.patch('/api/me', { data: { onboarded: true } })).status()).toBe(200);
  const canvases: string[] = [];
  for (const palette of ['ash', 'midnight', 'dawn', 'pale']) {
    await page.goto('/#/settings/preferences');
    await page
      .getByRole('button', { name: palette[0]!.toUpperCase() + palette.slice(1), exact: true })
      .click();
    await expect(page.locator('html')).toHaveAttribute('data-palette', palette);
    await page.goto('/#/onboarding?step=workspace');
    await page.getByRole('radio', { name: ws.name, exact: true }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await expect(page.getByText('library-introduction', { exact: true })).toBeVisible();
    await expect(page.getByText('A real workspace skill.', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add skill' })).toHaveCount(0);
    canvases.push(
      await page.locator('.wizard .rail').evaluate((el) => getComputedStyle(el).backgroundColor),
    );
    await page.screenshot({ path: `${shots}/${palette}-wizard-skills.png` });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.screenshot({ path: `${shots}/${palette}-wizard-skills-narrow.png` });
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Connectors', exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await page.getByRole('button', { name: 'Continue', exact: true }).focus();
    await expect(page.getByRole('button', { name: 'Continue', exact: true })).toBeFocused();
    await page.screenshot({ path: `${shots}/${palette}-wizard-connectors-narrow.png` });
    await page.getByRole('button', { name: 'Back', exact: true }).click();
    await page.setViewportSize({ width: 1512, height: 982 });
  }
  expect(new Set(canvases).size).toBe(4);
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Connectors', exact: true })).toBeVisible();
  for (const connector of connectors) {
    const row = page
      .locator('.onboarding-card')
      .filter({ has: page.getByText(connector.name, { exact: true }) });
    await expect(row).toContainText(connector.purpose);
    if (connector.enabled) await expect(row).not.toContainText('Available');
    else await expect(row).toContainText(connector.reason);
  }
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(page.locator('[aria-current=step]')).toHaveText('Skills');
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  const next = page.getByRole('button', { name: 'Continue', exact: true });
  await next.focus();
  await expect(next).toBeFocused();
  await expect(next).toBeInViewport();
  await page.screenshot({ path: `${shots}/mobile-wizard-connectors.png` });
  await next.click();
  await expect(page.locator('[aria-current=step]')).toHaveText('Machines');
});

test('a failed prefix save retries the created workspace without creating a duplicate', async ({
  page,
}) => {
  await newcomer(page, 'prefix-retry');
  await page.getByRole('button', { name: 'Set up your lab' }).click();
  await page.getByRole('textbox', { name: 'Workspace name' }).fill('Prefix retry lab');
  await page.getByRole('textbox', { name: 'Card prefix' }).fill('PREFIX');
  let fail = true;
  await page.route('**/api/workspaces/*', async (route) => {
    if (route.request().method() === 'PATCH' && fail) {
      fail = false;
      await route.abort('failed');
    } else await route.continue();
  });
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(page.locator('[aria-current=step]')).toHaveText('Skills');
  const saved = await (await page.request.get('/api/workspaces')).json();
  expect(saved).toHaveLength(1);
  expect(saved[0].display_prefix).toBe('PREFIX');
});

for (const phase of ['create', 'list'] as const) {
  test(`a delayed real ${phase} response cannot retarget a later workspace visit`, async ({
    browser,
  }) => {
    const owner = await browser.newContext({ storageState: 'test-results/ui-lab/session.json' });
    const controlled = await owner.newPage();
    const created: string[] = [];
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let received!: () => void;
    const held = new Promise<void>((resolve) => {
      received = resolve;
    });
    try {
      const make = async (name: string) => {
        const response = await owner.request.post('/api/workspaces', {
          data: { name, slug: `${phase}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}` },
        });
        expect(response.status()).toBe(201);
        const workspace = await response.json();
        created.push(workspace.id);
        return workspace;
      };
      const a = await make(`Onboarding A ${phase}`),
        b = await make(`Onboarding B ${phase}`);
      await controlled.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), a.id);
      await controlled.goto('/#/onboarding?step=workspace');
      // The hold below must catch a refetch, not the list's first load: the
      // switcher can only offer B once the list has arrived.
      await expect(controlled.getByRole('radio', { name: b.name, exact: true })).toBeVisible();
      await controlled.getByRole('radio', { name: 'Create a new workspace' }).check();
      await controlled
        .getByRole('textbox', { name: 'Workspace name' })
        .fill(`Late workspace ${phase}`);
      let pending = true;
      await controlled.route('**/api/workspaces', async (route) => {
        const target = phase === 'create' ? 'POST' : 'GET';
        if (route.request().method() === 'POST') {
          const response = await route.fetch();
          const record = await response.json();
          created.push(record.id);
          if (phase === 'create' && pending) {
            pending = false;
            received();
            await gate;
          }
          await route.fulfill({ response });
        } else if (route.request().method() === target && pending) {
          pending = false;
          const response = await route.fetch();
          received();
          await gate;
          await route.fulfill({ response });
        } else await route.continue();
      });
      await controlled.getByRole('button', { name: 'Create workspace' }).click();
      await held;
      await controlled.goto('/#/studies');
      await controlled.locator('.workspace-switch').click();
      await controlled.getByRole('menuitem', { name: b.name, exact: false }).last().click();
      await expect(controlled.locator('.workspace-switch')).toContainText(b.name);
      release();
      await controlled.unrouteAll({ behavior: 'wait' });
      await controlled.evaluate(
        () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
      );
      await expect(controlled).toHaveURL(/#\/studies$/);
      await expect(controlled.locator('.workspace-switch')).toContainText(b.name);
      await expect(controlled.getByRole('heading', { name: 'Skills', exact: true })).toHaveCount(0);
    } finally {
      release();
      await controlled.unrouteAll({ behavior: 'wait' });
      for (const id of created) await owner.request.delete(`/api/workspaces/${id}`);
      await owner.close();
    }
  });
}
