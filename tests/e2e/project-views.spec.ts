import { installAristotleFixture } from './aristotle-fixture';
import { expect, test } from './test';

const project = { id: 'project', name: 'Fixture project', description: '', studies: [] };

test.beforeEach(async ({ page }) => {
  await installAristotleFixture(page);
  await page.setViewportSize({ width: 1233, height: 864 });
  await page.route('**/api/researches', (route) => route.fulfill({ json: [project] }));
  await page.route('**/api/researches/project', (route) => route.fulfill({ json: project }));
  await page.route('**/api/cards', (route) => route.fulfill({ json: [] }));
  await page.route('**/api/study-proposals?*', (route) =>
    route.fulfill({ json: { items: [], next_cursor: null } }),
  );
});

test('project tabs center their empty states in the content pane', async ({ page }) => {
  await page.addInitScript(() =>
    localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: 'midnight' })),
  );
  await page.route('**/api/researches/project/coresearch', (route) => route.fulfill({ json: [] }));
  await page.goto('/#/projects/project');
  await expect(page.getByRole('complementary', { name: 'Project properties' })).toHaveCSS(
    'width',
    '240px',
  );
  const studyDisplay = await page
    .getByRole('button', { name: 'Display', exact: true })
    .boundingBox();
  const studyLayout = await page.getByRole('button', { name: /^(Board|List)$/ }).boundingBox();
  for (const [tab, title, description, action] of [
    [
      'Studies',
      'No studies',
      'Start a new study or add an existing one from Studies.',
      'New study',
    ],
    [
      'Coresearches',
      'No Coresearches linked',
      'Start a new chat or add an existing one from Coresearch.',
      'New chat',
    ],
  ]) {
    await page.getByRole('tab', { name: tab, exact: true }).click();
    const panel = page.getByRole('tabpanel', { name: tab });
    const empty = panel.locator('.project-empty');
    await expect(empty.getByRole('heading', { name: title, exact: true })).toBeVisible();
    await expect(empty.getByText(description, { exact: true })).toBeVisible();
    const create = empty.getByRole(tab === 'Studies' ? 'button' : 'link', {
      name: action,
      exact: true,
    });
    await expect(create).toBeVisible();
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
    if (tab === 'Coresearches') {
      const filter = panel.getByRole('button', { name: 'Filter', exact: true });
      expect(await filter.boundingBox()).toEqual(studyDisplay);
      const display = panel.getByRole('button', { name: 'Display', exact: true });
      await expect(display).toBeVisible();
      expect(await display.boundingBox()).toEqual(studyLayout);
      await display.click();
      await expect(panel.getByRole('menuitemcheckbox')).toHaveText(['experiment', 'cowrite']);
      await page.keyboard.press('Escape');
      await filter.click();
      const active = panel.getByRole('menuitemradio', { name: 'Active', exact: true });
      const archived = panel.getByRole('menuitemradio', { name: 'Archived', exact: true });
      const check = await active.locator('svg').boundingBox();
      const activeBox = (await active.boundingBox())!;
      expect(check).toMatchObject({ width: 12, height: 12 });
      expect(activeBox.x + activeBox.width - (check!.x + check!.width)).toBe(8);
      await archived.click();
      await expect(empty.getByRole('heading', { name: 'No archived chats' })).toBeVisible();
      await expect(create).toHaveCount(0);
      await filter.click();
      await expect(archived).toHaveAttribute('aria-checked', 'true');
      expect((await archived.locator('svg').boundingBox())!.x).toBe(check!.x);
      await active.click();
    }
    await create.click();
    if (tab === 'Studies') {
      const dialog = page.getByRole('dialog', { name: 'New study', exact: true });
      await expect(dialog.getByRole('button', { name: project.name, exact: true })).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(dialog).toHaveCount(0);
    } else {
      await expect(page).toHaveURL(/#\/coresearch\/new$/);
      await expect(page.getByRole('textbox', { name: 'Message the agent' })).toBeVisible();
    }
  }
});

test('a project with only archived chats still offers a new chat in the active view', async ({
  page,
}) => {
  await page.route('**/api/researches/project/coresearch', (route) =>
    route.fulfill({
      json: [
        {
          id: 'archived',
          title: 'Archived chat',
          mode: 'experiment',
          archived_at: '2026-11-01',
          updated_at: '2026-11-01',
          is_owner: false,
        },
      ],
    }),
  );
  await page.goto('/#/projects/project');
  await page.getByRole('tab', { name: 'Coresearches', exact: true }).click();
  const panel = page.getByRole('tabpanel', { name: 'Coresearches' });
  await expect(panel.getByRole('heading', { name: 'No active chats' })).toBeVisible();
  await expect(panel.getByRole('link', { name: 'New chat' })).toBeVisible();
  await panel.getByRole('button', { name: 'Filter' }).click();
  await panel.getByRole('menuitemradio', { name: 'Archived' }).click();
  await expect(panel.getByRole('button', { name: 'Archived chat' })).toBeVisible();
  await expect(panel.getByRole('link', { name: 'New chat' })).toHaveCount(0);
});

test('Coresearches aligns its controls with Studies and filters chats by mode', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1629, height: 864 });
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
  const studyDisplay = await page
    .getByRole('button', { name: 'Display', exact: true })
    .boundingBox();
  const studyLayout = await page.getByRole('button', { name: /^(Board|List)$/ }).boundingBox();
  await page.getByRole('tab', { name: 'Coresearches', exact: true }).click();
  const panel = page.getByRole('tabpanel', { name: 'Coresearches' });
  const experiment = panel.getByRole('button', { name: 'Experiment chat', exact: true });
  const cowrite = panel.getByRole('button', { name: 'Cowrite chat', exact: true });
  const filter = panel.getByRole('button', { name: 'Filter', exact: true });
  const display = panel.getByRole('button', { name: 'Display', exact: true });
  await expect(experiment).toBeVisible();
  await expect(cowrite).toBeVisible();
  const studies = page.getByRole('tab', { name: 'Studies', exact: true });
  const table = panel.getByRole('table', { name: 'Coresearch chats' });
  const tableBox = (await table.boundingBox())!;
  const tabX = (await studies.boundingBox())!.x;
  const panelBox = (await panel.boundingBox())!;
  expect(tableBox.x).toBe(tabX);
  expect((await experiment.boundingBox())!.x).toBe(tabX);
  expect(tableBox.x + tableBox.width).toBe(panelBox.x + panelBox.width - 16);
  await expect(table.getByRole('columnheader', { name: 'Name' })).toHaveCSS('padding-left', '0px');
  expect(await filter.boundingBox()).toEqual(studyDisplay);
  expect(await display.boundingBox()).toEqual(studyLayout);
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
  const archived = panel.getByRole('button', { name: 'Archived chat', exact: true });
  await expect(archived).toBeVisible();
  await page.setViewportSize({ width: 640, height: 864 });
  await page.getByRole('button', { name: 'Toggle project properties' }).click();
  const narrowTable = (await table.boundingBox())!;
  const narrowPanel = (await panel.boundingBox())!;
  const narrowTabX = (await studies.boundingBox())!.x;
  expect(narrowTable.x).toBe(narrowTabX);
  expect((await archived.boundingBox())!.x).toBe(narrowTabX);
  expect(narrowTable.x + narrowTable.width).toBe(narrowPanel.x + narrowPanel.width - 16);
});
