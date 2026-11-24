import { installFilesFixture } from './coresearch-files-fixture';
import { expect, test } from './test';

const project = { id: 'project', name: 'Fixture project', description: '', studies: [] };

test.beforeEach(async ({ page }) => {
  await installFilesFixture(page);
  await page.setViewportSize({ width: 1233, height: 864 });
  await page.route('**/api/researches/project', (route) => route.fulfill({ json: project }));
  await page.route('**/api/cards', (route) => route.fulfill({ json: [] }));
  await page.route('**/api/study-proposals?*', (route) =>
    route.fulfill({ json: { items: [], next_cursor: null } }),
  );
});

test('project tabs center their empty states in the content pane', async ({ page }) => {
  await page.route('**/api/researches/project/coresearch', (route) => route.fulfill({ json: [] }));
  await page.goto('/#/projects/project');
  for (const [tab, title] of [
    ['Studies', 'No studies'],
    ['Coresearches', 'No Coresearches linked'],
  ]) {
    await page.getByRole('tab', { name: tab, exact: true }).click();
    const panel = page.getByRole('tabpanel', { name: tab });
    const empty = panel.locator('.project-empty');
    await expect(empty.getByRole('heading', { name: title, exact: true })).toBeVisible();
    await expect(empty.locator('svg')).toHaveCount(1);
    await expect(panel.getByRole('button', { name: 'New study', exact: true })).toHaveCount(0);
    const bounds = (await empty.boundingBox())!;
    const pane = (await panel.boundingBox())!;
    expect(bounds.width).toBeCloseTo(pane.width, 0);
    expect(bounds.y + bounds.height).toBeCloseTo(pane.y + pane.height, 0);
    const contents = await empty.evaluate((element) => {
      const first = element.firstElementChild!.getBoundingClientRect();
      const last = element.lastElementChild!.getBoundingClientRect();
      return { x: first.x + first.width / 2, y: (first.top + last.bottom) / 2 };
    });
    expect(contents.x).toBeCloseTo(bounds.x + bounds.width / 2, 0);
    expect(contents.y).toBeCloseTo(bounds.y + bounds.height / 2, 0);
  }
});

test('Coresearches aligns its controls with Studies and filters chats by mode', async ({
  page,
}) => {
  await page.route('**/api/researches/project/coresearch', (route) =>
    route.fulfill({
      json: [
        { id: 'exp', title: 'Experiment chat', mode: 'experiment', archived_at: null },
        { id: 'draft', title: 'Cowrite chat', mode: 'cowrite', archived_at: null },
        { id: 'old', title: 'Archived chat', mode: 'experiment', archived_at: '2026-11-01' },
      ].map((chat) => ({ ...chat, updated_at: '2026-11-01', is_owner: false })),
    }),
  );
  await page.goto('/#/projects/project');
  const studyDisplay = (await page
    .getByRole('button', { name: 'Display', exact: true })
    .boundingBox())!;
  await page.getByRole('tab', { name: 'Coresearches', exact: true }).click();
  const panel = page.getByRole('tabpanel', { name: 'Coresearches' });
  const experiment = panel.getByRole('button', { name: 'Experiment chat', exact: true });
  const cowrite = panel.getByRole('button', { name: 'Cowrite chat', exact: true });
  const filter = panel.getByRole('button', { name: 'Filter', exact: true });
  const display = panel.getByRole('button', { name: 'Display', exact: true });
  await expect(experiment).toBeVisible();
  await expect(cowrite).toBeVisible();
  expect((await filter.boundingBox())!.y).toBe(studyDisplay.y);
  expect((await display.boundingBox())!.y).toBe(studyDisplay.y);
  await display.press('ArrowDown');
  await expect(panel.getByRole('menuitemcheckbox')).toHaveText(['experiment', 'cowrite']);
  await expect(
    panel.getByRole('menuitemcheckbox', { name: 'experiment', exact: true }),
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(display).toBeFocused();
  await expect(cowrite).toHaveCount(0);
  await expect(experiment).toBeVisible();
  await display.click();
  await panel.getByRole('menuitemcheckbox', { name: 'cowrite', exact: true }).click();
  await expect(experiment).toHaveCount(0);
  await expect(cowrite).toBeVisible();
  await filter.click();
  await panel.getByRole('menuitemradio', { name: 'Archived', exact: true }).click();
  await expect(panel.getByRole('heading', { name: 'No chats match these filters' })).toBeVisible();
  await display.click();
  await panel.getByRole('menuitemcheckbox', { name: 'cowrite', exact: true }).click();
  await expect(panel.getByRole('button', { name: 'Archived chat', exact: true })).toBeVisible();
});
