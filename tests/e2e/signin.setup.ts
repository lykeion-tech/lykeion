import { expect, test as setup } from '@playwright/test';
import { EMAIL, labCode } from './lab';

// The lab allows five codes per address an hour; specs share this primary session.
setup('sign in by email code', async ({ page }) => {
  await page.goto('/#/studies');
  await expect(page).toHaveURL(/#\/signin$/);
  await page.screenshot({ path: 'test-results/ui-lab/task11-signin.png', fullPage: true });
  await page.getByRole('textbox', { name: 'Email' }).fill(EMAIL);
  await page.getByRole('button', { name: 'Continue with email' }).click();
  await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();
  await page.screenshot({ path: 'test-results/ui-lab/task11-code.png', fullPage: true });
  await page.getByRole('textbox', { name: 'Digit 1' }).click();
  await page.keyboard.type(await labCode(page.request, EMAIL));
  await expect(page).not.toHaveURL(/#\/signin$/);
  const me = await page.request.get('/api/me');
  expect((await me.json()).email).toBe(EMAIL);
  // The person every other spec acts as has a workspace and has finished onboarding;
  // the onboarding spec signs in as someone new.
  const created = await page.request.post('/api/workspaces', {
    data: { name: 'Beta-cell lab', slug: 'beta-cell-lab' },
  });
  expect(created.status()).toBe(201);
  // Studies the lab's agent escalates stay active, so the default WIP limit of two
  // would hold every later study in Backlog; specs that need a held Backlog lower it.
  const settings = await page.request.patch(
    `/api/workspaces/${(await created.json()).id}/settings`,
    {
      data: { settings: { wip_limit: 100 } },
    },
  );
  expect(settings.status()).toBe(204);
  expect(
    (await page.request.patch('/api/me', { data: { name: 'Ana Ruiz', onboarded: true } })).status(),
  ).toBe(200);
  await page.context().storageState({ path: 'test-results/ui-lab/session.json' });
});
