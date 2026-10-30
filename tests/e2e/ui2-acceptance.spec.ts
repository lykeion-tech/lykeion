import { expect, test, type Page } from '@playwright/test';
import { LAB, labFixture, labRealtime } from './lab';

const shots = 'test-results/ui2/task18/screenshots';
const palettes = ['ash', 'midnight', 'dawn', 'pale'];
async function palette(page: Page, name: string) {
  await page.goto('/#/settings/preferences');
  await page
    .getByRole('button', { name: name[0]!.toUpperCase() + name.slice(1), exact: true })
    .click();
  await expect(page.locator('html')).toHaveAttribute('data-palette', name);
}
async function fits(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}
for (const width of [1512, 390]) {
  test(`library and backend dialogs render all four actual palettes at ${width}px`, async ({
    page,
  }) => {
    const f = await labFixture(page.request);
    await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
    const headers = { 'X-Workspace-ID': f.workspace_id };
    const created = await page.request.post('/api/skills', {
      headers,
      data: { name: 'matrix-method', description: 'Review recorded evidence.' },
    });
    expect(created.status()).toBe(201);
    const agentResponse = await page.request.post('/api/agents', {
      headers,
      data: { name: 'Matrix backend', harness: 'codex' },
    });
    expect(agentResponse.status()).toBe(201);
    const agent = await agentResponse.json();
    await page.setViewportSize({ width, height: width === 390 ? 844 : 982 });
    const colors: Record<string, string[]> = { skill: [], connector: [], backend: [] };
    for (const theme of palettes) {
      await page.setViewportSize({ width: 1512, height: 982 });
      await palette(page, theme);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 982 });
      if (
        width === 390 &&
        !(await page.locator('.application').getAttribute('class'))?.includes('sidebar-hidden')
      )
        await page.getByRole('button', { name: 'Toggle left sidebar' }).click();
      await page.goto('/#/settings/skills');
      await expect(page.getByRole('button', { name: 'matrix-method', exact: true })).toBeVisible();
      await expect(page.getByRole('button', { name: 'Add skill', exact: true })).toBeInViewport({
        ratio: 1,
      });
      await fits(page);
      await page.screenshot({ path: `${shots}/${width}-${theme}-skills.png` });
      await page.getByRole('button', { name: 'matrix-method', exact: true }).click();
      const skill = page.getByRole('dialog', { name: 'matrix-method', exact: true });
      await expect(skill).toBeVisible();
      colors.skill!.push(
        await skill.evaluate(
          (el) => getComputedStyle(el).backgroundColor + ' / ' + getComputedStyle(el).color,
        ),
      );
      await skill.getByRole('button', { name: 'Edit', exact: true }).focus();
      await page.keyboard.press('Tab');
      await expect(skill.getByRole('button', { name: 'Delete skill', exact: true })).toBeFocused();
      await fits(page);
      await page.screenshot({ path: `${shots}/${width}-${theme}-skill-modal.png` });
      await page.keyboard.press('Escape');
      await expect(page.getByRole('button', { name: 'matrix-method', exact: true })).toBeFocused();
      if (width === 390) {
        const navigation = page.getByRole('combobox', { name: 'Settings section' });
        await expect(navigation).toBeInViewport({ ratio: 1 });
        await navigation.selectOption('connectors');
      } else await page.goto('/#/settings/connectors');
      await expect(page.getByRole('button', { name: 'biogrid', exact: true })).toBeVisible();
      await page
        .getByRole('button', { name: 'Add connector', exact: true })
        .scrollIntoViewIfNeeded();
      await expect(page.getByRole('button', { name: 'Add connector', exact: true })).toBeInViewport(
        { ratio: 1 },
      );
      await fits(page);
      await page.screenshot({ path: `${shots}/${width}-${theme}-connectors.png` });
      await page.getByRole('button', { name: 'biogrid', exact: true }).click();
      const connector = page.getByRole('dialog', { name: 'biogrid connector', exact: true });
      await expect(connector).toBeVisible();
      colors.connector!.push(
        await connector.evaluate(
          (el) => getComputedStyle(el).backgroundColor + ' / ' + getComputedStyle(el).color,
        ),
      );
      await connector.getByRole('tab', { name: 'Configuration', exact: true }).click();
      await fits(page);
      await page.screenshot({ path: `${shots}/${width}-${theme}-connector-modal.png` });
      await page.keyboard.press('Escape');
      await page.getByRole('button', { name: 'Add connector', exact: true }).click();
      const add = page.getByRole('dialog', { name: 'Add connector', exact: true });
      await expect(add).toBeVisible();
      await fits(page);
      await page.screenshot({ path: `${shots}/${width}-${theme}-custom-modal.png` });
      await page.keyboard.press('Escape');
      await page.goto(`/#/agents/${agent.id}`);
      await page.getByRole('tab', { name: 'Settings', exact: true }).click();
      const backendTrigger = page.getByRole('button', { name: 'Backend details', exact: true });
      await backendTrigger.click();
      const backend = page.getByRole('dialog', { name: 'Codex backend details' });
      await expect(backend).toBeVisible();
      await expect(backend).not.toContainText('Private only');
      colors.backend!.push(
        await page
          .locator('.backend-dialog')
          .evaluate(
            (el) => getComputedStyle(el).backgroundColor + ' / ' + getComputedStyle(el).color,
          ),
      );
      await fits(page);
      await page.screenshot({ path: `${shots}/${width}-${theme}-backend-modal.png` });
      await page.keyboard.press('Escape');
      await expect(backendTrigger).toBeFocused();
    }
    for (const [component, values] of Object.entries(colors))
      expect(new Set(values).size, component).toBe(4);
  });
}

test('two owner clients observe real daemon registration, expiry and return without reload', async ({
  page,
  browser,
}) => {
  test.setTimeout(150_000);
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  const context = await browser.newContext({ storageState: await page.context().storageState() });
  await context.addInitScript(
    (id) => localStorage.setItem('lykeion.workspace', id),
    f.workspace_id,
  );
  const other = await context.newPage();
  const control = async (action: string) => {
    const response = await page.request.post(`${LAB}/lab/availability-daemon/${action}`);
    expect(response.status(), await response.text()).toBe(204);
  };
  try {
    const sockets = [await labRealtime(page), await labRealtime(other)];
    for (const client of [page, other]) {
      await client.goto('/#/settings/skills');
      await client.getByRole('button', { name: 'Add skill', exact: true }).click();
      await client.getByRole('button', { name: 'Copy from runtime', exact: false }).click();
    }
    for (const socket of sockets) await socket.subscribed();
    await control('start');
    const option = (client: Page) =>
      client
        .getByRole('combobox', { name: 'Runtime', exact: true })
        .locator('option')
        .filter({ hasText: 'ui2-availability' });
    for (const client of [page, other]) {
      await expect(option(client)).toHaveCount(1);
      await expect(option(client)).toHaveJSProperty('disabled', false);
    }
    for (const socket of sockets) expect(socket.events()).toContain('runtime.online');
    const workspaceResponse = await page.request.get(`/api/workspaces/${f.workspace_id}`);
    expect(workspaceResponse.status()).toBe(200);
    const workspace = await workspaceResponse.json();
    const wizard = await context.newPage();
    await wizard.goto('/#/onboarding');
    await wizard.getByRole('button', { name: 'Set up your lab' }).click();
    await wizard
      .getByRole('radio', { name: workspace.name, exact: true })
      .and(wizard.locator(`[value="${f.workspace_id}"]`))
      .check();
    await wizard.getByRole('button', { name: 'Continue', exact: true }).click();
    await expect(wizard.locator('[aria-current=step]')).toHaveText('Machines');
    await expect(wizard.getByText('ui2-availability', { exact: false })).toBeVisible();
    await wizard.screenshot({ path: `${shots}/real-daemon-wizard-runtime.png` });
    for (const step of ['Skills', 'Connectors', 'Ready']) {
      await wizard.getByRole('button', { name: 'Continue', exact: true }).click();
      await expect(wizard.locator('[aria-current=step]')).toHaveText(step);
    }
    await wizard.getByRole('button', { name: 'Enter workspace' }).click();
    await expect(wizard).toHaveURL(/#\/studies$/);
    await wizard.close();
    await control('stop');
    await expect
      .poll(
        async () => {
          const response = await page.request.get('/api/runtimes', {
            headers: { 'X-Workspace-ID': f.workspace_id },
          });
          expect(response.status()).toBe(200);
          return (await response.json()).find(
            (r: { device_name: string }) => r.device_name === 'ui2-availability',
          )?.status;
        },
        { timeout: 100_000, intervals: [1000] },
      )
      .toBe('offline');
    for (const client of [page, other]) {
      await expect(option(client)).toHaveJSProperty('disabled', true);
      await expect(option(client)).toContainText('Offline');
    }
    for (const socket of sockets) expect(socket.events()).toContain('runtime.updated');
    const runtimeID = await option(page).getAttribute('value');
    await control('start');
    await expect
      .poll(async () => {
        const response = await page.request.get('/api/runtimes', {
          headers: { 'X-Workspace-ID': f.workspace_id },
        });
        expect(response.status()).toBe(200);
        return (await response.json()).find((runtime: { id: string }) => runtime.id === runtimeID)
          ?.status;
      })
      .toBe('online');
    for (const client of [page, other]) {
      await expect(option(client)).toHaveJSProperty('disabled', false);
      await expect(option(client)).not.toContainText('Offline');
    }
  } finally {
    await control('stop');
    await context.close();
  }
});

test('fresh daemon work completes a report visible from history and Inbox and create-study follows its card', async ({
  page,
}) => {
  const { labReportAutoresearch } = await import('./lab');
  const f = await labReportAutoresearch(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  const headers = { 'X-Workspace-ID': f.workspace_id };
  const started = await page.request.post(`/api/autoresearch/${f.autoresearch_id}/run`, {
    headers,
  });
  expect(started.status()).toBe(201);
  const run = await started.json();
  let report = '';
  await expect
    .poll(async () => {
      const response = await page.request.get(`/api/autoresearch-runs/${run.id}`, { headers });
      expect(response.status()).toBe(200);
      const result = await response.json();
      report = result.run.report;
      if (result.run.status === 'failed') {
        const agent = result.run.agent_run_id
          ? await (
              await page.request.get(`/api/agent-runs/${result.run.agent_run_id}`, { headers })
            ).json()
          : {};
        throw new Error(
          `Report failed: ${result.run.reason}; agent: ${agent.failure_reason ?? ''} ${agent.error ?? ''}`,
        );
      }
      return result.run.status;
    })
    .toBe('completed');
  expect(report).toContain('end-of-real-daemon-report');
  expect(report.length).toBeGreaterThan(5000);
  await page.goto(`/#/autoresearch/${f.autoresearch_id}`);
  await page.getByRole('button', { name: /Completed.*Open report/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Autoresearch run', exact: true });
  await expect(dialog.getByText('end-of-real-daemon-report', { exact: true })).toBeVisible();
  await expect(dialog.locator('script,img')).toHaveCount(0);
  expect(await page.evaluate(() => 'reportExecuted' in window)).toBe(false);
  await dialog.getByRole('tab', { name: 'Raw', exact: true }).click();
  await expect(dialog.locator('pre.run-raw')).toHaveText(report);
  await page.screenshot({ path: `${shots}/real-daemon-history.png` });
  await page.keyboard.press('Escape');
  const inbox = await page.request.get('/api/inbox', { headers });
  expect(inbox.status()).toBe(200);
  const item = (await inbox.json()).find(
    (i: { autoresearch_run_id: string }) => i.autoresearch_run_id === run.id,
  );
  expect(item).toBeTruthy();
  await page.goto(`/#/inbox/${item.id}`);
  await expect(dialog.getByText('end-of-real-daemon-report', { exact: true })).toBeVisible();
  await dialog.getByRole('tab', { name: 'Raw', exact: true }).click();
  await expect(dialog.locator('pre.run-raw')).toHaveText(report);
  await page.screenshot({ path: `${shots}/real-daemon-inbox.png` });
  await page.keyboard.press('Escape');
  const created = await page.request.post('/api/autoresearch', {
    headers,
    data: {
      title: 'Fresh create-study',
      description: 'Create a study to inspect.',
      execution_mode: 'create_card',
      assignee: { type: 'agent', id: f.agent_id },
      scope: { from: 'grounding', to: 'validation' },
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const ar = await created.json();
  const response = await page.request.post(`/api/autoresearch/${ar.id}/run`, { headers });
  expect(response.status()).toBe(201);
  const cardRun = await response.json();
  expect(cardRun.card_id).toBeTruthy();
  await page.goto(`/#/autoresearch/${ar.id}`);
  await page.getByRole('link', { name: /Open study/ }).click();
  await expect(page).toHaveURL(new RegExp(cardRun.card_id));
  await expect(page.getByRole('region', { name: 'Study', exact: true })).toBeVisible();
});
