import { expect, test } from '@playwright/test';
import { chooseCompact, labPack, labTeam, labWorkspace } from './lab';

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
    }[]
  ).filter((c) => c.question === question);
}

test.describe('new study', () => {
  test('Team set choices use a compact keyboard-accessible list', async ({ page }) => {
    await labTeam(page.request, await labWorkspace(page.request));
    await page.goto('/#/studies');
    await page.getByRole('button', { name: 'New', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'New study' });
    await dialog.getByRole('button', { name: 'Assignees' }).click();
    await dialog.getByRole('menuitem', { name: 'New set…' }).click();
    const control = dialog.getByRole('combobox', { name: 'Grounding Team' });
    await control.click();
    const list = dialog.getByRole('listbox', { name: 'Grounding Team' });
    await expect(list.getByRole('option', { name: 'Built-in Team' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    expect((await list.boundingBox())!.width).toBeLessThanOrEqual(240);
    await list.getByRole('option', { name: 'ui-demo', exact: true }).click();
    await expect(control).toContainText('ui-demo');
    await control.click();
    await page.keyboard.press('Escape');
    await expect(list).toHaveCount(0);
    await expect(control).toBeFocused();
  });

  test('picker menus use the calendar background in both themes', async ({ page }) => {
    for (const palette of ['ash', 'midnight']) {
      await page.goto('/#/studies');
      await page.evaluate(
        (theme) => localStorage.setItem('lykeion.preferences', JSON.stringify({ theme })),
        palette,
      );
      await page.reload();
      await page.getByRole('button', { name: 'New', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: 'New study' });
      await dialog.getByRole('button', { name: 'No deadline' }).click();
      const calendar = dialog.getByRole('dialog', { name: 'Choose due date' });
      const color = await calendar.evaluate((element) => getComputedStyle(element).backgroundColor);
      await dialog.getByRole('button', { name: 'No deadline' }).click();
      await dialog.getByRole('button', { name: 'Machines', exact: true }).click();
      const machines = dialog.getByRole('menu', { name: 'Choose machine' });
      expect(await machines.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe(
        color,
      );
      await dialog.getByRole('button', { name: 'Machines', exact: true }).click();
      await dialog.getByRole('button', { name: 'Assignees' }).click();
      const assignees = dialog.getByRole('menu');
      expect(await assignees.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe(
        color,
      );
    }
  });

  test('machine picker opens a compact menu and closes with Escape', async ({ page }) => {
    await page.goto('/#/studies');
    await page.getByRole('button', { name: 'New', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'New study' });
    const trigger = dialog.getByRole('button', { name: 'Machines', exact: true });
    await expect(trigger.locator('svg')).toHaveCount(2);
    await trigger.click();
    const menu = dialog.getByRole('menu', { name: 'Choose machine' });
    await expect(menu.getByRole('menuitemradio', { name: 'My machines' })).toBeChecked();
    const bounds = await menu.boundingBox();
    expect(bounds?.width).toBeLessThanOrEqual(280);
    expect(bounds?.x).toBeGreaterThanOrEqual(0);
    await page.keyboard.press('Escape');
    await expect(menu).toHaveCount(0);
    await expect(trigger).toBeFocused();
  });

  test('creating a study, then the duplicate check refuses a second', async ({ page }) => {
    const question = `Do dedifferentiated beta cells re-express ALDH1A3 in T2D? ${Date.now()}`;
    await page.goto('/#/studies');
    await page.getByRole('button', { name: 'New', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'New study' });
    await expect(dialog.getByText('Beta-cell lab')).toBeVisible();
    await dialog
      .getByRole('textbox', { name: 'Study title' })
      .fill('Beta-cell ALDH1A3 in type 2 diabetes');
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);

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

    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog.getByRole('status')).toContainText('An active study already asks this');
    await expect(dialog.getByRole('textbox', { name: 'Describe your study' })).toHaveValue(
      question,
    );
    await page.screenshot({ path: `${artifacts}/duplicate-warning.png`, fullPage: true });
    await expect(dialog.getByRole('button', { name: /Follows from/ })).toHaveCount(0);
    found = await cardsNamed(page, question);
    expect(found).toHaveLength(1);
  });

  test('switching modes preserves each draft', async ({ page }) => {
    const agent = await soloAgent(page);
    await page.goto('/#/studies');
    await page.keyboard.press('c');
    const dialog = page.getByRole('dialog', { name: 'New study' });
    await dialog.getByRole('textbox', { name: 'Study title' }).fill('Manual title');
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill('Manual question');
    await expect(dialog.getByRole('button', { name: 'Switch to Agent' })).toBeDisabled();
    await dialog.getByRole('button', { name: 'Assignees' }).click();
    await dialog.getByRole('menuitem', { name: agent.name, exact: true }).click();
    await dialog.getByRole('button', { name: 'Switch to Agent' }).click();
    await expect(dialog.getByText('Create with agent')).toBeVisible();
    await expect(dialog.getByLabel(`Created by agent: ${agent.name}`)).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Created by agent' })).toHaveCount(0);
    await dialog
      .getByRole('textbox', { name: 'Describe the study for the agent' })
      .fill('Agent request');
    await dialog.getByRole('button', { name: 'Switch to Manual' }).click();
    await expect(dialog.getByRole('textbox', { name: 'Study title' })).toHaveValue('Manual title');
    await expect(dialog.getByRole('textbox', { name: 'Describe your study' })).toHaveValue(
      'Manual question',
    );
    await dialog.getByRole('button', { name: 'Switch to Agent' }).click();
    await expect(
      dialog.getByRole('textbox', { name: 'Describe the study for the agent' }),
    ).toHaveValue('Agent request');
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
    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
    await dialog.getByRole('menuitem', { name: /^ui-hyp Built for/ }).click();
    await expect(dialog.getByRole('button', { name: 'Assignee: ui-hyp' })).toBeVisible();
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
        scope: { from: 'grounding', to: 'validation' },
      },
    });
    expect(earlier.status()).toBe(201);
    const question = `The set again ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);

    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
    await dialog.getByRole('menuitem', { name: /Your last set/ }).click();
    await expect(dialog.getByRole('button', { name: 'Assignee: Your Team set' })).toBeVisible();
    await expect(
      dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }),
    ).toBeDisabled();
    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    const study = await studyOf(page, question);
    expect([study.scope_from, study.scope_to]).toEqual(['grounding', 'validation']);
    expect(study.assignee_id).toBeNull();
    expect(study.teams.grounding!.pack_id).toBe(ground);
    expect(study.teams.hypotheses!.pack_id).toBe(hyp);
  });

  test('built-in Teams with a Grounding → Hypotheses range', async ({ page }) => {
    const question = `Built-in Teams, two stages ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }).click();
    await page.screenshot({ path: `${shots}/stages-pill.png` });
    await chooseCompact(page, 'To stage', 'Hypotheses');
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
    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
    const item = dialog.getByRole('menuitem', { name: /^ui-nostage/ });
    await expect(item).not.toContainText('Built for');
    await item.click();
    await expect(dialog.getByRole('button', { name: /Create study/ })).toBeDisabled();
    await dialog.getByRole('button', { name: 'Stages: choose a stage' }).click();
    await chooseCompact(page, 'Stage', 'Validation');
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
        scope: { from: 'grounding', to: 'validation' },
      },
    });
    expect(made.status()).toBe(201);
    const dialog = await composer(page);
    await dialog
      .getByRole('textbox', { name: 'Describe your study' })
      .fill(`Refused set ${Date.now()}`);

    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
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
    await expect(dialog.getByRole('button', { name: 'Assignees' })).toBeVisible();
    await expect(dialog.getByRole('textbox', { name: 'Describe your study' })).toHaveAttribute(
      'placeholder',
      'Describe what you want to do, in your own words',
    );
    await expect(
      dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }),
    ).toBeEnabled();
  });

  test('the picker lists Teams and Team sets', async ({ page }) => {
    await labTeam(page.request, await labWorkspace(page.request));
    await stageTeam(page, 'ui-hyp', 'hypotheses');
    const dialog = await composer(page);
    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
    const menu = dialog.getByRole('menu');
    await expect(menu.getByText('Custom Teams', { exact: true })).toBeVisible();
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
    await chooseCompact(page, 'Grounding Team', 'ui-demo');
    await chooseCompact(page, 'Hypotheses Team', 'ui-hyp');
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

    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
    await dialog.getByRole('menuitem', { name: 'New set…' }).click();
    await chooseCompact(page, 'Hypotheses Team', 'ui-hyp');
    await dialog.getByRole('button', { name: 'Use this set' }).click();
    await expect(dialog.getByRole('button', { name: 'Assignee: Your Team set' })).toBeVisible();
    const saved = page.waitForResponse(
      (r) => r.url().endsWith('/api/cards') && r.request().method() === 'POST',
    );
    await dialog.getByRole('button', { name: /Create study/ }).click();
    const response = await saved;
    expect(response.status(), await response.text()).toBe(201);
    expect(response.request().postDataJSON().scope).toEqual({
      from: 'grounding',
      to: 'validation',
    });
    const study = await studyOf(page, question);
    expect([study.scope_from, study.scope_to]).toEqual(['grounding', 'validation']);
    expect(Object.keys(study.teams).sort()).toEqual([
      'experimentation',
      'grounding',
      'hypotheses',
      'validation',
    ]);
    expect(study.teams.hypotheses!.pack_id).toBe(hyp);
  });

  async function soloAgent(page: import('@playwright/test').Page) {
    const headers = { 'X-Workspace-ID': await labWorkspace(page.request) };
    const name = `Solo ${Date.now()}`;
    const made = await page.request.post('/api/agents', {
      headers,
      data: { name, stage: 'experimentation', harness: 'claude' },
    });
    expect(made.status()).toBe(201);
    return { name, id: ((await made.json()) as { id: string }).id };
  }

  test('one agent works a study alone, with no auditor', async ({ page }) => {
    const agent = await soloAgent(page);
    const question = `One agent, every stage ${Date.now()}`;
    const dialog = await composer(page);
    await dialog.getByRole('textbox', { name: 'Describe your study' }).fill(question);
    await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
    const menu = dialog.getByRole('menu');
    await expect(menu.getByText('Agents', { exact: true })).toBeVisible();
    await expect(menu.getByRole('menuitem', { name: 'Manage agents' })).toHaveAttribute(
      'href',
      '#/agents',
    );
    await menu.getByRole('menuitem', { name: agent.name, exact: true }).click();
    await expect(dialog.locator('.assignee-note')).toHaveCount(0);
    await expect(
      dialog.getByRole('button', { name: 'Stages: Grounding → Validation' }),
    ).toBeEnabled();

    await dialog.getByRole('button', { name: /Create study/ }).click();
    await expect(dialog).toHaveCount(0);
    const study = await studyOf(page, question);
    expect(study.assignee_type).toBe('agent');
    expect(study.assignee_id).toBe(agent.id);
    expect(study.teams).toEqual({});
  });

  test('the chips sit on one row and fold into ⋯ when narrow', async ({ page }, testInfo) => {
    const agent = await soloAgent(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    const noScroll = async () =>
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
        ),
      ).toBeLessThanOrEqual(0);

    // With an agent picked, every chip fits one row in both palettes.
    let dialog = page.getByRole('dialog', { name: 'New study' });
    for (const [theme, palette] of [
      ['light', 'ash'],
      ['dark', 'midnight'],
    ] as const) {
      await page.goto('/#/studies');
      await page.evaluate(
        (p) => localStorage.setItem('lykeion.preferences', JSON.stringify({ theme: p })),
        palette,
      );
      await page.reload();
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
      dialog = await composer(page);
      await dialog.getByRole('button', { name: 'Assignees', exact: true }).click();
      await dialog.getByRole('menuitem', { name: agent.name, exact: true }).click();
      await dialog.getByRole('textbox', { name: 'Study title' }).fill('Sleep and memory');
      await expect(dialog.locator('.assignee-note')).toHaveCount(0);
      const row = dialog.getByRole('group', { name: 'Study properties' });
      for (const name of [
        `Assignee: ${agent.name}`,
        'Stages: Grounding → Validation',
        'Start date',
        'Due date',
        'No project',
      ])
        await expect(row.getByRole('button', { name, exact: true })).toBeVisible();
      await expect(row.getByRole('button', { name: 'More properties' })).toHaveCount(0);
      const tops = await row.evaluate((el) =>
        [...el.querySelectorAll('.property-pill, .machine-trigger')].map((c) =>
          Math.round(c.getBoundingClientRect().top),
        ),
      );
      expect(new Set(tops).size, String(tops)).toBe(1);
      await noScroll();
      const path = testInfo.outputPath(`new-study-${theme}.png`);
      await page.screenshot({ path });
      await testInfo.attach(`new-study-${theme}`, { path, contentType: 'image/png' });
    }

    // The date and project controls remain reachable on narrow screens.
    await page.setViewportSize({ width: 600, height: 800 });
    const row = dialog.getByRole('group', { name: 'Study properties' });
    await expect(row.getByRole('button', { name: 'Start date' })).toBeVisible();
    await expect(row.getByRole('button', { name: 'Due date' })).toBeVisible();
    await expect(row.getByRole('button', { name: 'No project' })).toBeVisible();
    await noScroll();
    await row.getByRole('button', { name: 'Due date' }).click();
    await expect(page.locator('.composer-calendar')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.composer-calendar')).toHaveCount(0);
  });
});
