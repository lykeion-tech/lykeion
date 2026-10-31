import { selectPicker } from './select-picker';
import { expect, test } from '@playwright/test';

test('members, invitations and the last-owner rule', async ({ page }) => {
  await page.goto('/#/colleagues');
  const members = page.getByRole('table', { name: 'Members' });
  const me = members.getByRole('row').filter({ hasText: 'Ana Ruiz' });
  await expect(me).toContainText('ana@lab.test');
  await expect(me.getByRole('combobox', { name: 'Role of Ana Ruiz' })).toHaveAttribute(
    'data-value',
    'owner',
  );

  await selectPicker(me.getByRole('combobox', { name: 'Role of Ana Ruiz' }), 'member');
  await expect(page.getByRole('alert')).toHaveText('a workspace needs at least one owner');
  await expect(me.getByRole('combobox', { name: 'Role of Ana Ruiz' })).toHaveAttribute(
    'data-value',
    'owner',
  );

  const email = `c.lindqvist.${Date.now()}@ki.se`;
  await page.getByRole('button', { name: 'Invite' }).click();
  const dialog = page.getByRole('dialog', { name: 'Invite a colleague' });
  await dialog.getByRole('textbox', { name: 'Email' }).fill(email);
  await selectPicker(dialog.getByRole('combobox', { name: 'Role' }), 'member');
  await dialog.getByRole('button', { name: 'Send invitation' }).click();
  const pending = page
    .getByRole('table', { name: 'Pending invitations' })
    .getByRole('row')
    .filter({ hasText: email });
  await expect(pending).toContainText('Member');
  await pending.getByRole('button', { name: 'Revoke' }).click();
  await expect(pending).toHaveCount(0);
});

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-20-artifacts';
import { labFixture, labSignIn, labWorkspace } from './lab';
import type { APIRequestContext, BrowserContext, Page } from '@playwright/test';
const headers = (ws: string) => ({ 'X-Workspace-ID': ws });
const colleagueSessions = new Map<string, Awaited<ReturnType<BrowserContext['cookies']>>>();
const roster = (page: Page) => page.getByRole('table', { name: 'Members', exact: true });
const pending = (page: Page) => page.getByRole('table', { name: 'Pending invitations' });
async function invitations(request: APIRequestContext, ws: string) {
  const response = await request.get(`/api/workspaces/${ws}/invitations`, { headers: headers(ws) });
  expect(response.status()).toBe(200);
  return (await response.json()) as { id: string; email: string; role: string }[];
}
async function joined(
  owner: APIRequestContext,
  other: BrowserContext,
  ws: string,
  email: string,
  role = 'member',
) {
  const response = await owner.post(`/api/workspaces/${ws}/invitations`, {
    headers: headers(ws),
    data: { email, role },
  });
  expect(response.status()).toBe(201);
  const session = colleagueSessions.get(email);
  if (session) await other.addCookies(session);
  else {
    await labSignIn(other.request, email);
    colleagueSessions.set(email, await other.cookies());
  }
  expect(
    (await other.request.post(`/api/invitations/${(await response.json()).token}/accept`)).status(),
  ).toBe(200);
  expect(
    (await other.request.patch('/api/me', { data: { name: email, onboarded: true } })).status(),
  ).toBe(200);
  const me = await (await other.request.get('/api/me')).json();
  expect(me.email).toBe(email);
  return me.id as string;
}
async function openInvite(page: Page, email: string) {
  await page.getByRole('button', { name: 'Invite', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Invite a colleague' });
  await dialog.getByRole('textbox', { name: 'Email' }).fill(email);
  return dialog;
}

test('real owner, admin and member controls enforce F1 and remove a colleague', async ({
  page,
  browser,
}) => {
  const ws = await labWorkspace(page.request);
  const ownerId = (await (await page.request.get('/api/me')).json()).id;
  const admin = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const member = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  let adminId = '',
    memberId = '';
  try {
    adminId = await joined(page.request, admin, ws, 'colleagues-admin@lab.test', 'admin');
    memberId = await joined(page.request, member, ws, 'colleagues-member@lab.test');
    const adminPage = await admin.newPage();
    await adminPage.goto('/#/colleagues');
    const ownerRow = roster(adminPage).getByRole('row').filter({ hasText: 'ana@lab.test' });
    const ownerSelect = ownerRow.getByRole('combobox');
    await expect(ownerSelect).toHaveAttribute('data-value', 'owner');
    await expect(ownerSelect.locator('..').locator('select option[value="owner"]')).toBeDisabled();
    await expect(
      roster(adminPage)
        .getByRole('row')
        .filter({ hasText: 'colleagues-member@lab.test' })
        .getByRole('combobox')
        .locator('..').locator('select option[value="owner"]'),
    ).toHaveCount(0);
    const refusal = adminPage.waitForResponse(
      (r) => r.request().method() === 'PATCH' && r.url().endsWith(`/members/${ownerId}`),
    );
    await selectPicker(ownerSelect, 'member');
    expect((await refusal).status()).toBe(409);
    await expect(adminPage.getByRole('alert')).toHaveText('a workspace needs at least one owner');
    await expect(ownerSelect).toHaveAttribute('data-value', 'owner');
    adminPage.once('dialog', (d) => d.accept());
    const removal = adminPage.waitForResponse(
      (r) => r.request().method() === 'DELETE' && r.url().endsWith(`/members/${ownerId}`),
    );
    await ownerRow.getByRole('button', { name: 'Remove Ana Ruiz' }).click();
    expect((await removal).status()).toBe(409);
    await expect(adminPage.getByRole('alert')).toHaveText('a workspace needs at least one owner');
    await adminPage.screenshot({
      path: `${artifacts}/admin-last-owner-refusal.png`,
      fullPage: true,
    });
    const inviteDialog = await openInvite(adminPage, 'admin-invited@lab.test');
    await selectPicker(inviteDialog.getByRole('combobox', { name: 'Role' }), 'admin');
    await adminPage.screenshot({ path: `${artifacts}/invite.png`, fullPage: true });
    await inviteDialog.getByRole('button', { name: 'Send invitation' }).click();
    const invitationRow = pending(adminPage)
      .getByRole('row')
      .filter({ hasText: 'admin-invited@lab.test' });
    await expect(invitationRow).toContainText('Admin');
    await adminPage.screenshot({ path: `${artifacts}/admin-roster-pending.png`, fullPage: true });
    await invitationRow.getByRole('button', { name: 'Revoke' }).click();
    await expect(invitationRow).toHaveCount(0);
    const memberPage = await member.newPage();
    await memberPage.goto('/#/colleagues');
    await expect(roster(memberPage)).toContainText('Ana Ruiz');
    await expect(roster(memberPage).getByRole('combobox')).toHaveCount(0);
    await expect(roster(memberPage).getByRole('button')).toHaveCount(0);
    await expect(memberPage.getByRole('button', { name: 'Invite', exact: true })).toHaveCount(0);
    await expect(pending(memberPage)).toHaveCount(0);
    expect(
      (
        await member.request.get(`/api/workspaces/${ws}/invitations`, { headers: headers(ws) })
      ).status(),
    ).toBe(403);
    await memberPage.screenshot({ path: `${artifacts}/member-roster.png`, fullPage: true });
    await page.goto('/#/colleagues');
    const memberRow = roster(page)
      .getByRole('row')
      .filter({ hasText: 'colleagues-member@lab.test' });
    await selectPicker(memberRow.getByRole('combobox'), 'owner');
    await expect(memberRow.getByRole('combobox')).toHaveAttribute('data-value', 'owner');
    await selectPicker(memberRow.getByRole('combobox'), 'member');
    await expect(memberRow.getByRole('combobox')).toHaveAttribute('data-value', 'member');
    await page.screenshot({ path: `${artifacts}/owner-roster.png`, fullPage: true });
    page.once('dialog', (d) => d.dismiss());
    await memberRow.getByRole('button').click();
    await expect(memberRow).toBeVisible();
    page.once('dialog', (d) => d.accept());
    await memberRow.getByRole('button').click();
    await expect(memberRow).toHaveCount(0);
  } finally {
    for (const inv of await invitations(page.request, ws))
      if (inv.email === 'admin-invited@lab.test')
        await page.request.delete(`/api/workspaces/${ws}/invitations/${inv.id}`, {
          headers: headers(ws),
        });
    for (const id of [adminId, memberId].filter(Boolean))
      await page.request.delete(`/api/workspaces/${ws}/members/${id}`, { headers: headers(ws) });
    await admin.close();
    await member.close();
  }
});

test('a real refused invitation keeps the email and role inline for retry', async ({
  page,
  browser,
}) => {
  const ws = await labWorkspace(page.request);
  const guest = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const guestId = await joined(page.request, guest, ws, 'colleagues-refusal@lab.test');
  await page.route('**/api/workspaces/*/invitations', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await guest.request.fetch(route.request().url(), {
      method: 'POST',
      headers: headers(ws),
      data: route.request().postDataJSON(),
    });
    expect(response.status()).toBe(403);
    await route.fulfill({ response });
  });
  try {
    await page.goto('/#/colleagues');
    const dialog = await openInvite(page, 'draft-refused@lab.test');
    await selectPicker(dialog.getByRole('combobox', { name: 'Role' }), 'admin');
    await dialog.getByRole('button', { name: 'Send invitation' }).click();
    await expect(dialog.getByRole('alert')).toHaveText('admins only');
    await expect(dialog.getByRole('textbox', { name: 'Email' })).toHaveValue(
      'draft-refused@lab.test',
    );
    await expect(dialog.getByRole('combobox', { name: 'Role' })).toHaveAttribute(
      'data-value',
      'admin',
    );
    expect(
      (await invitations(page.request, ws)).some((x) => x.email === 'draft-refused@lab.test'),
    ).toBe(false);
    await page.screenshot({ path: `${artifacts}/invitation-refusal.png`, fullPage: true });
    await page.unroute('**/api/workspaces/*/invitations');
    await dialog.getByRole('button', { name: 'Send invitation' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(pending(page)).toContainText('draft-refused@lab.test');
  } finally {
    for (const inv of await invitations(page.request, ws))
      if (inv.email === 'draft-refused@lab.test')
        await page.request.delete(`/api/workspaces/${ws}/invitations/${inv.id}`, {
          headers: headers(ws),
        });
    expect(
      (
        await page.request.delete(`/api/workspaces/${ws}/members/${guestId}`, {
          headers: headers(ws),
        })
      ).status(),
    ).toBe(204);
    await guest.close();
  }
});

for (const action of ['invite', 'role', 'revoke', 'remove'] as const) {
  for (const destination of ['context', 'return'] as const) {
    for (const outcome of ['accepted', 'refused'] as const) {
      test(`delayed ${outcome} ${action} preserves a new ${destination} visit`, async ({
        page,
        browser,
      }) => {
        const ws = await labWorkspace(page.request);
        const other = await labFixture(page.request);
        const email = `lifecycle-${action}-${outcome}@lab.test`;
        const person = await browser.newContext({ storageState: { cookies: [], origins: [] } });
        let personId = '',
          invitationId = '';
        let release!: () => void;
        const gate = new Promise<void>((resolve) => {
          release = resolve;
        });
        let received!: (status: number) => void;
        const held = new Promise<number>((resolve) => {
          received = resolve;
        });
        let drain!: () => void;
        const drained = new Promise<void>((resolve) => {
          drain = resolve;
        });
        const method = action === 'invite' ? 'POST' : action === 'role' ? 'PATCH' : 'DELETE';
        let dispatch!: () => void;
        const dispatchGate = new Promise<void>((resolve) => {
          dispatch = resolve;
        });
        let seen!: () => void;
        const requestSeen = new Promise<void>((resolve) => {
          seen = resolve;
        });
        let handled = false;
        const writes: { ws: string; data: unknown }[] = [];
        try {
          if (action === 'role' || action === 'remove' || outcome === 'refused')
            personId = await joined(page.request, person, ws, email);
          if (action === 'revoke') {
            const res = await page.request.post(`/api/workspaces/${ws}/invitations`, {
              headers: headers(ws),
              data: { email, role: 'member' },
            });
            expect(res.status()).toBe(201);
            invitationId = (await res.json()).id;
          }
          await page.goto('/#/colleagues');
          await expect(roster(page)).toContainText('Ana Ruiz');
          await page.route('**/api/workspaces/**', async (route) => {
            if (route.request().method() !== method) return route.continue();
            writes.push({
              ws: route.request().headers()['x-workspace-id']!,
              data: route.request().postDataJSON(),
            });
            if (handled) return route.continue();
            handled = true;
            try {
              seen();
              await dispatchGate;
              const response =
                outcome === 'refused'
                  ? await person.request.fetch(route.request().url(), {
                      method,
                      headers: headers(ws),
                      data: route.request().postDataJSON() ?? undefined,
                    })
                  : await route.fetch();
              received(response.status());
              await gate;
              await route.fulfill({ response });
            } finally {
              drain();
            }
          });
          if (action === 'invite') {
            const dialog = await openInvite(page, email);
            await dialog.getByRole('button', { name: 'Send invitation' }).click();
            await expect(dialog.getByRole('button', { name: 'Send invitation' })).toBeDisabled();
          } else if (action === 'revoke') {
            await pending(page)
              .getByRole('row')
              .filter({ hasText: email })
              .getByRole('button', { name: 'Revoke' })
              .click();
          } else {
            const row = roster(page).getByRole('row').filter({ hasText: email });
            if (action === 'role') {
              await selectPicker(row.getByRole('combobox'), 'admin');
            } else {
              page.once('dialog', (d) => d.accept());
              await row.getByRole('button').click();
            }
          }
          await requestSeen;
          if (action === 'role')
            await expect(
              roster(page).getByRole('row').filter({ hasText: email }).getByRole('combobox'),
            ).toHaveAttribute('data-value', 'member');
          if (action === 'remove')
            await expect(roster(page).getByRole('row').filter({ hasText: email })).toBeVisible();
          if (action === 'revoke')
            await expect(pending(page).getByRole('row').filter({ hasText: email })).toBeVisible();
          dispatch();
          expect(await held).toBe(outcome === 'refused' ? 403 : action === 'invite' ? 201 : 204);
          if (destination === 'return') {
            await page.goto('/#/inbox');
            await expect(page.getByRole('navigation', { name: 'Inbox items' })).toBeVisible();
            await page.goto('/#/colleagues');
            await expect(roster(page)).toContainText('Ana Ruiz');
          } else if (action === 'invite') {
            // Closing and reopening on this same component is a new dialog visit.
            await page.keyboard.press('Escape');
          } else {
            // A workspace switch resets the old view before opening its next dialog.
            await page
              .getByRole('complementary', { name: 'Workspace navigation' })
              .getByRole('button', { name: /Beta-cell lab/ })
              .click();
            await page
              .getByRole('menu', { name: 'Workspace' })
              .getByRole('menuitem', { name: /Browser fixtures/ })
              .first()
              .click();
            await expect(page).toHaveURL(/#\/studies$/);
            await page.goto('/#/colleagues');
            await expect(roster(page)).toContainText('Ana Ruiz');
          }
          const newDialog = await openInvite(page, 'current-visit@lab.test');
          await selectPicker(newDialog.getByRole('combobox', { name: 'Role' }), 'admin');
          const browserResponse = page.waitForResponse(
            (r) => r.request().method() === method && r.url().includes(`/api/workspaces/${ws}/`),
          );
          release();
          await drained;
          await (await browserResponse).finished();
          await page.evaluate(() => new Promise(requestAnimationFrame));
          await expect(newDialog).toBeVisible();
          await expect(newDialog.getByRole('textbox', { name: 'Email' })).toHaveValue(
            'current-visit@lab.test',
          );
          await expect(newDialog.getByRole('combobox', { name: 'Role' })).toHaveAttribute(
            'data-value',
            'admin',
          );
          await expect(newDialog.getByRole('button', { name: 'Send invitation' })).toBeEnabled();
          await expect(page.getByRole('alert')).toHaveCount(0);
          expect(writes).toEqual([
            {
              ws,
              data:
                action === 'invite'
                  ? { email, role: 'member' }
                  : action === 'role'
                    ? { role: 'admin' }
                    : null,
            },
          ]);
          const storedInvitations = await invitations(page.request, ws);
          if (action === 'invite')
            expect(storedInvitations.some((x) => x.email === email)).toBe(outcome === 'accepted');
          if (action === 'revoke')
            expect(storedInvitations.some((x) => x.id === invitationId)).toBe(
              outcome === 'refused',
            );
          if (personId && (action === 'role' || action === 'remove')) {
            const list = (await (
              await page.request.get(`/api/workspaces/${ws}/members`, { headers: headers(ws) })
            ).json()) as { user_id: string; role: string }[];
            const stored = list.find((x) => x.user_id === personId);
            if (action === 'remove') expect(Boolean(stored)).toBe(outcome === 'refused');
            else expect(stored?.role).toBe(outcome === 'accepted' ? 'admin' : 'member');
          }
          expect(
            (await invitations(page.request, other.workspace_id)).some((x) => x.email === email),
          ).toBe(false);
        } finally {
          dispatch();
          release();
          if (handled) await drained;
          await page.unroute('**/api/workspaces/**');
          for (const inv of await invitations(page.request, ws))
            if (inv.email === email)
              await page.request.delete(`/api/workspaces/${ws}/invitations/${inv.id}`, {
                headers: headers(ws),
              });
          if (personId)
            await page.request.delete(`/api/workspaces/${ws}/members/${personId}`, {
              headers: headers(ws),
            });
          expect(
            (await page.request.delete(`/api/workspaces/${other.workspace_id}`)).status(),
          ).toBe(204);
          await person.close();
        }
      });
    }
  }
}
