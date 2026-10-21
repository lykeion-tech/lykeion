import { expect, test } from '@playwright/test';
import { LAB, labMember, labWorkspace, newStudy } from './lab';

const artifacts = '.superpowers/sdd/2026-10-21-team-ratings/task-5-artifacts';

test('the owner rates a Team column, changes, clears and dismisses it; a colleague sees nothing', async ({
  page,
  browser,
}) => {
  const study = await newStudy(page.request, `Ratings live check ${Date.now()}?`, {
    title: 'Ratings live check',
  });
  expect((await page.request.post(`${LAB}/lab/rating-offer/${study.id}/grounding`)).status()).toBe(
    204,
  );
  await page.goto(`/#/studies/${study.id}`);
  const main = page.getByRole('region', { name: 'Study' });
  const offer = main.getByRole('region', { name: 'Rate the Team in Grounding' });
  await expect(offer.getByText('How did the Team do in this column? (optional)')).toBeVisible();
  await page.screenshot({ path: `${artifacts}/1-offered.png` });

  const pressed = (n: string) => offer.getByRole('button', { name: n, exact: true });
  const saved = (click: Promise<void>) =>
    Promise.all([
      page.waitForResponse((r) => r.url().includes('/ratings/grounding') && r.ok()),
      click,
    ]);
  await saved(pressed('3').click());
  await expect(pressed('3')).toHaveAttribute('aria-pressed', 'true');
  await page.reload();
  await expect(pressed('3')).toHaveAttribute('aria-pressed', 'true');
  await page.screenshot({ path: `${artifacts}/2-rated-3.png` });

  await saved(pressed('1').click());
  await page.reload();
  await expect(pressed('1')).toHaveAttribute('aria-pressed', 'true');
  await expect(pressed('3')).toHaveAttribute('aria-pressed', 'false');

  await saved(offer.getByRole('button', { name: 'Clear' }).click());
  await page.reload();
  for (const n of ['1', '2', '3', '4'])
    await expect(pressed(n)).toHaveAttribute('aria-pressed', 'false');

  const colleague = await labMember(browser, page.request, await labWorkspace(page.request));
  await colleague.page.goto(`/#/studies/${study.id}`);
  await expect(
    colleague.page
      .getByRole('region', { name: 'Study' })
      .getByRole('heading', { name: 'Ratings live check' }),
  ).toBeVisible();
  await expect(
    colleague.page.getByText('How did the Team do in this column? (optional)'),
  ).toHaveCount(0);
  await colleague.page.screenshot({ path: `${artifacts}/3-colleague.png` });
  await colleague.context.close();

  await offer.getByRole('button', { name: 'Dismiss' }).click();
  await expect(offer).toHaveCount(0);
  await page.reload();
  await expect(main.getByRole('heading', { name: 'Ratings live check' })).toBeVisible();
  await expect(offer).toHaveCount(0);
  await page.screenshot({ path: `${artifacts}/4-dismissed.png` });
});
