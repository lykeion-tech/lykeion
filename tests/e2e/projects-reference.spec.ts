import { expect, test } from '@playwright/test';
import { labWorkspace } from './lab';

test('Projects has the reference table and scopes its board and saved Properties to one project', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  const ws = await labWorkspace(page.request),
    headers = { 'X-Workspace-ID': ws };
  const created = await page.request.post('/api/researches', {
    headers,
    data: {
      name: 'Reference project board',
      description: 'A recorded project description.',
      priority: 'high',
    },
  });
  expect(created.status()).toBe(201);
  const project = await created.json();
  const card = await page.request.post('/api/cards', {
    headers,
    data: {
      title: 'Linked research study',
      question: 'Which sources establish this project board fixture?',
      research_id: project.id,
      predicates: [{ name: 'effect', text: 'effect > 0' }],
    },
  });
  expect(card.status()).toBe(201);
  const unrelated = await page.request.post('/api/cards', {
    headers,
    data: {
      title: 'Outside this project',
      question: 'An unrelated question outside the project.',
      predicates: [{ name: 'effect', text: 'effect > 0' }],
    },
  });
  expect(unrelated.status()).toBe(201);
  await page.goto('/#/projects');
  await expect(page.locator('.page-header')).toHaveCSS('height', '58px');
  await expect(page.locator('.page-header h1')).toHaveCSS('font-size', '20px');
  await expect(page.locator('#page-header-count')).toBeEmpty();
  const table = page.getByRole('table', { name: 'Projects', exact: true });
  await expect(table.getByRole('columnheader', { name: 'Name', exact: true })).toHaveCSS(
    'font-size',
    '12px',
  );
  await expect(table.locator('th').first()).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
  await expect(table.getByRole('columnheader')).toHaveText([
    'Name',
    'Priority',
    'Status',
    'Progress',
    'Lead',
    'Created',
  ]);
  const row = table.getByRole('row').filter({ hasText: project.name });
  await expect(row).toContainText('High');
  await expect(row).toContainText('Planned');
  await expect(row).toContainText('0/1');
  expect((await row.boundingBox())!.height).toBe(44);
  await page.screenshot({ path: 'test-results/reference/projects-list.png' });
  await row.getByRole('link', { name: project.name, exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'Project breadcrumb' })).toContainText(
    project.name,
  );
  await expect(page.locator('.page-header')).toHaveCount(0);
  await expect(page.locator('.project-heading')).toHaveCSS('height', '56px');
  await expect(page.locator('.project-heading')).toHaveCSS('border-bottom-width', '0px');
  await expect(page.getByRole('link', { name: 'Back to projects', exact: true })).toBeVisible();
  const rail = page.getByRole('complementary', { name: 'Project properties' });
  await expect(rail).toBeVisible();
  await expect(page.getByRole('button', { name: /Linked research study/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /Outside this project/ })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Members', exact: true })).toBeVisible();
  await expect(rail.getByRole('textbox', { name: 'Project description' })).toHaveValue(
    project.description,
  );
  await rail.getByRole('combobox', { name: 'Project status' }).selectOption('in_progress');
  await expect(rail.getByRole('combobox', { name: 'Project status' })).toBeEnabled();
  await rail.getByRole('combobox', { name: 'Project priority' }).selectOption('urgent');
  await expect(rail.getByRole('combobox', { name: 'Project priority' })).toBeEnabled();
  const me = await (await page.request.get('/api/me')).json();
  await rail.getByRole('combobox', { name: 'Project lead' }).selectOption(`member:${me.id}`);
  await expect(rail.getByRole('combobox', { name: 'Project lead' })).toBeEnabled();
  await rail
    .getByRole('textbox', { name: 'Project description' })
    .fill('Updated from the Properties rail.');
  await rail.getByRole('heading', { name: 'Description', exact: true }).click();
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/researches/${project.id}`, { headers })).json())
          .description,
    )
    .toBe('Updated from the Properties rail.');
  await page.reload();
  await expect(rail.getByRole('combobox', { name: 'Project status' })).toHaveValue('in_progress');
  await expect(rail.getByRole('combobox', { name: 'Project priority' })).toHaveValue('urgent');
  await expect(rail.getByRole('combobox', { name: 'Project lead' })).toHaveValue(`member:${me.id}`);
  await expect(rail.getByRole('heading', { name: 'Resources' })).toHaveCount(0);
  await page.screenshot({ path: 'test-results/reference/project-board.png' });
  await page.getByRole('button', { name: 'Toggle project properties' }).click();
  await expect(rail).toHaveCount(0);
  await page.getByRole('button', { name: 'Toggle project properties' }).click();
  await expect(rail).toBeVisible();
});

test('an empty project creates a study already linked to its board', async ({ page }) => {
  const ws = await labWorkspace(page.request),
    headers = { 'X-Workspace-ID': ws };
  const created = await page.request.post('/api/researches', {
    headers,
    data: { name: 'Empty project creation' },
  });
  expect(created.status()).toBe(201);
  const project = await created.json();
  await page.goto(`/#/projects/${project.id}`);
  await expect(page.getByRole('heading', { name: 'No studies linked', exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/reference/project-empty.png' });
  await page.getByRole('button', { name: 'New study', exact: true }).click();
  const modal = page.getByRole('dialog', { name: 'New study', exact: true });
  await modal.getByPlaceholder('Study title').fill('Created inside project');
  await modal
    .getByPlaceholder('Describe what you want to do, in your own words')
    .fill('How can this project create a durable linked study?');
  await modal.getByRole('button', { name: /^Create study/ }).click();
  await expect(modal).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Created inside project/ })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', { name: /Created inside project/ })).toBeVisible();
  const detail = await (
    await page.request.get(`/api/researches/${project.id}`, { headers })
  ).json();
  expect(detail.studies).toHaveLength(1);
  expect(detail.studies[0].research_id).toBe(project.id);
});

test('a remote project description update keeps a local draft until the person resolves it', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request),
    headers = { 'X-Workspace-ID': ws };
  const created = await page.request.post('/api/researches', {
    headers,
    data: { name: 'Concurrent description', description: 'Original saved text' },
  });
  expect(created.status()).toBe(201);
  const project = await created.json();
  await page.goto(`/#/projects/${project.id}`);
  const description = page
    .getByRole('complementary', { name: 'Project properties' })
    .getByRole('textbox', { name: 'Project description' });
  await description.fill('My unsaved draft');
  expect(
    (
      await page.request.patch(`/api/researches/${project.id}`, {
        headers,
        data: { description: 'A newer saved description' },
      })
    ).status(),
  ).toBe(200);
  await expect(
    page.getByText('Description changed elsewhere. Your draft has been kept.'),
  ).toBeVisible();
  await expect(description).toHaveValue('My unsaved draft');
  await page.getByRole('button', { name: 'Use saved description', exact: true }).click();
  await expect(description).toHaveValue('A newer saved description');
  await expect(page.getByRole('button', { name: 'Save my description', exact: true })).toHaveCount(
    0,
  );
});

test('Project properties shares study pane proportions and saves pointer and keyboard resizing', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1512, height: 982 });
  const ws = await labWorkspace(page.request);
  const response = await page.request.post('/api/researches', {
    headers: { 'X-Workspace-ID': ws },
    data: { name: 'Resizable project properties' },
  });
  expect(response.status()).toBe(201);
  const project = await response.json();
  await page.goto(`/#/projects/${project.id}`);
  const rail = page.getByRole('complementary', { name: 'Project properties' });
  const splitter = page.getByRole('separator', { name: 'Resize project properties' });
  const main = (await page.locator('.project-main').boundingBox())!;
  const emptyCenter = await page.locator('.project-empty').evaluate((el) => {
    const children = Array.from(el.children).map((child) => child.getBoundingClientRect());
    return (children[0]!.top + children[children.length - 1]!.bottom) / 2;
  });
  expect(Math.abs(emptyCenter - (main.y + main.height / 2))).toBeLessThan(1);
  await expect(rail).toHaveCSS('width', '364px');
  await expect(rail).toHaveCSS('padding-left', '12px');
  await expect(splitter).toHaveAttribute('aria-valuemin', '240');
  await expect(splitter).toHaveAttribute('aria-valuemax', '480');
  await splitter.focus();
  await page.keyboard.press('Shift+ArrowLeft');
  await expect(rail).toHaveCSS('width', '404px');
  const bounds = (await splitter.boundingBox())!;
  await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + 100);
  await page.mouse.down();
  await page.mouse.move(bounds.x + bounds.width / 2 - 36, bounds.y + 100, { steps: 4 });
  await page.mouse.up();
  await expect(rail).toHaveCSS('width', '440px');
  await page.reload();
  await expect(rail).toHaveCSS('width', '440px');
  const toggle = page.getByRole('button', { name: 'Toggle project properties' });
  await toggle.click();
  await expect(splitter).toHaveCount(0);
  await toggle.click();
  await expect(rail).toHaveCSS('width', '440px');
  await splitter.focus();
  await page.keyboard.press('Home');
  await expect(rail).toHaveCSS('width', '240px');
  await page.keyboard.press('End');
  await expect(rail).toHaveCSS('width', '480px');
});
