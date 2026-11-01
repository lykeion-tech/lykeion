import { expect, test } from '@playwright/test';
import { labPack, labWorkspace, labFixture, openAgentTranscript } from './lab';

test('Team agent skills save in place, and not while a study uses the Team', async ({ page }) => {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const original = await labPack(page.request, `skill-picker-${Date.now()}`, ws);
  await page.request.post(`/api/teams/${original.id}/add`, { headers });
  await page.goto(`/#/teams/${original.id}`);
  const member = page
    .locator('.team-member-profile')
    .filter({ has: page.getByRole('heading', { name: /^pi/ }) });
  await member.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Skills for pi', exact: true });
  await picker.getByRole('checkbox', { name: 'aeon', exact: true }).check();
  await picker.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(picker).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(original.id));
  await page.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  await expect(picker.getByRole('checkbox', { name: 'aeon', exact: true })).toBeChecked();
  await page.keyboard.press('Escape');
  await expect(picker).toHaveCount(0);
  const saved = await (await page.request.get(`/api/teams/${original.id}`, { headers })).json();
  expect([...saved.manifest.leader.skills].sort()).toEqual(['aeon', 'lead']);

  // A study working the Team locks it; the picker shows the server's refusal.
  const study = await page.request.post('/api/cards', {
    headers,
    data: {
      question: `Locking skill-selection study ${Date.now()}`,
      assignee: { type: 'team', teams: { grounding: original.id } },
      grounding_criteria: ['Peer-reviewed source'],
    },
  });
  expect(study.status()).toBe(201);
  await member.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  await picker.getByRole('checkbox', { name: 'aeon', exact: true }).uncheck();
  await picker.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(picker.getByRole('alert')).toContainText('Used by 1 study');
  await page.reload();
  await page.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  await expect(picker.getByRole('checkbox', { name: 'aeon', exact: true })).toBeChecked();
  await page.keyboard.press('Escape');
  await expect(picker).toHaveCount(0);
});

test('run header has only the agent name and Properties icons share a center line', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
  await page.getByRole('button', { name: 'Properties', exact: true }).click();
  const rows = page.locator('.study-meta > .property-row');
  const centers: number[] = [];
  for (let i = 0; i < 3; i++) {
    const row = rows.nth(i);
    const icon = row.locator('.property-icon');
    const box = (await icon.boundingBox())!;
    centers.push(box.x + box.width / 2);
  }
  expect(Math.max(...centers) - Math.min(...centers)).toBeLessThanOrEqual(1);
  const agentName = await page.locator('.agent-card').first().getByRole('heading').innerText();
  await openAgentTranscript(page);
  await expect(page.locator('.run-dialog-title h2')).toHaveText(agentName);
  await expect(page.locator('.run-dialog-title p')).toHaveCount(0);
});
