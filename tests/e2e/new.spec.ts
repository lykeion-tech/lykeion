import { expect, test } from '@playwright/test';
import { LAB, labTeam, labWorkspace } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-14-artifacts';

async function cardsNamed(page: import('@playwright/test').Page, question: string) {
  const res = await page.request.get('/api/cards', {
    headers: { 'X-Workspace-ID': await labWorkspace(page.request) },
  });
  return (
    (await res.json()) as {
      id: string;
      question: string;
      title: string;
      deadline: string | null;
      follows_from: string[];
    }[]
  ).filter((c) => c.question === question);
}

test.describe('new study', () => {
  test('creating a study, then a follow-up the duplicate check suggests', async ({ page }) => {
    const question = `Do dedifferentiated beta cells re-express ALDH1A3 in T2D? ${Date.now()}`;
    await page.goto('/#/studies');
    await page.getByRole('button', { name: 'New', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'New study' });
    await expect(dialog.getByText('Beta-cell lab')).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Backlog' })).toBeDisabled();
    await dialog
      .getByRole('textbox', { name: 'Study title' })
      .fill('Beta-cell ALDH1A3 in type 2 diabetes');
    await dialog.getByRole('textbox', { name: 'Research question' }).fill(question);
    await dialog.getByRole('button', { name: 'No deadline' }).click();
    const chosen = page.locator('.composer-calendar [aria-current="date"]');
    const day = await chosen.getAttribute('data-date');
    await chosen.click();
    await page.screenshot({ path: `${artifacts}/new-study.png`, fullPage: true });
    await dialog.getByRole('textbox', { name: 'Research question' }).press('ControlOrMeta+Enter');
    await expect(dialog).toHaveCount(0);
    let found = await cardsNamed(page, question);
    expect(found).toHaveLength(1);
    expect(found[0]!.title).toBe('Beta-cell ALDH1A3 in type 2 diabetes');
    const endOfDay = await page.evaluate(
      (value) => new Date(`${value}T23:59:59`).toISOString(),
      day,
    );
    expect(Date.parse(found[0]!.deadline!)).toBe(Date.parse(endOfDay));

    await page.getByRole('button', { name: 'New', exact: true }).click();
    await dialog.getByRole('textbox', { name: 'Research question' }).fill(question);
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog.getByRole('status')).toContainText('An active study already asks this');
    await expect(dialog.getByRole('textbox', { name: 'Research question' })).toHaveValue(question);
    await page.screenshot({ path: `${artifacts}/duplicate-warning.png`, fullPage: true });
    await dialog.getByRole('button', { name: /^Follows from BETA-/ }).click();
    await expect(dialog.getByRole('button', { name: /Follows from: BETA-/ })).toBeVisible();
    await page.screenshot({ path: `${artifacts}/follow-up.png`, fullPage: true });
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    found = await cardsNamed(page, question);
    expect(found).toHaveLength(2);
    expect(found.find((c) => c.follows_from.length)?.follows_from).toEqual([
      found.find((c) => !c.follows_from.length)!.id,
    ]);
  });

  test('"Create another" keeps the modal open and empties it', async ({ page }) => {
    await page.goto('/#/studies');
    await page.keyboard.press('c');
    const dialog = page.getByRole('dialog', { name: 'New study' });
    await dialog.getByRole('switch', { name: 'Create another' }).check();
    const question = `Lithium and telomere length ${Date.now()}?`;
    await dialog.getByRole('textbox', { name: 'Research question' }).fill(question);
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog.getByRole('textbox', { name: 'Research question' })).toHaveValue('');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('textbox', { name: 'Study title' })).toHaveValue('');
    const found = await cardsNamed(page, question);
    expect(found).toHaveLength(1);
    expect(found[0]!.title).toBe(question);
    await page.screenshot({ path: `${artifacts}/create-another.png`, fullPage: true });
  });

  // The Backlog menu supplies the visible edit action. Exercise the exported
  // composer interface directly while that menu is unavailable.
});

test('editing a held Backlog study persists fields and a later refusal retains the draft', async ({
  page,
}) => {
  const created = await page.request.post('/api/workspaces', {
    data: { name: 'Modal edit lab', slug: `modal-edit-${Date.now()}` },
  });
  expect(created.status()).toBe(201);
  const ws = (await created.json()) as { id: string };
  const headers = { 'X-Workspace-ID': ws.id };
  expect(
    (
      await page.request.patch(`/api/workspaces/${ws.id}/settings`, {
        data: { settings: { wip_limit: 1 } },
      })
    ).status(),
  ).toBe(204);
  const parentResponse = await page.request.post('/api/cards', {
    headers,
    data: {
      question: 'An active parent for the edit fixture?',
      assignee: { type: 'team', teams: { grounding: await labTeam(page.request, ws.id) } },
    },
  });
  expect(parentResponse.status()).toBe(201);
  const parent = (await parentResponse.json()) as { id: string };
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`/api/cards/${parent.id}`, { headers })).json())
          .board_column,
    )
    .not.toBe('backlog');
  const response = await page.request.post('/api/cards', {
    headers,
    data: {
      title: 'Original edit title',
      question: 'Original edit question?',
      deadline: '2026-11-12T23:59:59+01:00',
      follows_from: [parent.id],
    },
  });
  expect(response.status()).toBe(201);
  const card = (await response.json()) as { id: string; board_column: string };
  expect(card.board_column).toBe('backlog');
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), ws.id);
  await page.goto('/#/studies');
  await expect(page.locator(`[data-card="${card.id}"]`)).toBeVisible();
  const openEdit = async () =>
    page.evaluate(async (id) => {
      const path = '/src/state/task-composer.ts';
      const { useTaskComposer } = await import(path);
      useTaskComposer().openComposer(id);
    }, card.id);
  await openEdit();
  const dialog = page.getByRole('dialog', { name: 'Edit study' });
  await expect(dialog.getByRole('textbox', { name: 'Study title' })).toHaveValue(
    'Original edit title',
  );
  await expect(dialog.getByRole('textbox', { name: 'Research question' })).toHaveValue(
    'Original edit question?',
  );
  await expect(dialog.getByRole('button', { name: '2026-11-12', exact: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: /Follows from: / })).toHaveCount(1);
  await expect(dialog.getByRole('switch', { name: 'Create another' })).toHaveCount(0);
  await dialog.getByRole('textbox', { name: 'Study title' }).fill('Edited study title');
  await dialog
    .getByRole('textbox', { name: 'Research question' })
    .fill('Edited research question?');
  await dialog.getByRole('button', { name: '2026-11-12', exact: true }).click();
  await dialog.getByRole('button', { name: 'No due date', exact: true }).click();
  await dialog.getByRole('button', { name: /Follows from: / }).click();
  await page.screenshot({ path: `${artifacts}/edit-study.png`, fullPage: true });
  await dialog.getByRole('button', { name: /Save study/ }).click();
  await expect(dialog).toHaveCount(0);
  const saved = await (await page.request.get(`/api/cards/${card.id}`, { headers })).json();
  expect(saved).toMatchObject({
    title: 'Edited study title',
    question: 'Edited research question?',
    deadline: null,
    follows_from: [],
  });
  await openEdit();
  await expect(dialog.getByRole('textbox', { name: 'Study title' })).toHaveValue(
    'Edited study title',
  );
  await dialog.getByRole('textbox', { name: 'Study title' }).fill('Retain this draft');
  expect((await page.request.post(`${LAB}/lab/hypotheses/${card.id}`)).status()).toBe(204);
  await dialog.getByRole('button', { name: /Save study/ }).click();
  await expect(dialog.getByRole('alert')).toContainText('Backlog');
  await expect(dialog.getByRole('textbox', { name: 'Study title' })).toHaveValue(
    'Retain this draft',
  );
  await expect(dialog.getByRole('textbox', { name: 'Research question' })).toHaveValue(
    'Edited research question?',
  );
  await page.screenshot({ path: `${artifacts}/edit-refused.png`, fullPage: true });
  expect((await (await page.request.get(`/api/cards/${card.id}`, { headers })).json()).title).toBe(
    'Edited study title',
  );
});

