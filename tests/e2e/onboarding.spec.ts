import { expect, test } from '@playwright/test';
import { labCode, labNoTeam, labShip, labSignIn, labUnship } from './lab';

// A newcomer, not the lab's own person, so onboarding has not been done.
const NEWCOMER = 'newcomer@lab.test';
test.use({ storageState: { cookies: [], origins: [] } });

test('a newcomer goes through the six steps to an onboarded workspace', async ({ page }) => {
  await page.goto('/#/studies');
  await page.getByRole('textbox', { name: 'Email' }).fill(NEWCOMER);
  await page.getByRole('button', { name: 'Continue with email' }).click();
  await page.getByRole('textbox', { name: 'Digit 1' }).click();
  await page.keyboard.type(await labCode(page.request, NEWCOMER));
  await expect(page).toHaveURL(/#\/onboarding/);

  await expect(page.getByRole('heading', { name: /A lab for your/ })).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task12-welcome.png', fullPage: true });
  await page.getByRole('button', { name: 'Set up your lab' }).click();

  await page.screenshot({ path: 'test-results/ui-lab/task12-profile.png', fullPage: true });
  await page
    .getByRole('textbox', { name: 'Research context' })
    .fill('Zebrafish heart regeneration by scRNA-seq.');
  await page.getByRole('combobox', { name: 'Your career stage' }).selectOption('postdoc');
  await page.getByRole('button', { name: 'Continue' }).click();

  await page.getByRole('radio', { name: 'Create a new workspace' }).check();
  await page.getByRole('textbox', { name: 'Workspace name' }).fill('Zebrafish lab');
  await expect(page.getByRole('textbox', { name: 'URL' })).toHaveValue('zebrafish-lab');
  await expect(page.getByRole('textbox', { name: 'Card prefix' })).toHaveAttribute(
    'placeholder',
    'ZEBR',
  );
  await page.getByRole('textbox', { name: 'Card prefix' }).fill('ZFISH');
  await page.screenshot({ path: 'test-results/ui-lab/task12-workspace.png', fullPage: true });
  await page.getByRole('button', { name: 'Create workspace' }).click();

  await expect(page.getByRole('button', { name: 'Continue' })).toBeDisabled();
  await page.getByRole('button', { name: 'Skip for now' }).click();
  await expect(page.getByRole('heading', { name: 'Connect a research runtime' })).toBeVisible();
  await expect(page.getByText('lyk daemon start')).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task12-runtime.png', fullPage: true });
  await page.getByRole('button', { name: 'Skip for now' }).click();

  await expect(page.getByText('Europe PMC', { exact: false })).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task12-capabilities.png', fullPage: true });
  await page.getByRole('button', { name: 'Continue' }).click();

  await expect(page.getByText('Zebrafish lab')).toBeVisible();
  await expect(page.getByText('Zebrafish heart regeneration by scRNA-seq.')).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task12-ready.png', fullPage: true });
  await page.getByRole('button', { name: 'Enter workspace' }).click();
  await expect(page).toHaveURL(/#\/studies$/);

  const me = await (await page.request.get('/api/me')).json();
  expect(me.career_stage).toBe('postdoc');
  expect(me.onboarded_at).not.toBeNull();
  const [ws] = await (await page.request.get('/api/workspaces')).json();
  expect(ws.display_prefix).toBe('ZFISH');
  expect(ws.research_context).toBe('Zebrafish heart regeneration by scRNA-seq.');
});

test('a failed context save retries the created workspace without creating it again', async ({
  page,
}) => {
  await page.goto('/#/studies');
  const email = 'onboarding-retry@lab.test';
  await page.getByRole('textbox', { name: 'Email' }).fill(email);
  await page.getByRole('button', { name: 'Continue with email' }).click();
  await page.getByRole('textbox', { name: 'Digit 1' }).click();
  await page.keyboard.type(await labCode(page.request, email));
  await expect(page).toHaveURL(/#\/onboarding/);
  await page.getByRole('button', { name: 'Set up your lab' }).click();
  await page.getByRole('textbox', { name: 'Research context' }).fill('Retained context');
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('textbox', { name: 'Workspace name' }).fill('Retry lab');
  await page.getByRole('textbox', { name: 'Card prefix' }).fill('RETRY');
  let creates = 0;
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith('/api/workspaces')) creates++;
  });
  let failPatch = true;
  await page.route('**/api/workspaces/*', async (route) => {
    if (route.request().method() === 'PATCH' && failPatch) {
      failPatch = false;
      await route.abort('failed');
    } else await route.continue();
  });
  await page.getByRole('button', { name: 'Create workspace' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  expect(await (await page.request.get('/api/workspaces')).json()).toEqual([
    expect.objectContaining({ name: 'Retry lab', research_context: '' }),
  ]);
  await page.getByRole('radio', { name: 'Retry lab', exact: true }).check();
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await page.getByRole('button', { name: 'Skip for now' }).click();
  await expect(page.getByRole('heading', { name: 'Connect a research runtime' })).toBeVisible();
  expect(creates).toBe(1);
  expect(await (await page.request.get('/api/workspaces')).json()).toEqual([
    expect.objectContaining({
      name: 'Retry lab',
      research_context: 'Retained context',
      display_prefix: 'RETRY',
    }),
  ]);
});

test('direct sign-in onboards a newcomer and no-workspace completion returns to Workspace', async ({
  page,
}) => {
  await page.goto('/#/signin');
  const email = 'onboarding-direct@lab.test';
  await page.getByRole('textbox', { name: 'Email' }).fill(email);
  await page.getByRole('button', { name: 'Continue with email' }).click();
  await page.getByRole('textbox', { name: 'Digit 1' }).click();
  await page.keyboard.type(await labCode(page.request, email));
  await expect(page).toHaveURL(/#\/onboarding$/);
  await page.getByRole('button', { name: "I've done this before" }).click();
  await expect(page).toHaveURL(/#\/onboarding\?step=workspace$/);
  await expect(page.getByRole('textbox', { name: 'Workspace name' })).toBeVisible();
  expect((await (await page.request.get('/api/me')).json()).onboarded_at).not.toBeNull();
});

test('a member sees the real research-context refusal and can continue without overwriting it', async ({
  browser,
  page,
}) => {
  const owner = await browser.newContext({ storageState: 'test-results/ui-lab/session.json' });
  const created = await owner.request.post('/api/workspaces', {
    data: { name: 'Member lab', slug: 'member-lab' },
  });
  expect(created.status()).toBe(201);
  const workspace = await created.json();
  const email = 'onboarding-member@lab.test';
  const invitation = await owner.request.post(`/api/workspaces/${workspace.id}/invitations`, {
    headers: { 'X-Workspace-ID': workspace.id },
    data: { email, role: 'member' },
  });
  expect(invitation.status()).toBe(201);
  await labSignIn(page.request, email);
  expect(
    (
      await page.request.post(`/api/invitations/${(await invitation.json()).token}/accept`)
    ).status(),
  ).toBe(200);
  await page.goto('/#/studies');
  await expect(page).toHaveURL(/#\/onboarding$/);
  await page.getByRole('button', { name: 'Set up your lab' }).click();
  await page.getByRole('textbox', { name: 'Research context' }).fill('Member context');
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('radio', { name: 'Member lab', exact: true }).check();
  const refusal = page.waitForResponse(
    (response) =>
      response.request().method() === 'PATCH' &&
      response.url().endsWith(`/api/workspaces/${workspace.id}`),
  );
  await page.getByRole('button', { name: 'Continue' }).click();
  expect((await refusal).status()).toBe(403);
  await expect(page.getByRole('alert')).toBeVisible();
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Research context' })).toHaveValue(
    'Member context',
  );
  await page.getByRole('textbox', { name: 'Research context' }).fill('');
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('button', { name: 'Skip for now' }).click();
  await expect(page.getByRole('heading', { name: 'Connect a research runtime' })).toBeVisible();
  await page.getByRole('button', { name: 'Log out' }).click();
  await expect(page).toHaveURL(/#\/signin$/);
  expect((await page.request.get('/api/me')).status()).toBe(401);
  await owner.close();
});

test('the Team step follows Workspace, can be skipped, and is not asked twice', async ({
  page,
}) => {
  const email = 'onboarding-team@lab.test';
  labShip('demo', '1');
  try {
    await labSignIn(page.request, email);
    await page.goto('/#/onboarding?step=workspace');
    await page.getByRole('textbox', { name: 'Workspace name' }).fill('Team lab');
    await page.getByRole('button', { name: 'Create workspace' }).click();

    await expect(
      page.getByRole('heading', { name: 'Which Team should work your studies?' }),
    ).toBeVisible();
    await expect(page.getByRole('listitem').filter({ hasText: 'Your Team' })).toHaveAttribute(
      'aria-current',
      'step',
    );
    await expect(page.getByRole('button', { name: 'Continue' })).toBeDisabled();
    await expect(page.getByText('Studies start from: question, claim')).toBeVisible();
    await expect(page.getByText('Choosing a Team approves its current version (v1)')).toBeVisible();
    await expect(page.getByText('You can change your Team later on the Teams page.')).toBeVisible();
    await expect(page.getByText('Bring your own Team')).toBeVisible();
    await page.screenshot({ path: 'test-results/ui-lab/onboarding-team.png', fullPage: true });
    // The upload row is drawn like the Team rows above it.
    const look = (selector: string) =>
      page
        .locator(selector)
        .first()
        .evaluate((el) => {
          const s = getComputedStyle(el);
          return [
            s.borderTopStyle,
            s.borderTopWidth,
            s.fontSize,
            s.fontFamily,
            s.lineHeight,
            s.background,
            s.borderRadius,
          ];
        });
    expect(await look('.choice.upload')).toEqual(await look('label.choice'));

    // Skipping leaves the person without a Team.
    await page.getByRole('button', { name: 'Skip for now' }).click();
    await expect(page.getByRole('heading', { name: 'Connect a research runtime' })).toBeVisible();
    expect((await (await page.request.get('/api/me')).json()).pack_name).toBe('');

    await page.goto('/#/onboarding?step=workspace');
    await page.reload();
    await page.getByRole('radio', { name: 'Team lab', exact: true }).check();
    await page.getByRole('button', { name: 'Continue' }).click();
    await page.getByRole('radio', { name: 'demo Team' }).check();
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByRole('heading', { name: 'Connect a research runtime' })).toBeVisible();
    expect((await (await page.request.get('/api/me')).json()).pack_name).toBe('demo');
    // A Team chosen in this run is still a step Back can return to.
    await page.getByRole('button', { name: 'Back', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Which Team should work your studies?' }),
    ).toBeVisible();

    // With a Team already chosen, the wizard goes from Workspace to Runtime and back.
    await page.goto('/#/onboarding?step=workspace');
    await page.reload();
    await page.getByRole('radio', { name: 'Team lab', exact: true }).check();
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByRole('heading', { name: 'Connect a research runtime' })).toBeVisible();
    await page.getByRole('button', { name: 'Back', exact: true }).click();
    await expect(page.getByRole('radio', { name: 'Team lab', exact: true })).toBeVisible();
  } finally {
    labUnship('demo');
    await labNoTeam(page.request);
  }
});
