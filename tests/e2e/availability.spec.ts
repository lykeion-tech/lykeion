import { expect, test } from '@playwright/test';
import { labWorkspace } from './lab';

test('skill row switch persists bundled policy without opening the detail', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  await page.goto('/#/settings/skills');
  await page.getByRole('searchbox', { name: 'Search skills' }).fill('aeon');
  const toggle = page.getByRole('switch', { name: 'Enable aeon', exact: true });
  await expect(toggle).toBeChecked();
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  const rows = await (await page.request.get('/api/skills', { headers })).json();
  expect(rows.find((s: { id: string }) => s.id === 'bundle:aeon').policy_enabled).toBe(false);
  await page.reload();
  await expect(toggle).not.toBeChecked();
  await toggle.click();
  await expect(toggle).toBeChecked();
});
test('connector row switch replaces status and preserves setup requirements', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const rows = await (await page.request.get('/api/connectors', { headers })).json();
  const setup = rows.find((c: { name: string }) => c.name === 'biogrid');
  expect(setup.policy_enabled).toBe(true);
  expect(setup.enabled).toBe(false);
  await page.goto('/#/settings/connectors');
  await page.getByRole('searchbox', { name: 'Search connectors' }).fill('biogrid');
  const toggle = page.getByRole('switch', { name: 'Enable biogrid', exact: true });
  await expect(toggle).toBeChecked();
  await expect(page.locator('.connector-row small')).toContainText(setup.reason);
  await expect(page.locator('.connector-row .status')).toHaveCount(0);
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  await page.reload();
  await expect(toggle).not.toBeChecked();
  await toggle.click();
  await expect(toggle).toBeChecked();
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('custom connector switches persist and update a second client', async ({ page, context }) => {
  const ws = await labWorkspace(page.request),
    headers = { 'X-Workspace-ID': ws },
    name = `policy-custom-${Date.now()}`;
  const created = await page.request.post('/api/mcp-servers', {
    headers,
    data: { name, config: { url: 'https://example.org/mcp' } },
  });
  expect(created.status()).toBe(201);
  const saved = await created.json();
  const other = await context.newPage();
  try {
    for (const p of [page, other]) {
      await p.goto('/#/settings/connectors');
      await p.getByRole('searchbox', { name: 'Search connectors' }).fill(name);
    }
    const a = page.getByRole('switch', { name: `Enable ${name}`, exact: true }),
      b = other.getByRole('switch', { name: `Enable ${name}`, exact: true });
    await expect(a).toBeChecked();
    await expect(b).toBeChecked();
    await a.click();
    await expect(a).not.toBeChecked();
    await expect(b).not.toBeChecked();
    const rows = await (await page.request.get('/api/mcp-servers', { headers })).json();
    const current = rows.find((r: { id: string }) => r.id === saved.id);
    expect(current.revision).toBe(saved.revision);
    expect(current.policy_revision).toBe(1);
    await a.click();
    await expect(a).toBeChecked();
    await expect(b).toBeChecked();
  } finally {
    await other.close();
  }
});

test('a stale row toggle reports the real conflict and retries the current revision', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request),
    headers = { 'X-Workspace-ID': ws };
  await page.goto('/#/settings/skills');
  await page.getByRole('searchbox', { name: 'Search skills' }).fill('aeon');
  const toggle = page.getByRole('switch', { name: 'Enable aeon', exact: true });
  await expect(toggle).toBeChecked();
  let changed = false;
  await page.route('**/api/availability/skill/bundle:aeon', async (route) => {
    if (!changed) {
      changed = true;
      const sent = route.request().postDataJSON();
      expect(
        (
          await page.request.put('/api/availability/skill/bundle:aeon', {
            headers,
            data: { revision: sent.revision, enabled: true },
          })
        ).status(),
      ).toBe(200);
    }
    await route.continue();
  });
  const refused = page.waitForResponse(
    (r) => r.url().includes('/api/availability/skill/') && r.status() === 409,
  );
  await toggle.click();
  await refused;
  await expect(page.getByRole('alert')).toContainText('availability changed');
  await expect(toggle).toBeChecked();
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  await expect(page.getByRole('alert')).toHaveCount(0);
  await toggle.click();
  await expect(toggle).toBeChecked();
});
