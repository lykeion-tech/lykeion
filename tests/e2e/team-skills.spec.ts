import { expect, test } from '@playwright/test';
import { labPack, labWorkspace, labFixture, openAgentTranscript } from './lab';

test('Team agent skills save a private version and preserve the original selection', async ({
  page,
}) => {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const original = await labPack(page.request, `skill-picker-${Date.now()}`, ws);
  await page.request.post(`/api/teams/${original.id}/add`, { headers });
  const before = await page.request.post('/api/cards', {
    headers,
    data: {
      question: 'Pinned skill-selection study',
      assignee: { type: 'team', teams: { grounding: original.id } },
    },
  });
  expect(before.status()).toBe(201);
  const pinnedStudy = await before.json();
  await page.goto(`/#/teams/${original.id}`);
  await page.getByRole('tab', { name: 'Agents', exact: true }).click();
  const member = page
    .locator('.team-member-profile')
    .filter({ has: page.getByRole('heading', { name: /^pi/ }) });
  await member.getByRole('button', { name: 'Edit skills for pi', exact: true }).click();
  const picker = page.getByRole('dialog', { name: 'Skills for pi', exact: true });
  await picker.getByRole('checkbox', { name: 'aeon', exact: true }).check();
  await picker.getByRole('button', { name: 'Save new version', exact: true }).click();
  await expect(picker).toHaveCount(0);
  await expect(page).not.toHaveURL(new RegExp(original.id));
  await page.getByRole('tab', { name: 'Agents', exact: true }).click();
  await expect(page.locator('.team-member-profile').first()).toContainText('aeon');
  const currentId = page.url().split('/').pop()!;
  const retained = await (
    await page.request.get(`/api/cards/${pinnedStudy.id}`, { headers })
  ).json();
  expect(retained.teams.grounding.pack_id).toBe(original.id);
  const after = await page.request.post('/api/cards', {
    headers,
    data: {
      question: 'New skill-selection study',
      assignee: { type: 'team', teams: { grounding: currentId } },
    },
  });
  expect(after.status()).toBe(201);
  expect((await after.json()).teams.grounding.pack_id).toBe(currentId);
  await page.reload();
  await page.getByRole('tab', { name: 'Agents', exact: true }).click();
  await expect(page.locator('.team-member-profile').first()).toContainText('aeon');
  const saved = await (await page.request.get(`/api/teams/${original.id}`, { headers })).json();
  expect(saved.manifest.leader.skills).toEqual(['lead']);
});

test('run header has only the agent name and Properties icons share a center line', async ({
  page,
}) => {
  const f = await labFixture(page.request);
  await page.addInitScript((id) => localStorage.setItem('lykeion.workspace', id), f.workspace_id);
  await page.goto(`/#/studies/${f.card_id}`);
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
