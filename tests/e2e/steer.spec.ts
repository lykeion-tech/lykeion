import { expect, test, type Page } from '@playwright/test';
import { labHypotheses, newStudy } from './lab';

async function choose(page: Page, ...path: string[]) {
  await page.getByRole('button', { name: 'Action' }).click();
  for (const item of path) await page.getByRole('menuitem', { name: item }).click();
}
async function escalated(page: Page) {
  await expect(page.getByText('Escalated: waiting for a person').first()).toBeVisible({
    timeout: 30_000,
  });
}

test('Stop ends the study after confirmation and removes the composer', async ({ page }) => {
  const study = await newStudy(page.request, `GLP-1 agonists and alcohol intake ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page);
  await choose(page, 'Stop');
  await page.getByRole('textbox', { name: 'Comment' }).fill('no usable data in human cohorts');
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(
    page
      .getByRole('complementary', { name: 'Study details' })
      .getByText('Stopped · no usable data in human cohorts'),
  ).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
});

test('a stopped study keeps its full long reason inside the metadata pane', async ({ page }) => {
  await page.setViewportSize({ width: 1580, height: 1000 });
  const study = await newStudy(page.request, `Primary liver evidence ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page);
  const reason = `No usable primary-source evidence after checking mouse liver cohorts. ${'unbroken'.repeat(40)}`;
  await choose(page, 'Stop');
  await page.getByRole('textbox', { name: 'Comment' }).fill(reason);
  page.once('dialog', (d) => d.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  const meta = page.getByRole('complementary', { name: 'Study details' });
  const ending = meta.getByText(`Stopped · ${reason}`, { exact: true });
  await expect(ending).toBeVisible();
  const fits = await ending.evaluate((el) => {
    const pane = el.closest('[aria-label="Study details"]')!;
    const chip = el.getBoundingClientRect();
    const bounds = pane.getBoundingClientRect();
    const text = document.createRange();
    text.selectNodeContents(el);
    return {
      chipInsidePane: chip.left >= bounds.left && chip.right <= bounds.right + 1,
      chipInsideViewport: chip.right <= window.innerWidth + 1,
      textInsideChip: [...text.getClientRects()].every(
        (line) =>
          line.left >= chip.left - 1 &&
          line.right <= chip.right + 1 &&
          line.top >= chip.top - 1 &&
          line.bottom <= chip.bottom + 1,
      ),
      noHiddenText: el.scrollWidth <= el.clientWidth + 1 && el.scrollHeight <= el.clientHeight + 1,
    };
  });
  expect(fits).toEqual({
    chipInsidePane: true,
    chipInsideViewport: true,
    textInsideChip: true,
    noHiddenText: true,
  });
});

test('Resume reruns an escalated study and posts the typed text as a comment', async ({ page }) => {
  const study = await newStudy(
    page.request,
    `Tau spread through the entorhinal cortex ${Date.now()}?`,
  );
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page);
  await choose(page, 'Resume');
  await page.getByRole('textbox', { name: 'Comment' }).fill('Widen to rodent studies.');
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText(/^Resumed/)).toBeVisible();
  await expect(page.locator('.bubble', { hasText: 'Widen to rodent studies.' })).toBeVisible();
  // The rerun escalates again; its new run turn arrives live.
  await expect(page.locator('.full-history')).toHaveCount(2, {
    timeout: 30_000,
  });
});

test('Send back moves a Hypotheses study to Grounding with the reason', async ({ page }) => {
  const study = await newStudy(page.request, `Islet amyloid in human donors ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page);
  await labHypotheses(page.request, study.id);
  const ws = await labWorkspace(page.request);
  const before = await actualCard(page, ws, study.id);
  const meta = page.getByRole('complementary', { name: 'Study details' });
  await expect(meta.getByText('Hypotheses')).toBeVisible({ timeout: 10_000 });
  await choose(page, 'Send back to', 'Grounding');
  await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Send back to Grounding/);
  await page
    .getByRole('textbox', { name: 'Comment' })
    .fill('Two passages come from reviews; use primary studies.');
  await page.screenshot({ path: `${artifacts}/send-back-typed.png`, fullPage: true });
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText(/^Sent back to Grounding/)).toBeVisible();
  await expect(meta.getByText('Grounding')).toBeVisible();
  const after = await actualCard(page, ws, study.id);
  expect(after.round).toBeGreaterThan(before.round);
  expect(after.handoff_note).toBe('Two passages come from reviews; use primary studies.');
  expect(after.board_column).toBe('grounding');
});

test('Send back offers only earlier columns, and nothing in Grounding', async ({ page }) => {
  const study = await newStudy(page.request, `Lithium in bipolar telomeres ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page);
  await page.getByRole('button', { name: 'Action' }).click();
  await expect(page.getByRole('menuitem', { name: 'Stop' })).toBeVisible();
  await page.screenshot({ path: `${artifacts}/grounding-actions.png`, fullPage: true });
  await expect(page.getByRole('menuitem', { name: 'Send back to' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await labHypotheses(page.request, study.id);
  await expect(
    page.getByRole('complementary', { name: 'Study details' }).getByText('Hypotheses'),
  ).toBeVisible({ timeout: 10_000 });
  await choose(page, 'Send back to');
  const targets = page.getByRole('menu', { name: 'Send back to' }).getByRole('menuitem');
  await expect(targets).toHaveText(['Grounding']);
  await page.screenshot({ path: `${artifacts}/send-back-targets.png`, fullPage: true });
});

import { labFixture, labNoTeam, labPack, labWorkspace } from './lab';

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-17-artifacts';

async function recordedStudy(page: Page) {
  const f = await labFixture(page.request);
  await page.addInitScript((ws) => localStorage.setItem('lykeion.workspace', ws), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  await expect(page.getByRole('heading', { name: 'Gate and transcript fixture' })).toBeVisible();
  return f;
}
async function actualCard(page: Page, ws: string, id: string) {
  const response = await page.request.get(`/api/cards/${id}`, {
    headers: { 'X-Workspace-ID': ws },
  });
  expect(response.status()).toBe(200);
  return response.json();
}
async function actualComments(page: Page, ws: string, id: string) {
  const response = await page.request.get(`/api/cards/${id}/comments`, {
    headers: { 'X-Workspace-ID': ws },
  });
  expect(response.status()).toBe(200);
  return response.json() as Promise<{ body: string }[]>;
}

// Holds only the delivery of a real server response, after its transaction finishes.
async function hold(page: Page, path: string) {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let accept!: (value: { status: number; workspace?: string; body: unknown }) => void;
  const accepted = new Promise<{ status: number; workspace?: string; body: unknown }>((resolve) => {
    accept = resolve;
  });
  let done!: () => void;
  const drained = new Promise<void>((resolve) => {
    done = resolve;
  });
  await page.route(`**${path}`, async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    try {
      const response = await route.fetch();
      accept({
        status: response.status(),
        workspace: route.request().headers()['x-workspace-id'],
        body: route.request().postDataJSON(),
      });
      await gate;
      await route.fulfill({ response });
    } finally {
      done();
    }
  });
  return {
    accepted,
    release,
    async finish() {
      const response = page.waitForResponse(
        (r) => r.request().method() === 'POST' && r.url().endsWith(path),
      );
      release();
      await drained;
      await (await response).finished();
      await page.evaluate(() => new Promise(requestAnimationFrame));
    },
  };
}

for (const text of ['', 'Widen the recorded search.']) {
  test(`Resume sends no reason and preserves its actual server outcome ${text ? 'with' : 'without'} a comment`, async ({
    page,
  }) => {
    const f = await recordedStudy(page);
    const requests: { workspace?: string; body: string | null }[] = [];
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${f.card_id}/resume`))
        requests.push({ workspace: r.headers()['x-workspace-id'], body: r.postData() });
    });
    await choose(page, 'Resume');
    await page.getByRole('textbox', { name: 'Comment' }).fill(text);
    await page.screenshot({
      path: `${artifacts}/resume${text ? '-comment' : '-empty'}.png`,
      fullPage: true,
    });
    const resumedResponse = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith(`/api/cards/${f.card_id}/resume`),
    );
    await page.getByRole('button', { name: 'Send' }).click();
    const resumed = await resumedResponse;
    expect(resumed.status()).toBe(200);
    expect((await resumed.json()).flag).toBe('');
    await expect(page.getByText(/^Resumed/)).toBeVisible();
    expect(requests).toEqual([{ workspace: f.workspace_id, body: null }]);
    expect((await actualComments(page, f.workspace_id, f.card_id)).map((c) => c.body)).toEqual(
      text ? [text] : [],
    );
    await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Comment/);
  });
}

test('a failed Resume retry does not duplicate its acknowledged comment', async ({ page }) => {
  const f = await recordedStudy(page);
  let attempts = 0;
  await page.route(`**/api/cards/${f.card_id}/resume`, async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    if (++attempts === 1) return route.abort('connectionfailed');
    await route.continue();
  });
  const text = 'This comment was already accepted before the transport failure.';
  await choose(page, 'Resume');
  await page.getByRole('textbox', { name: 'Comment' }).fill(text);
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(text);
  await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Resume/);
  expect((await actualComments(page, f.workspace_id, f.card_id)).map((c) => c.body)).toEqual([
    text,
  ]);
  await page.screenshot({ path: `${artifacts}/resume-error-retention.png`, fullPage: true });
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByText(/^Resumed/)).toBeVisible();
  expect(attempts).toBe(2);
  expect((await actualComments(page, f.workspace_id, f.card_id)).map((c) => c.body)).toEqual([
    text,
  ]);
});

test('a server Send back refusal retains its reason and selected action', async ({ page }) => {
  const f = await recordedStudy(page);
  await labHypotheses(page.request, f.card_id);
  await expect(
    page.getByRole('complementary', { name: 'Study details' }).getByText('Hypotheses'),
  ).toBeVisible();
  await choose(page, 'Send back to', 'Grounding');
  const reason = 'Use the primary source before returning.';
  await page.getByRole('textbox', { name: 'Comment' }).fill(reason);
  // A concurrent person's actual send-back makes this target no longer earlier.
  expect(
    (
      await page.request.post(`/api/cards/${f.card_id}/send-back`, {
        headers: { 'X-Workspace-ID': f.workspace_id },
        data: { to: 'grounding', reason: 'Concurrent route back' },
      })
    ).status(),
  ).toBe(200);
  const response = page.waitForResponse(
    (r) => r.url().endsWith(`/api/cards/${f.card_id}/send-back`) && r.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Send' }).click();
  const refused = await response;
  expect(refused.status()).toBe(400);
  await expect(page.getByRole('alert')).toHaveText((await refused.json()).error);
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(reason);
  await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Send back to Grounding/);
  await expect(
    page.getByRole('complementary', { name: 'Study details' }).getByText('Grounding'),
  ).toBeVisible();
  await page.screenshot({ path: `${artifacts}/send-back-error-retention.png`, fullPage: true });
});

test('a multipart Resume cannot continue after a different visit, even A to B to A', async ({
  page,
}) => {
  const f = await recordedStudy(page);
  const created = await page.request.post('/api/cards', {
    headers: { 'X-Workspace-ID': f.workspace_id },
    data: { question: `Other visit ${Date.now()}?`, title: 'Other steering visit' },
  });
  expect(created.status()).toBe(201);
  const b = (await created.json()).id;
  const held = await hold(page, `/api/cards/${f.card_id}/comments`);
  let resumes = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${f.card_id}/resume`)) resumes++;
  });
  try {
    await choose(page, 'Resume');
    await page.getByRole('textbox', { name: 'Comment' }).fill('Accepted on the old visit');
    await page.getByRole('button', { name: 'Send' }).click();
    expect(await held.accepted).toEqual({
      status: 201,
      workspace: f.workspace_id,
      body: { body: 'Accepted on the old visit' },
    });
    await page.evaluate((id) => {
      location.hash = `/studies/${id}`;
    }, b);
    await expect(page.getByRole('heading', { name: 'Other steering visit' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Comment/);
    await page.evaluate((id) => {
      location.hash = `/studies/${id}`;
    }, f.card_id);
    await expect(page.getByRole('heading', { name: 'Gate and transcript fixture' })).toBeVisible();
    await choose(page, 'Stop');
    await page.getByRole('textbox', { name: 'Comment' }).fill('Keep the returned visit reason');
    await held.finish();
    expect(resumes).toBe(0);
    expect((await actualCard(page, f.workspace_id, f.card_id)).flag).toBe('escalated');
    await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
      'Keep the returned visit reason',
    );
    await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Stop/);
    await expect(page.getByRole('button', { name: 'Send' })).toBeEnabled();
  } finally {
    held.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('Resume captures its action and text before its comment response arrives', async ({
  page,
}) => {
  const f = await recordedStudy(page);
  const held = await hold(page, `/api/cards/${f.card_id}/comments`);
  let stops = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${f.card_id}/stop`)) stops++;
  });
  try {
    await choose(page, 'Resume');
    await page.getByRole('textbox', { name: 'Comment' }).fill('Original captured Resume text');
    await page.getByRole('button', { name: 'Send' }).click();
    expect((await held.accepted).status).toBe(201);
    await choose(page, 'Stop');
    await page.getByRole('textbox', { name: 'Comment' }).fill('A later different action');
    await held.finish();
    await expect(page.getByText(/^Resumed/)).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
      'A later different action',
    );
    await expect(page.getByRole('button', { name: 'Action', exact: true })).toHaveText(/Stop/);
    expect(stops).toBe(0);
    expect((await actualComments(page, f.workspace_id, f.card_id)).map((c) => c.body)).toEqual([
      'Original captured Resume text',
    ]);
    expect((await actualCard(page, f.workspace_id, f.card_id)).ending).toBe('');
  } finally {
    held.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

test('Stop cancellation retains text and sends nothing until confirmation', async ({ page }) => {
  const f = await recordedStudy(page);
  let stops = 0;
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith(`/api/cards/${f.card_id}/stop`)) stops++;
  });
  await choose(page, 'Stop');
  await page.getByRole('textbox', { name: 'Comment' }).fill('The recorded data cannot answer it.');
  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('Ended studies are never reopened.');
    await dialog.dismiss();
  });
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
    'The recorded data cannot answer it.',
  );
  expect(stops).toBe(0);
  expect((await actualCard(page, f.workspace_id, f.card_id)).ending).toBe('');
  await page.screenshot({ path: `${artifacts}/stop-confirmation-retained.png`, fullPage: true });
  page.once('dialog', (dialog) => dialog.accept());
  await page.getByRole('button', { name: 'Send' }).click();
  await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
  expect(await actualCard(page, f.workspace_id, f.card_id)).toMatchObject({
    ending: 'stopped',
    end_reason: 'The recorded data cannot answer it.',
  });
});

test('a multipart Resume cannot issue its second write in a new workspace', async ({ page }) => {
  const name = `Steering workspace ${Date.now()}`;
  const wsResponse = await page.request.post('/api/workspaces', {
    data: { name, slug: `steer-${Date.now()}` },
  });
  expect(wsResponse.status()).toBe(201);
  const ws = (await wsResponse.json()).id;
  expect(
    (
      await page.request.patch(`/api/workspaces/${ws}/settings`, {
        data: { settings: { wip_limit: 0 } },
      })
    ).status(),
  ).toBe(204);
  const cardResponse = await page.request.post('/api/cards', {
    headers: { 'X-Workspace-ID': ws },
    data: { question: `New scope ${Date.now()}?`, title: 'Current workspace steering' },
  });
  expect(cardResponse.status()).toBe(201);
  const b = (await cardResponse.json()).id;
  const f = await recordedStudy(page);
  const held = await hold(page, `/api/cards/${f.card_id}/comments`);
  const resumes: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().endsWith('/resume')) resumes.push(r.url());
  });
  try {
    await choose(page, 'Resume');
    await page.getByRole('textbox', { name: 'Comment' }).fill('The old workspace comment');
    await page.getByRole('button', { name: 'Send' }).click();
    expect((await held.accepted).workspace).toBe(f.workspace_id);
    await page
      .getByRole('complementary', { name: 'Workspace navigation' })
      .getByRole('button', { name: /Browser fixtures/ })
      .click();
    await page
      .getByRole('menu', { name: 'Workspace' })
      .getByRole('menuitem', { name: new RegExp(name) })
      .click();
    await expect(page).toHaveURL(/#\/studies$/);
    await page
      .getByRole('region', { name: 'Backlog' })
      .getByRole('button', { name: /Current workspace steering/ })
      .click();
    await expect(page.getByRole('heading', { name: 'Current workspace steering' })).toBeVisible();
    await page.getByRole('textbox', { name: 'Comment' }).fill('Keep the new workspace draft');
    await held.finish();
    expect(resumes).toEqual([]);
    await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue(
      'Keep the new workspace draft',
    );
    await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Comment/);
    expect((await actualCard(page, f.workspace_id, f.card_id)).flag).toBe('escalated');
    expect(await actualComments(page, ws, b)).toEqual([]);
  } finally {
    held.release();
    await page.unrouteAll({ behavior: 'wait' });
  }
});

for (const reset of ['text', 'action'] as const) {
  test(`Resume comment receipt resets after a deliberate ${reset} change`, async ({ page }) => {
    const f = await recordedStudy(page);
    let attempts = 0;
    await page.route(`**/api/cards/${f.card_id}/resume`, async (route) => {
      if (++attempts === 1) return route.abort('connectionfailed');
      await route.continue();
    });
    await choose(page, 'Resume');
    const text = 'The accepted original Resume comment';
    await page.getByRole('textbox', { name: 'Comment' }).fill(text);
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.getByRole('alert')).toBeVisible();
    if (reset === 'action') {
      await choose(page, 'Comment');
      await choose(page, 'Resume');
    } else {
      await page.getByRole('textbox', { name: 'Comment' }).fill('A revised optional comment');
    }
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.getByText(/^Resumed/)).toBeVisible();
    expect((await actualComments(page, f.workspace_id, f.card_id)).map((c) => c.body)).toEqual([
      text,
      reset === 'action' ? text : 'A revised optional comment',
    ]);
  });
}

test('an active study still offers Stop and no way out of Blocked', async ({ page }) => {
  const study = await newStudy(page.request, `Ketamine and synaptic density ${Date.now()}?`);
  await page.goto(`/#/studies/${study.id}`);
  await escalated(page);
  await expect(page.locator('.block-bar')).toHaveCount(0);
  await page.getByRole('button', { name: 'Action' }).click();
  await expect(page.getByRole('menuitem', { name: 'Stop' })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: 'Close' })).toHaveCount(0);
  await expect(page.getByRole('menuitem', { name: 'Raise cap' })).toHaveCount(0);
});

test('a capped study shows why, a raised cap returns it to Working, and Close ends another as Stopped', async ({
  page,
}) => {
  test.setTimeout(330_000);
  await labPack(page.request);
  try {
    const ws = await labWorkspace(page.request);
    const raised = await newStudy(page.request, `Demo Team raise ${Date.now()}?`, {
      start_kind: 'question',
    });
    const closed = await newStudy(page.request, `Demo Team close ${Date.now()}?`, {
      start_kind: 'question',
    });
    await page.goto(`/#/studies/${raised.id}`);
    const bar = page.locator('.block-bar');
    await expect(bar).toContainText('Blocked: cap reached.', { timeout: 280_000 });
    await expect(bar).toContainText('failure 4 with a cap of 3');
    await page.getByRole('button', { name: 'Action' }).click();
    await expect(page.getByRole('menu', { name: 'Actions' }).getByRole('menuitem')).toHaveText([
      /^\s*Comment/,
      /^\s*Raise cap/,
      /^\s*Close/,
    ]);
    await page.keyboard.press('Escape');

    // A cap no greater than the uses already made is refused before sending.
    page.once('dialog', (d) => d.accept('4'));
    await choose(page, 'Raise cap');
    await expect(page.getByRole('alert')).toContainText('more than 4');
    await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Comment/);

    page.once('dialog', (d) => d.accept('5'));
    await choose(page, 'Raise cap');
    await expect(page.getByRole('button', { name: 'Action' })).toHaveText(/Raise cap to 5/);
    const unblock = page.waitForRequest(
      (r) => r.method() === 'POST' && r.url().endsWith(`/api/cards/${raised.id}/unblock`),
    );
    await page.getByRole('button', { name: 'Send' }).click();
    expect((await unblock).postDataJSON()).toEqual({ action: 'raise_cap', cap: 5 });
    await expect(bar).toHaveCount(0);
    await expect(page.locator('.stage-box .chip-working')).toHaveText('Working');
    expect(await actualCard(page, ws, raised.id)).toMatchObject({
      cap: 5,
      flag: '',
      block_kind: '',
    });

    await page.goto(`/#/studies/${closed.id}`);
    await expect(bar).toContainText('Blocked: cap reached.', { timeout: 60_000 });
    await choose(page, 'Close');
    await page.getByRole('textbox', { name: 'Comment' }).fill('the replication never holds');
    page.once('dialog', (d) => d.accept());
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(
      page
        .getByRole('complementary', { name: 'Study details' })
        .getByText('Stopped · the replication never holds'),
    ).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveCount(0);
    expect(await actualCard(page, ws, closed.id)).toMatchObject({
      ending: 'stopped',
      end_reason: 'the replication never holds',
    });
  } finally {
    await labNoTeam(page.request);
  }
});
