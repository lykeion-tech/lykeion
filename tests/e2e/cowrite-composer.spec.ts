import { expect, test } from '@playwright/test';
import { installAristotleFixture } from './aristotle-fixture';
const models = [
  { value: 'cli-default', label: 'Default (recommended)', description: 'CLI default' },
  { value: 'cli-large', label: 'Large', description: 'More capacity' },
];
test('Manage skills preserves the draft on Settings back and explicit New clears it', async ({
  page,
}) => {
  await installAristotleFixture(page, true);
  let uploads = 0;
  await page.route('**/api/me', (r) =>
    r.fulfill({
      json: { id: 'me', name: 'Ada', email: 'ada@example.com', onboarded_at: '2026-01-01' },
    }),
  );
  await page.route('**/api/runtimes', (r) =>
    r.fulfill({
      json: [{ id: 'own', owner_id: 'me', harness: 'claude', status: 'online', models }],
    }),
  );
  await page.route('**/api/coresearch/templates', (r) =>
    r.fulfill({
      json: [
        { id: 'blank', label: 'Blank' },
        { id: 'neurips', label: 'NeurIPS' },
      ],
    }),
  );
  await page.route('**/api/coresearch/uploads', (r) => {
    uploads++;
    return r.fulfill({ status: 201, json: { upload_id: 'held-upload' } });
  });
  await page.route('**/api/skills', (r) =>
    r.fulfill({
      json: [
        {
          id: 'research',
          name: 'Research',
          domain: 'research',
          description: 'Find evidence',
          origin: 'imported',
          revision: 1,
          policy_enabled: true,
          policy_revision: 1,
          source: { kind: 'url', location: 'https://example.com/skill' },
          editable: true,
        },
      ],
    }),
  );
  await page.goto('/#/coresearch/new');
  await page.getByRole('button', { name: 'Cowrite', exact: true }).click();
  await page
    .getByRole('button', {
      name: 'CLI and model: Claude Code · Default (recommended)',
      exact: true,
    })
    .click();
  await page.getByRole('button', { name: 'Large — More capacity', exact: true }).click();
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Template', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'NeurIPS', exact: true }).click();
  await page.getByLabel('Files', { exact: true }).setInputFiles({
    name: 'figure.svg',
    mimeType: 'image/svg+xml',
    buffer: Buffer.from('<svg/>'),
  });
  await expect(page.getByRole('button', { name: 'Remove figure.svg', exact: true })).toBeVisible();
  await page
    .getByRole('textbox', { name: 'Message the agent' })
    .fill('Retain this paper description');
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Skills', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Manage skills', exact: true }).click();
  await expect(page).toHaveURL(/#\/settings\?tab=skills$/);
  await expect(page.getByRole('switch', { name: 'Enable Research', exact: true })).toBeEnabled();
  await page.goBack();
  await expect(page.getByRole('textbox', { name: 'Message the agent' })).toHaveValue(
    'Retain this paper description',
  );
  await expect(page.getByRole('button', { name: 'Cowrite', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(
    page.getByRole('button', { name: 'CLI and model: Claude Code · Large', exact: true }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Remove figure.svg', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Template', exact: true }).click();
  await expect(page.getByRole('menuitemradio', { name: 'NeurIPS', exact: true })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  expect(uploads).toBe(1);
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('link', { name: 'New conversation', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Message the agent' })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Experiment', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(page.getByRole('button', { name: 'Remove figure.svg', exact: true })).toHaveCount(0);
});
for (const width of [1440, 390]) {
  test(`${width}: draft model menu is inside the frame without scrolling it into view`, async ({
    page,
  }) => {
    await installAristotleFixture(page, true);
    await page.setViewportSize({ width, height: width === 1440 ? 900 : 844 });
    await page.route('**/api/runtimes', (r) =>
      r.fulfill({
        json: [{ id: 'own', owner_id: 'me', harness: 'claude', status: 'online', models }],
      }),
    );
    await page.goto('/#/coresearch/new');
    if (width === 390)
      await page.getByRole('button', { name: 'Toggle left sidebar', exact: true }).click();
    await page
      .getByRole('button', {
        name: 'CLI and model: Claude Code · Default (recommended)',
        exact: true,
      })
      .click();
    const bounds = await page
      .getByRole('dialog', { name: 'CLI and model', exact: true })
      .evaluate((element) => {
        const menu = element.getBoundingClientRect();
        const frame = document.querySelector('.aristotle-chat')!.getBoundingClientRect();
        return {
          top: menu.top,
          bottom: menu.bottom,
          frameTop: frame.top,
          frameBottom: frame.bottom,
        };
      });
    expect(bounds.top).toBeGreaterThanOrEqual(bounds.frameTop);
    expect(bounds.bottom).toBeLessThanOrEqual(bounds.frameBottom);
  });
}
test('draft composer uploads before Cowrite acceptance and routes the selected model and template', async ({
  page,
}) => {
  await installAristotleFixture(page, true);
  const requests: { path: string; body: unknown; workspace?: string }[] = [];
  await page.route('**/api/runtimes', (r) =>
    r.fulfill({
      json: [{ id: 'own', owner_id: 'me', harness: 'claude', status: 'online', models }],
    }),
  );
  await page.route('**/api/coresearch/templates', (r) =>
    r.fulfill({
      json: [
        { id: 'blank', label: 'Blank' },
        { id: 'neurips', label: 'NeurIPS' },
      ],
    }),
  );
  await page.route('**/api/coresearch/uploads', async (r) => {
    requests.push({
      path: '/api/coresearch/uploads',
      body: r.request().postData(),
      workspace: r.request().headers()['x-workspace-id'],
    });
    await r.fulfill({ status: 201, json: { upload_id: 'uploaded-figure' } });
  });
  await page.route('**/api/coresearch/conversations', async (r) => {
    if (r.request().method() === 'GET') {
      await r.fallback();
      return;
    }
    requests.push({
      path: '/api/coresearch/conversations',
      body: r.request().postDataJSON(),
      workspace: r.request().headers()['x-workspace-id'],
    });
    await r.fulfill({
      status: 201,
      json: {
        conversation: {
          id: 'accepted',
          project_id: 'paper',
          chat_id: 'paper-chat',
          mode: 'cowrite',
        },
        run: { id: 'run' },
      },
    });
  });
  await page.goto('/#/coresearch/new');
  await page.getByRole('button', { name: 'Cowrite', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Start paper', exact: true })).toBeDisabled();
  await expect(page.getByRole('textbox', { name: 'Message the agent' })).toHaveAttribute(
    'placeholder',
    'What is the paper about?',
  );
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Template', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'NeurIPS', exact: true }).click();
  await page
    .getByRole('button', {
      name: 'CLI and model: Claude Code · Default (recommended)',
      exact: true,
    })
    .click();
  await page.getByRole('button', { name: 'Large — More capacity', exact: true }).click();
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  const picker = page.waitForEvent('filechooser');
  await page.getByRole('menuitem', { name: 'Add files or photos', exact: true }).click();
  await (
    await picker
  ).setFiles({ name: 'figure.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg/>') });
  await expect(page.getByRole('button', { name: 'Remove figure.svg', exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: 'Message the agent' }).fill('A paper about residuals');
  await page.getByRole('button', { name: 'Start paper', exact: true }).click();
  await expect(page).toHaveURL(/#\/coresearch\/paper\/paper-chat$/);
  expect(requests.map((r) => r.path)).toEqual([
    '/api/coresearch/uploads',
    '/api/coresearch/conversations',
  ]);
  expect(requests.every((r) => r.workspace === 'lab')).toBe(true);
  expect(requests[1]!.body).toMatchObject({
    body: 'A paper about residuals',
    mode: 'cowrite',
    model: 'cli-large',
    template: 'neurips',
    attachments: [{ upload_id: 'uploaded-figure', name: 'figure.svg' }],
  });
});
test('member sees disabled switches and an empty runtime catalogue shows plain Default', async ({
  page,
}) => {
  await installAristotleFixture(page, true);
  await page.route('**/api/workspaces', (r) =>
    r.fulfill({ json: [{ id: 'lab', name: 'Fixture lab', role: 'member' }] }),
  );
  await page.route('**/api/skills', (r) =>
    r.fulfill({
      json: [
        {
          id: 'research',
          name: 'Research',
          description: 'Find evidence',
          policy_enabled: true,
          policy_revision: 1,
        },
      ],
    }),
  );
  await page.route('**/api/connectors', (r) =>
    r.fulfill({
      json: [{ name: 'openalex', purpose: 'Research papers', policy_enabled: true, revision: 1 }],
    }),
  );
  await page.goto('/#/coresearch/new');
  await expect(
    page.getByRole('button', { name: 'CLI and model: Claude Code · Default' }),
  ).toBeVisible();
  await page.getByRole('textbox', { name: 'Message the agent' }).fill('/');
  await expect(page.getByRole('option', { name: 'Research Find evidence' })).toBeVisible();
  await page.getByRole('textbox', { name: 'Message the agent' }).press('Enter');
  await expect(page.getByRole('textbox', { name: 'Message the agent' })).toHaveValue('/Research ');
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Skills', exact: true }).click();
  await expect(page.getByRole('switch', { name: 'Research', exact: true })).toBeDisabled();
  await page.getByRole('menuitem', { name: 'Connectors', exact: true }).click();
  await expect(page.getByRole('switch', { name: 'openalex', exact: true })).toBeDisabled();
  await expect(page.getByText('Delegation', { exact: true })).toHaveCount(0);
});
