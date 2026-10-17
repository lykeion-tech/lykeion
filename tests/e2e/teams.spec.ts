import { expect, test } from '@playwright/test';
import { labFixture, labNoTeam, labPackArchive, labShip, labSignIn, labUnship } from './lab';

test('the sidebar shows Teams between Studies and Colleagues', async ({ page }) => {
  await page.goto('/#/studies');
  const nav = page.getByRole('complementary', { name: 'Workspace navigation' });
  const names = await nav.getByRole('link').allInnerTexts();
  const at = (name: string) => names.findIndex((n) => n.trim() === name);
  expect(at('Teams')).toBeGreaterThan(at('Studies'));
  expect(at('Teams')).toBeLessThan(at('Colleagues'));
  await nav.getByRole('link', { name: 'Teams', exact: true }).click();
  await expect(page).toHaveURL(/#\/teams$/);
  await expect(page.getByRole('heading', { name: 'Teams', level: 1 })).toBeVisible();
});

test('Teams lists shipped and own Teams, approves a new version and changes the Team', async ({
  page,
  browser,
}) => {
  const f = await labFixture(page.request);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  const before = (await (await page.request.get('/api/me')).json()).pack_name as string;
  expect(before).toBe('');
  labShip('demo', '1');
  try {
    expect(
      (await page.request.put('/api/me/pack', { headers, data: { name: 'demo' } })).status(),
    ).toBe(200);
    await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
    await page.goto('/#/teams');

    const shipped = page.getByRole('region', { name: "Lykeion's Teams" });
    const demo = shipped.getByRole('listitem').filter({ hasText: 'demo' });
    await expect(demo).toContainText('Your Team');
    await expect(demo).toContainText('Starts from: question, claim');
    await expect(demo).toContainText('Grounding: demo-grounding-author');
    await expect(demo).toContainText('v1');
    await expect(page.locator('#page-header-count')).toHaveText('1');
    const notice = page.getByRole('status').filter({ hasText: 'needs your approval' });
    await expect(notice).toHaveCount(0);

    // › opens the versions with this person's approvals.
    await demo.getByRole('button', { name: /0 studies ›/ }).click();
    const versions = page.getByRole('table', { name: 'demo versions' });
    await expect(versions.getByRole('row').filter({ hasText: 'v1' })).toContainText(
      'Approved by you',
    );

    // A newer shipped version waits for this person's approval.
    labShip('demo', '2');
    await page.reload();
    await expect(notice).toContainText('demo Team v2 is available and needs your approval.');
    await expect(demo).toContainText('v2');
    await page.screenshot({ path: 'test-results/ui-lab/teams.png' });
    await notice.getByRole('button', { name: 'Approve v2' }).click();
    await expect(notice).toHaveCount(0);
    await demo.getByRole('button', { name: /studies ›/ }).click();
    await expect(versions.getByRole('row').filter({ hasText: 'v2' })).toContainText(
      'Approved by you',
    );

    // An upload is listed under Your Teams and can become this person's Team.
    const chooser = page.waitForEvent('filechooser');
    await page.getByRole('button', { name: 'Upload a Team' }).click();
    await (
      await chooser
    ).setFiles({
      name: 'ana-pack.zip',
      mimeType: 'application/zip',
      buffer: labPackArchive('ana-pack'),
    });
    const own = page.getByRole('region', { name: 'Your Teams' });
    const ana = own.getByRole('listitem').filter({ hasText: 'ana-pack' });
    await expect(ana).toContainText('Uploaded by you');
    await expect(page.locator('#page-header-count')).toHaveText('2');
    page.once('dialog', (d) => void d.accept());
    await ana.getByRole('button', { name: 'Make my Team' }).click();
    await expect(ana).toContainText('Your Team');
    await expect(demo).not.toContainText('Your Team');
    await expect(demo.getByRole('button', { name: 'Make my Team' })).toBeVisible();
    expect((await (await page.request.get('/api/me')).json()).pack_name).toBe('ana-pack');

    // A colleague in the same workspace sees the shipped Team, not the upload.
    const email = `teams-${Date.now()}@lab.test`;
    const other = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    try {
      const invite = await page.request.post(`/api/workspaces/${f.workspace_id}/invitations`, {
        headers,
        data: { email, role: 'member' },
      });
      expect(invite.status()).toBe(201);
      await labSignIn(other.request, email);
      expect(
        (
          await other.request.post(`/api/invitations/${(await invite.json()).token}/accept`)
        ).status(),
      ).toBe(200);
      expect(
        (await other.request.patch('/api/me', { data: { name: email, onboarded: true } })).status(),
      ).toBe(200);
      const theirs = await other.newPage();
      await theirs.addInitScript(
        (id) => localStorage.setItem('lykeion.workspace', id),
        f.workspace_id,
      );
      await theirs.goto('/#/teams');
      await expect(
        theirs.getByRole('region', { name: "Lykeion's Teams" }).getByText('demo', { exact: true }),
      ).toBeVisible();
      await expect(theirs.getByText('ana-pack')).toHaveCount(0);
    } finally {
      await other.close();
    }
  } finally {
    labUnship('demo');
    await labNoTeam(page.request);
  }
});
