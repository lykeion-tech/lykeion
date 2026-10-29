import { expect, test } from '@playwright/test';
import { labPack, labTeam, labWorkspace, preregister } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-14-artifacts';
const shots = 'test-results/teams-scoring';

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
    await dialog
      .getByRole('textbox', { name: 'Study title' })
      .fill('Beta-cell ALDH1A3 in type 2 diabetes');
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await preregister(dialog);
    await dialog.getByRole('button', { name: 'No deadline' }).click();
    const chosen = page.locator('.composer-calendar [aria-current="date"]');
    const day = await chosen.getAttribute('data-date');
    await chosen.click();
    await page.screenshot({ path: `${artifacts}/new-study.png`, fullPage: true });
    await dialog.getByRole('textbox', { name: 'Describe your study' }).press('ControlOrMeta+Enter');
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
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await preregister(dialog);
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog.getByRole('status')).toContainText('An active study already asks this');
    await expect(dialog.getByRole('textbox', { name: 'Describe your study' })).toHaveValue(
      question,
    );
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
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await preregister(dialog);
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog.getByRole('textbox', { name: 'Describe your study' })).toHaveValue('');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('textbox', { name: 'Study title' })).toHaveValue('');
    const found = await cardsNamed(page, question);
    expect(found).toHaveLength(1);
    expect(found[0]!.title).toBe(question);
    await page.screenshot({ path: `${artifacts}/create-another.png`, fullPage: true });
  });

  // Adds a demo Team built for a stage to the lab person and returns its id.
  async function stageTeam(page: import('@playwright/test').Page, name: string, stage: string) {
    const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
    const pack = await labPack(page.request, name, headers['X-Workspace-ID'], { stage });
    expect((await page.request.post(`/api/teams/${pack.id}/add`, { headers })).status()).toBe(200);
    return pack.id;
  }
  async function studyOf(page: import('@playwright/test').Page, question: string) {
    const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
    const list = (await (await page.request.get('/api/cards', { headers })).json()) as {
      id: string;
      question: string;
    }[];
    const id = list.find((c) => c.question === question)?.id;
    expect(id).toBeTruthy();
    return (await (await page.request.get(`/api/cards/${id}`, { headers })).json()) as {
      scope_from: string;
      scope_to: string;
      assignee_type: string | null;
      assignee_id: string | null;
      teams: Record<string, { pack_id: string }>;
    };
  }
  async function composer(page: import('@playwright/test').Page) {
    await page.goto('/#/studies');
    await page.getByRole('button', { name: 'New', exact: true }).click();
    return page.getByRole('dialog', { name: 'New study' });
  }

  test('assigning one Team makes a single-stage study', async ({ page }) => {
    const hyp = await stageTeam(page, 'ui-hyp', 'hypotheses');
    const question = `One Team, one stage ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await dialog.getByRole('button', { name: 'Assignee: Built-in Teams', exact: true }).click();
    await dialog.getByRole('menuitem', { name: /^ui-hyp Built for/ }).click();
    await expect(
      dialog.getByText('ui-hyp will start working right after you create the study'),
    ).toBeVisible();
    const stages = dialog.getByRole('button', { name: 'Stages: Hypotheses' });
    await expect(stages).toBeDisabled();
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    const study = await studyOf(page, question);
    expect([study.scope_from, study.scope_to]).toEqual(['hypotheses', 'hypotheses']);
    expect(study.assignee_type).toBe('team');
    expect(study.teams.hypotheses!.pack_id).toBe(hyp);
  });

  test('a Team set prefills from the last end-to-end study', async ({ page }) => {
    const ground = await labTeam(page.request, await labWorkspace(page.request));
    const hyp = await stageTeam(page, 'ui-hyp', 'hypotheses');
    const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
    const earlier = await page.request.post('/api/cards', {
      headers,
      data: {
        question: `The earlier set study ${Date.now()}`,
        assignee: { type: 'team', teams: { grounding: ground, hypotheses: hyp } },
        scope: { from: 'grounding', to: 'reporting' },
        predicates: [{ name: 'effect', text: 'effect > 0' }],
      },
    });
    expect(earlier.status()).toBe(201);
    const question = `The set again ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await preregister(dialog);
    await dialog.getByRole('button', { name: 'Assignee: Built-in Teams', exact: true }).click();
    await dialog.getByRole('menuitem', { name: /Your last set/ }).click();
    await expect(
      dialog.getByText('Your Team set will start working right after you create the study'),
    ).toBeVisible();
    await expect(
      dialog.getByRole('button', { name: 'Stages: Grounding → Reporting' }),
    ).toBeDisabled();
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    const study = await studyOf(page, question);
    expect([study.scope_from, study.scope_to]).toEqual(['grounding', 'reporting']);
    expect(study.assignee_id).toBeNull();
    expect(study.teams.grounding!.pack_id).toBe(ground);
    expect(study.teams.hypotheses!.pack_id).toBe(hyp);
  });

  test('built-in Teams with a Grounding → Hypotheses range', async ({ page }) => {
    const question = `Built-in Teams, two stages ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await dialog.getByRole('button', { name: 'Stages: Grounding → Reporting' }).click();
    await page.screenshot({ path: `${shots}/stages-pill.png` });
    await dialog.getByRole('combobox', { name: 'To stage' }).selectOption('hypotheses');
    await expect(
      dialog.getByRole('button', { name: 'Stages: Grounding → Hypotheses' }),
    ).toBeVisible();
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    const study = (await studyOf(page, question)) as Awaited<ReturnType<typeof studyOf>> & {
      teams: Record<string, { name: string }>;
    };
    expect([study.scope_from, study.scope_to]).toEqual(['grounding', 'hypotheses']);
    expect(study.assignee_id).toBeNull();
    expect(Object.keys(study.teams).sort()).toEqual(['grounding', 'hypotheses']);
    expect(study.teams.grounding!.name).toBe('Grounding Team');
    expect(study.teams.hypotheses!.name).toBe('Hypotheses Team');
  });

  test('a Team without a stage label asks for its column', async ({ page }) => {
    const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
    const id = await stageTeam(page, 'ui-nostage', '');
    const question = `A Team with no label ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await dialog.getByRole('button', { name: 'Assignee: Built-in Teams', exact: true }).click();
    const item = dialog.getByRole('menuitem', { name: /^ui-nostage/ });
    await expect(item).not.toContainText('Built for');
    await item.click();
    await expect(dialog.getByRole('button', { name: /Create study/ })).toBeDisabled();
    await dialog.getByRole('button', { name: 'Stages: choose a stage' }).click();
    await dialog.getByRole('combobox', { name: 'Stage' }).selectOption('validation');
    await expect(dialog.getByRole('button', { name: 'Stages: Validation' })).toBeDisabled();
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    const study = await studyOf(page, question);
    expect([study.scope_from, study.scope_to]).toEqual(['validation', 'validation']);
    expect(study.teams.validation!.pack_id).toBe(id);
    void headers;
  });

  test('a last set with a Team no longer added says so', async ({ page }) => {
    const ws = await labWorkspace(page.request);
    const headers = { 'X-Workspace-ID': ws };
    const ground = await labTeam(page.request, ws);
    const hyp = await stageTeam(page, 'ui-hyp', 'hypotheses');
    const made = await page.request.post('/api/cards', {
      headers,
      data: {
        question: `A set study ${Date.now()}`,
        assignee: { type: 'team', teams: { grounding: ground, hypotheses: hyp } },
        scope: { from: 'grounding', to: 'reporting' },
        predicates: [{ name: 'effect', text: 'effect > 0' }],
      },
    });
    expect(made.status()).toBe(201);
    const dialog = await composer(page);
    await dialog
      .getByRole('textbox', { name: 'Describe your study' })
      .fill(`Refused set ${Date.now()}`);
    await preregister(dialog);
    await dialog.getByRole('button', { name: 'Assignee: Built-in Teams', exact: true }).click();
    await dialog.getByRole('menuitem', { name: /Your last set/ }).click();
    await page.route('**/api/cards', (route) =>
      route.request().method() === 'POST'
        ? route.fulfill({
            status: 400,
            contentType: 'application/json',
            body: JSON.stringify({ error: 'add this Team to your workspace first' }),
          })
        : route.fallback(),
    );
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog.getByRole('alert')).toHaveText(
      "A Team in your last set isn't in your workspace any more",
    );
  });

  test('with no Team picked the built-in Teams start the study', async ({ page }) => {
    const dialog = await composer(page);
    await expect(
      dialog.getByText('Built-in Teams will start working right after you create the study'),
    ).toBeVisible();
    await expect(dialog.getByRole('textbox', { name: 'Describe your study' })).toHaveAttribute(
      'placeholder',
      'Describe what you want to do, in your own words',
    );
    await expect(
      dialog.getByRole('button', { name: 'Stages: Grounding → Reporting' }),
    ).toBeEnabled();
  });

  test('the picker lists Teams and Team sets', async ({ page }) => {
    await labTeam(page.request, await labWorkspace(page.request));
    await stageTeam(page, 'ui-hyp', 'hypotheses');
    const dialog = await composer(page);
    await dialog.getByRole('button', { name: 'Assignee: Built-in Teams', exact: true }).click();
    const menu = dialog.getByRole('menu');
    await expect(menu.getByText('Teams', { exact: true })).toBeVisible();
    await expect(
      menu.getByRole('menuitem', { name: /^ui-demo Built for Grounding/ }),
    ).toBeVisible();
    await expect(
      menu.getByRole('menuitem', { name: /^ui-hyp Built for Hypotheses/ }),
    ).toBeVisible();
    await expect(menu.getByText('Team sets', { exact: true })).toBeVisible();
    await menu.getByText('Team sets', { exact: true }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${shots}/assign-picker.png` });
    await menu.getByRole('menuitem', { name: 'New set…' }).click();
    await dialog
      .getByRole('combobox', { name: 'Grounding Team' })
      .selectOption({ label: 'ui-demo' });
    await dialog
      .getByRole('combobox', { name: 'Hypotheses Team' })
      .selectOption({ label: 'ui-hyp' });
    await expect(dialog.getByRole('combobox', { name: 'Agent for the other stages' })).toHaveCount(
      0,
    );
    await expect(dialog.getByRole('button', { name: 'Use this set' })).toBeEnabled();
    await page.screenshot({ path: `${shots}/team-set-editor.png` });
  });

  test('a New set with one Team runs end to end, built-in Teams on the rest', async ({ page }) => {
    const hyp = await stageTeam(page, 'ui-hyp', 'hypotheses');
    const question = `A set of one ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await preregister(dialog);
    await dialog.getByRole('button', { name: 'Assignee: Built-in Teams', exact: true }).click();
    await dialog.getByRole('menuitem', { name: 'New set…' }).click();
    await dialog
      .getByRole('combobox', { name: 'Hypotheses Team' })
      .selectOption({ label: 'ui-hyp' });
    await dialog.getByRole('button', { name: 'Use this set' }).click();
    await expect(dialog.getByRole('button', { name: 'Assignee: Your Team set' })).toBeVisible();
    const saved = page.waitForResponse(
      (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
    );
    await dialog.getByRole('button', { name: /Create study/ }).click();
    const response = await saved;
    expect(response.status(), await response.text()).toBe(201);
    expect(response.request().postDataJSON().scope).toEqual({ from: 'grounding', to: 'reporting' });
    const study = await studyOf(page, question);
    expect([study.scope_from, study.scope_to]).toEqual(['grounding', 'reporting']);
    expect(Object.keys(study.teams).sort()).toEqual([
      'experimentation',
      'grounding',
      'hypotheses',
      'reporting',
      'validation',
    ]);
    expect(study.teams.hypotheses!.pack_id).toBe(hyp);
  });
});
