import { expect, test, type BrowserContext } from '@playwright/test';
import { labSignIn, labWorkspace, newStudy } from './lab';

test('real members and admin cannot see another member’s manual notes', async ({
  page,
  browser,
}) => {
  const ws = await labWorkspace(page.request);
  const headers = { 'X-Workspace-ID': ws };
  const contexts: BrowserContext[] = [];
  async function join(email: string, role: string) {
    const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    contexts.push(context);
    const invitation = await page.request.post(`/api/workspaces/${ws}/invitations`, {
      headers,
      data: { email, role },
    });
    expect(invitation.status()).toBe(201);
    await labSignIn(context.request, email);
    expect(
      (
        await context.request.post(`/api/invitations/${(await invitation.json()).token}/accept`)
      ).status(),
    ).toBe(200);
    expect(
      (await context.request.patch('/api/me', { data: { name: email, onboarded: true } })).status(),
    ).toBe(200);
    return context;
  }
  try {
    const creator = await join('manual-creator@lab.test', 'member');
    const other = await join('manual-other@lab.test', 'member');
    const admin = await join('manual-admin@lab.test', 'admin');
    const study = await newStudy(page.request, 'Manual acceptance colleague study', {
      assignee: null,
    });
    const p = await creator.newPage();
    await p.goto('/#/decisions/new');
    await p.getByRole('button', { name: /Start blank/ }).click();
    await p
      .getByRole('textbox', { name: 'Title', exact: true })
      .fill('Private live acceptance note');
    await p
      .getByRole('textbox', { name: 'Context', exact: true })
      .fill('Private live acceptance context');
    await p.getByLabel('Study', { exact: true }).selectOption(study.id);
    await p.getByLabel('Deadline', { exact: true }).fill('2099-12-31');
    await expect(p.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
    await p.reload();
    await expect(p.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue(
      'Private live acceptance note',
    );
    expect((await (await creator.request.get('/api/decisions', { headers })).json()).length).toBe(
      0,
    );
    const posted = p.waitForResponse(
      (r) => r.url().endsWith('/api/decisions/manual') && r.request().method() === 'POST',
    );
    await p.getByRole('button', { name: 'Create', exact: true }).click();
    const response = await posted;
    expect(response.status()).toBe(201);
    const note = await response.json();
    await expect(p).toHaveURL(new RegExp(`decisions/${note.id}`));
    const unlinked = await creator.request.post('/api/decisions/manual', {
      headers,
      data: { title: 'Private unlinked live note' },
    });
    expect(unlinked.status()).toBe(201);
    const unlinkedID = (await unlinked.json()).id;
    async function privateAcrossAccounts() {
      for (const context of [other, admin, page.context()]) {
        for (const path of [
          '/api/decisions',
          '/api/decisions?view=history',
          `/api/cards/${study.id}/events?after=0`,
        ]) {
          const result = await context.request.get(path, { headers });
          expect(result.status()).toBe(200);
          const text = await result.text();
          expect(text).not.toContain(note.id);
          expect(text).not.toContain(unlinkedID);
          expect(text).not.toContain('Private live acceptance');
        }
        expect(
          (await (await context.request.get('/api/decisions/count', { headers })).json()).open,
        ).toBe(0);
        expect(
          (await context.request.post(`/api/decisions/${note.id}/resolve`, { headers })).status(),
        ).toBe(404);
      }
    }
    await privateAcrossAccounts();
    expect(
      (
        await creator.request.patch(`/api/decisions/${note.id}/manual`, {
          headers,
          data: { title: 'Private reviewed live note', card_id: study.id },
        })
      ).status(),
    ).toBe(200);
    expect(
      (
        await page.request.post(`/api/cards/${study.id}/stop`, {
          headers,
          data: { reason: 'Acceptance finished' },
        })
      ).status(),
    ).toBe(200);
    expect(
      (await (await creator.request.get('/api/decisions', { headers })).json()).filter(
        (row: any) => row.kind === 'manual_decision',
      ),
    ).toHaveLength(2);
    expect(
      (await creator.request.post(`/api/decisions/${note.id}/resolve`, { headers })).status(),
    ).toBe(200);
    expect(
      (await creator.request.post(`/api/decisions/${unlinkedID}/dismiss`, { headers })).status(),
    ).toBe(200);
    expect(
      (await (await creator.request.get('/api/decisions?view=history', { headers })).json()).filter(
        (row: any) => row.kind === 'manual_decision',
      ),
    ).toHaveLength(2);
    await privateAcrossAccounts();
    // This member has no machine. Its real offline response must preserve the form.
    await p.goto('/#/decisions/new');
    await p.getByRole('button', { name: /Create with AI/ }).click();
    await p
      .getByRole('textbox', { name: 'Question', exact: true })
      .fill('Draft a private reminder');
    await p.getByRole('button', { name: 'Ask', exact: true }).click();
    await expect(p.getByRole('alert').filter({ hasText: /offline|machine/i })).toBeVisible();
    await p.getByRole('button', { name: 'Minimize', exact: true }).click();
    await p
      .getByRole('textbox', { name: 'Title', exact: true })
      .fill('Preserved offline live draft');
    await expect(p.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible();
    await p.reload();
    await expect(p.getByRole('textbox', { name: 'Title', exact: true })).toHaveValue(
      'Preserved offline live draft',
    );
    await p.screenshot({ path: '/private/tmp/manual-decisions-live-offline.png', fullPage: true });
  } finally {
    for (const context of contexts) await context.close();
  }
});
