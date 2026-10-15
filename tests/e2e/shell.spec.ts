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

test('the workspace menu names the workspace and logs out', async ({ page }) => {
  await page.goto('/#/studies');
  await page.getByRole('button', { name: /Beta-cell lab/ }).click();
  const menu = page.getByRole('menu', { name: 'Workspace' });
  await expect(menu.getByRole('menuitem', { name: /Ana Ruiz/ })).toBeVisible();
  await expect(menu.getByRole('menuitem', { name: /Beta-cell lab/ })).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task11-workspace-menu.png', fullPage: true });
  await menu.getByRole('menuitem', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/#\/signin$/);
  expect((await page.request.get('/api/me')).status()).toBe(401);
});
