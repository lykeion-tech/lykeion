import { randomUUID } from 'node:crypto';
import { expect, test } from './test';
import { labMember, labWorkspace } from './lab';

test('links both Coresearch modes and gives a project peer read-only access', async ({
  page,
  browser,
}) => {
  const workspace = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': workspace };
  const projectResponse = await page.request.post('/api/researches', {
    headers,
    data: { name: 'Shared conversations test' },
  });
  expect(projectResponse.status()).toBe(201);
  const project = await projectResponse.json();
  const path = `/#/projects/${project.id}`;

  for (const mode of ['experiment', 'cowrite'] as const) {
    const body = `Project sharing example for ${mode}`;
    const accepted = await page.request.post('/api/coresearch/conversations', {
      headers,
      data: {
        request_id: randomUUID(), mode, body, harness: 'claude',
        ...(mode === 'cowrite' ? { template: 'blank' } : {}),
      },
    });
    expect(accepted.status(), await accepted.text()).toBe(201);
    const { conversation } = await accepted.json();
    await page.goto(
      mode === 'cowrite'
        ? `/#/coresearch/${conversation.project_id}/${conversation.chat_id}`
        : `/#/coresearch/conversations/${conversation.id}`,
    );
    if (mode === 'cowrite') await page.getByRole('tab', { name: 'Chats', exact: true }).click();
    await page.getByRole('button', { name: `Actions for ${conversation.title}`, exact: true }).click();
    await page.getByRole('menuitem', { name: 'Add to project', exact: true }).click();
    await page.getByRole('button', { name: project.name, exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Add to project', exact: true })).toBeHidden();
  }

  await page.goto(path);
  await page.getByText('Coresearches', { exact: true }).click();
  const experiment = 'Project sharing example for experiment';
  const cowrite = 'Project sharing example for cowrite';
  await expect(page.getByText(experiment, { exact: true })).toBeVisible();
  await expect(page.getByText(cowrite, { exact: true })).toBeVisible();
  const table = page.getByRole('table', { name: 'Coresearch chats' });
  const nameHeader = table.getByRole('columnheader', { name: 'Name', exact: true });
  await expect(nameHeader).toHaveCSS('border-bottom-width', '0px');
  const nameCell = table.getByRole('row').nth(1).getByRole('cell').first();
  const nameInset = await nameCell.evaluate((element) => getComputedStyle(element).paddingLeft);
  await expect(nameHeader).toHaveCSS('padding-left', nameInset);
  const actions = (title: string) =>
    page.getByRole('button', { name: `Actions for ${title}`, exact: true });
  await actions(experiment).click();
  await expect(page.getByRole('menu', { name: 'Chat actions' })).toHaveCSS('width', '144px');
  await expect(page.getByRole('menuitem', { name: 'Archive', exact: true })).toHaveCSS(
    'min-height',
    '28px',
  );
  await page.getByRole('menuitem', { name: 'Pin', exact: true }).click();
  // The trigger ignores clicks until the change has settled.
  await expect(actions(experiment)).toHaveAttribute('aria-busy', 'false');
  await actions(experiment).click();
  await expect(page.getByRole('menuitem', { name: 'Unpin', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await actions(cowrite).click();
  await page.getByRole('menuitem', { name: 'Archive', exact: true }).click();
  await expect(page.getByText(cowrite, { exact: true })).toBeHidden();
  const filter = page.getByRole('button', { name: 'Filter', exact: true });
  const tabs = page.getByRole('tablist', { name: 'Project views' });
  const filterBox = (await filter.boundingBox())!;
  const tabsBox = (await tabs.boundingBox())!;
  expect(filterBox.x).toBeGreaterThan(tabsBox.x + tabsBox.width);
  expect(filterBox.y).toBeGreaterThan(tabsBox.y + tabsBox.height);
  await filter.press('ArrowDown');
  await expect(page.getByRole('menuitemradio', { name: 'Active' })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(filter).toBeFocused();
  await expect(table.getByRole('link', { name: cowrite, exact: true })).toBeVisible();

  await page.goto('/#/coresearch/new');
  const rail = page.getByRole('complementary', { name: 'Chat', exact: true });
  const archived = rail.getByRole('button', { name: 'Archived', exact: true });
  const archivedChat = rail.getByRole('link', { name: new RegExp(cowrite) });
  await expect(archived).toHaveAttribute('aria-expanded', 'false');
  await expect(archivedChat).toHaveCount(0);
  await archived.click();
  await expect(archivedChat).toBeVisible();
  await rail.getByRole('button', { name: `Actions for ${cowrite}`, exact: true }).press('ArrowDown');
  await page.getByRole('menuitem', { name: 'Unarchive', exact: true }).click();
  await expect(page.getByRole('menuitem', { name: 'Unarchive', exact: true })).toHaveCount(0);
  await archived.click();
  await expect(archivedChat).toBeVisible();
  await page.goto(path);
  await page.getByRole('tab', { name: 'Coresearches', exact: true }).click();
  await expect(table.getByRole('link', { name: cowrite, exact: true })).toBeVisible();

  const peer = await labMember(browser, page.request, workspace);
  try {
    await peer.page.goto(path);
    await peer.page.getByText('Coresearches', { exact: true }).click();
    await expect(peer.page.getByText(cowrite, { exact: true })).toBeVisible();
    await expect(peer.page.getByRole('button', { name: `Actions for ${cowrite}`, exact: true })).toHaveCount(0);
    await peer.page.getByText(cowrite, { exact: true }).click();
    await expect(peer.page.getByText(cowrite, { exact: true }).last()).toBeVisible();
    await expect(peer.page.getByRole('textbox', { name: 'Message the agent' })).toHaveCount(0);
  } finally {
    await peer.context.close();
  }
});
