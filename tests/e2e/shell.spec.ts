import { expect, test } from '@playwright/test';
import { labCode, labSignIn } from './lab';

test('the sidebar shows the UI-1 destinations only', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  await expect(nav.getByRole('button', { name: /Beta-cell lab/ })).toBeVisible();
  for (const name of ['New', 'Inbox', 'Studies', 'Colleagues', 'Machines', 'Settings'])
    await expect(
      nav
        .getByRole('link', { name, exact: true })
        .or(nav.getByRole('button', { name, exact: true })),
    ).toBeVisible();
  for (const name of [
    'Coresearch',
    'Researches',
    'Autoresearch',
    'Agents',
    'Teams',
    'Skills',
    'Connectors',
    'Updates',
    'Search...',
  ])
    await expect(nav.getByText(name, { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Open Aristotle' })).toHaveCount(0);
  await page.screenshot({ path: 'test-results/ui-lab/task11-shell.png', fullPage: true });
});

test("a later slice's route and an unknown route land on Studies", async ({ page }) => {
  for (const path of ['/#/agents', '/#/autoresearch', '/#/no-such-page']) {
    await page.goto(path);
    await expect(page).toHaveURL(/#\/studies$/);
  }
});

test('an invitation link asks a signed-out person to sign in first', async ({ browser }) => {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const page = await context.newPage();
  await page.goto('/#/invite/inv_test');
  await expect(page).toHaveURL(/#\/signin$/);
  expect(await page.evaluate(() => sessionStorage.getItem('lykeion.invite'))).toBe('inv_test');
  await context.close();
});

test('signing in and switching accounts refreshes the shell without a reload', async ({
  browser,
}) => {
  const people = [
    {
      email: 'shell-return@lab.test',
      name: 'Rae Return',
      workspace: 'Return lab',
      slug: 'return-lab',
    },
    { email: 'shell-other@lab.test', name: 'Oli Other', workspace: 'Other lab', slug: 'other-lab' },
  ];
  const workspaceIds: string[] = [];
  for (const person of people) {
    const fixture = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    await labSignIn(fixture.request, person.email);
    const created = await fixture.request.post('/api/workspaces', {
      data: { name: person.workspace, slug: person.slug },
    });
    expect(created.status()).toBe(201);
    workspaceIds.push((await created.json()).id);
    expect(
      (
        await fixture.request.patch('/api/me', { data: { name: person.name, onboarded: true } })
      ).status(),
    ).toBe(200);
    await fixture.close();
  }
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const page = await context.newPage();
  await page.goto('/#/studies');
  for (const [index, person] of people.entries()) {
    await expect(page).toHaveURL(/#\/signin$/);
    await page.getByRole('textbox', { name: 'Email' }).fill(person.email);
    await page.getByRole('button', { name: 'Continue with email' }).click();
    await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();
    await page.getByRole('textbox', { name: 'Digit 1' }).click();
    await page.keyboard.type(await labCode(page.request, person.email));
    await expect(page).toHaveURL(/#\/studies$/);
    await expect(page.getByRole('button', { name: new RegExp(person.workspace) })).toBeVisible();
    await page.getByRole('button', { name: new RegExp(person.workspace) }).click();
    const menu = page.getByRole('menu', { name: 'Workspace' });
    await expect(menu.getByRole('menuitem', { name: new RegExp(person.name) })).toBeVisible();
    await expect(menu.getByRole('menuitem', { name: new RegExp(person.workspace) })).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem('lykeion.workspace'))).toBe(
      workspaceIds[index],
    );
    if (index === 1) {
      await expect(menu.getByRole('menuitem', { name: /Return lab|Rae Return/ })).toHaveCount(0);
      await page.screenshot({
        path: 'test-results/ui-lab/task11-account-switch.png',
        fullPage: true,
      });
    }
    await menu.getByRole('menuitem', { name: 'Log out' }).click();
    await expect(page).toHaveURL(/#\/signin$/);
    expect((await page.request.get('/api/me')).status()).toBe(401);
    expect(await page.evaluate(() => localStorage.getItem('lykeion.workspace'))).toBe('');
  }
  await context.close();
});

for (const failedRead of [
  { name: 'identity', pattern: /\/api\/me$/ },
  { name: 'workspace list', pattern: /\/api\/workspaces$/ },
  { name: 'selected workspace', pattern: /\/api\/workspaces\/[^/?]+$/ },
]) {
  test(`an unavailable initial ${failedRead.name} read shows recovery and retries the real server`, async ({
    page,
  }) => {
    await page.route(failedRead.pattern, async (route) => {
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: `${failedRead.name} temporarily unavailable` }),
      });
    });
    await page.goto('/#/studies');
    const alert = page.getByRole('alert');
    await expect(alert).toContainText(`${failedRead.name} temporarily unavailable`);
    await expect(page.getByRole('button', { name: 'Board', exact: true })).toHaveCount(0);
    await page.screenshot({
      path: `test-results/ui-lab/task11-recovery-${failedRead.name.replaceAll(' ', '-')}.png`,
      fullPage: true,
    });
    await page.unroute(failedRead.pattern);
    let finishRetry!: () => void;
    const retryGate = new Promise<void>((resolve) => {
      finishRetry = resolve;
    });
    await page.route(failedRead.pattern, async (route) => {
      const response = await route.fetch();
      await retryGate;
      await route.fulfill({ response });
    });
    const retryRequest = page.waitForRequest(failedRead.pattern);
    await alert.getByRole('button', { name: 'Try again' }).click();
    await retryRequest;
    await expect(page.getByRole('status')).toContainText('Loading workspace');
    await expect(page.getByRole('button', { name: 'Try again', disabled: false })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Board', exact: true })).toHaveCount(0);
    finishRetry();
    await expect(alert).toHaveCount(0);
    await page.unroute(failedRead.pattern);
    await expect(page.getByRole('button', { name: /Beta-cell lab/ })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Board', exact: true })).toBeVisible();
  });
}

test('a real invitation returns through email sign-in and joins the invited workspace', async ({
  browser,
  request,
}) => {
  const created = await request.post('/api/workspaces', {
    data: { name: 'Invitation lab', slug: 'invitation-lab' },
  });
  expect(created.status()).toBe(201);
  const workspace = await created.json();
  const email = 'shell-invite@lab.test';
  const invited = await request.post(`/api/workspaces/${workspace.id}/invitations`, {
    headers: { 'X-Workspace-ID': workspace.id },
    data: { email, role: 'member' },
  });
  expect(invited.status()).toBe(201);
  const invitation = await invited.json();
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const page = await context.newPage();
  await page.goto('/#/invite/' + invitation.token);
  await expect(page).toHaveURL(/#\/signin$/);
  expect(await page.evaluate(() => sessionStorage.getItem('lykeion.invite'))).toBe(
    invitation.token,
  );
  await page.getByRole('textbox', { name: 'Email' }).fill(email);
  await page.getByRole('button', { name: 'Continue with email' }).click();
  await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();
  await page.getByRole('textbox', { name: 'Digit 1' }).click();
  await page.keyboard.type(await labCode(page.request, email));
  await expect(page).toHaveURL(/#\/onboarding$/);
  await page.getByRole('button', { name: "I've done this before" }).click();
  await expect(page).toHaveURL(/#\/studies$/);
  await expect(page.getByRole('button', { name: /Invitation lab/ })).toBeVisible();
  expect(await page.evaluate(() => sessionStorage.getItem('lykeion.invite'))).toBe('');
  expect(await page.evaluate(() => localStorage.getItem('lykeion.workspace'))).toBe(workspace.id);
  const memberships = await page.request.get('/api/workspaces');
  expect(memberships.status()).toBe(200);
  expect(await memberships.json()).toEqual(
    expect.arrayContaining([expect.objectContaining({ id: workspace.id, role: 'member' })]),
  );
  await page.screenshot({
    path: 'test-results/ui-lab/task11-invitation-accepted.png',
    fullPage: true,
  });
  await context.close();
});

test('Google sign-in targets the shipped backend route', async ({ browser }) => {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const page = await context.newPage();
  await page.goto('/#/signin');
  const response = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/auth/google',
  );
  await page.getByRole('button', { name: 'Continue with Google' }).click();
  const backend = await response;
  expect(backend.status()).toBe(404);
  expect(await backend.json()).toEqual({ error: 'google sign-in is not configured' });
  await expect(page).toHaveURL(/\/auth\/google$/);
  await context.close();
});

test('the workspace menu names the workspace and logs out', async ({ page }) => {
  await page.goto('/#/studies');
  await page.getByRole('button', { name: /Beta-cell lab/ }).click();
  const menu = page.getByRole('menu', { name: 'Workspace' });
  await expect(menu.getByRole('menuitem', { name: /Ana Ruiz/ })).toBeVisible();
  await expect(menu.getByRole('menuitem', { name: /Beta-cell lab/ })).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task11-workspace-menu.png', fullPage: true });
  const signedOutRead = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/me' && response.status() === 401,
  );
  await menu.getByRole('menuitem', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/#\/signin$/);
  await signedOutRead;
  expect((await page.request.get('/api/me')).status()).toBe(401);
  for (const destination of ['/studies', '/inbox', '/settings']) {
    await page.evaluate((destination) => {
      location.hash = destination;
    }, destination);
    await expect(page).toHaveURL(/#\/signin$/);
    await expect(page.getByRole('complementary', { name: 'Workspace navigation' })).toHaveCount(0);
  }
});
