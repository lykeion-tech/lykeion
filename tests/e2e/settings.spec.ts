import {
  expect,
  test,
  type APIRequestContext,
  type BrowserContext,
  type Browser,
  type Page,
  type Route,
} from '@playwright/test';
import { labSignIn } from './lab';

test.use({ trace: 'off', screenshot: 'off' });

const artifacts = '.superpowers/sdd/2026-10-15-ui1-frontend-wiring/task-21-artifacts';
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
  'base64',
);

async function ownWorkspace(request: APIRequestContext, name = 'Settings scratch') {
  const response = await request.post('/api/workspaces', {
    data: { name, slug: `settings-${Date.now()}-${Math.random().toString(36).slice(2, 7)}` },
  });
  expect(response.status()).toBe(201);
  return response.json();
}
async function selectWorkspace(page: Page, name: string) {
  await page.locator('.workspace-switch').click();
  await page.getByRole('menuitem', { name, exact: false }).last().click();
  await expect(page.locator('.workspace-switch')).toContainText(name);
}
async function general(page: Page, name: string) {
  await page.goto('/#/studies');
  await selectWorkspace(page, name);
  await page.goto('/#/settings?tab=general');
  await expect(page.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
    name,
  );
}
async function getWorkspace(request: APIRequestContext, id: string) {
  const response = await request.get(`/api/workspaces/${id}`);
  expect(response.status()).toBe(200);
  return response.json();
}
async function deleteOwn(request: APIRequestContext, id: string) {
  const response = await request.delete(`/api/workspaces/${id}`);
  expect([204, 404]).toContain(response.status());
}
async function account(request: APIRequestContext) {
  return (await request.get('/api/me')).json();
}
async function restoreAccount(
  request: APIRequestContext,
  me: { name: string; timezone: string; career_stage: string },
) {
  expect(
    (
      await request.patch('/api/me', {
        data: { name: me.name, timezone: me.timezone, career_stage: me.career_stage },
      })
    ).status(),
  ).toBe(200);
}

test('Profile and Preferences save to the account and the device', async ({ page }) => {
  const original = await account(page.request);
  await page.goto('/#/settings?tab=profile');
  const theme = (await page.locator('html').getAttribute('data-palette')) || 'ash';
  try {
    const nav = page.getByRole('navigation', { name: 'Settings', exact: true });
    await expect(nav.getByRole('button')).toHaveText([
      'Profile',
      'Preferences',
      'API Tokens',
      'Storage',
      'Machines',
      'Updates',
      'General',
      'Skills',
      'Connectors',
    ]);
    await page.getByRole('textbox', { name: 'Name', exact: true }).fill('Ana Settings');
    await page.getByRole('textbox', { name: 'Name', exact: true }).blur();
    await expect.poll(async () => (await account(page.request)).name).toBe('Ana Settings');
    await page.getByRole('combobox', { name: 'Your career stage' }).first().selectOption('phd');
    await expect.poll(async () => (await account(page.request)).career_stage).toBe('phd');
    await page.screenshot({ path: `${artifacts}/profile.png` });
    await page.getByRole('button', { name: 'General', exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Your career stage' })).toHaveValue('phd');
    await page.getByRole('combobox', { name: 'Your career stage' }).selectOption('postdoc');
    await expect.poll(async () => (await account(page.request)).career_stage).toBe('postdoc');
    await page.getByRole('button', { name: 'Profile', exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Your career stage' })).toHaveValue('postdoc');
    await page.getByRole('button', { name: 'Preferences', exact: true }).click();
    await page.getByRole('button', { name: 'Time zone', exact: true }).click();
    await page.getByRole('option', { name: /^Asia\/Tokyo UTC/ }).click();
    await expect.poll(async () => (await account(page.request)).timezone).toBe('Asia/Tokyo');
    await page.getByRole('button', { name: 'Midnight', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-palette', 'midnight');
    await page.screenshot({ path: `${artifacts}/preferences-midnight.png` });
    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('data-palette', 'midnight');
    await expect(page.getByRole('button', { name: 'Time zone', exact: true })).toContainText(
      'Asia/Tokyo',
    );
    await page.getByRole('button', { name: 'Ash', exact: true }).click();
    await page.screenshot({ path: `${artifacts}/preferences.png` });
  } finally {
    await restoreAccount(page.request, original);
    await page.goto('/#/settings?tab=preferences');
    await page
      .getByRole('button', {
        name: { ash: 'Ash', midnight: 'Midnight', dawn: 'Dawn', pale: 'Pale' }[theme] || 'Ash',
        exact: true,
      })
      .click();
  }
});

test('a token is shown once, listed with 90 day expiry, and revoked', async ({ page }) => {
  const name = `analysis-notebook-${Date.now()}`;
  let id = '';
  try {
    await page.goto('/#/settings?tab=tokens');
    await expect(page.getByRole('button', { name: 'Token expiry' })).toHaveCount(0);
    await page.getByRole('textbox', { name: 'Token name' }).fill(name);
    const request = page.waitForRequest(
      (r) => r.method() === 'POST' && r.url().endsWith('/api/tokens'),
    );
    await page.getByRole('button', { name: 'Create', exact: true }).click();
    expect((await request).postDataJSON()).toEqual({ name });
    await expect(page.getByText("Copy it now. It won't be shown again.")).toBeVisible();
    expect(
      await page.locator('.new-token code').evaluate((el) => /^lyk_/.test(el.textContent || '')),
    ).toBe(true);
    const row = page.getByRole('row').filter({ hasText: name });
    await expect(row).toContainText('Expires');
    const token = (await (await page.request.get('/api/tokens')).json()).find(
      (t: { name: string }) => t.name === name,
    );
    id = token.id;
    expect(
      Math.round((Date.parse(token.expires_at) - Date.parse(token.created_at)) / 86400000),
    ).toBe(90);
    await page.getByRole('button', { name: 'Profile', exact: true }).click();
    await page.getByRole('button', { name: 'API Tokens', exact: true }).click();
    await expect(page.locator('.new-token')).toHaveCount(0);
    await expect(row).toBeVisible();
    await page.screenshot({ path: `${artifacts}/tokens.png` });
    await row.getByRole('button', { name: 'Revoke', exact: true }).click();
    await expect(row).toHaveCount(0);
    const tokens = (await (await page.request.get('/api/tokens')).json()) as {
      id: string;
      revoked_at: string | null;
    }[];
    expect(tokens.find((t) => t.id === id)?.revoked_at).not.toBeNull();
  } finally {
    await page.goto('/#/settings?tab=profile');
    const tokens = await (await page.request.get('/api/tokens')).json();
    for (const t of tokens.filter(
      (t: { name: string; revoked_at: string | null }) => t.name === name && !t.revoked_at,
    ))
      await page.request.delete(`/api/tokens/${t.id}`);
  }
});

test('General saves workspace fields, a decoded logo, and explicit or URL-derived card prefix', async ({
  page,
}) => {
  const ws = await ownWorkspace(page.request);
  try {
    await general(page, ws.name);
    await page.getByRole('textbox', { name: 'Workspace description' }).fill('Human islet biology.');
    await page
      .getByRole('textbox', { name: 'Research context' })
      .fill('Beta-cell identity in type 2 diabetes.');
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.locator('.save-button')).toHaveCount(0);
    await expect(
      page.getByRole('button', { name: 'Change workspace logo', exact: true }),
    ).toBeEnabled();
    await page
      .locator('input[type=file]')
      .setInputFiles({ name: 'logo.png', mimeType: 'image/png', buffer: PNG });
    const logo = page.getByRole('img', { name: 'Workspace logo' });
    await expect(logo).toHaveAttribute('src', /\/logo\?v=/);
    await expect
      .poll(async () => logo.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0))
      .toBe(true);
    await page.getByRole('button', { name: 'Change prefix…', exact: true }).click();
    await page.getByRole('textbox', { name: 'Card prefix' }).fill('bcl');
    await expect(page.getByRole('textbox', { name: 'Card prefix' })).toHaveValue('BCL');
    await page.getByRole('button', { name: 'Save prefix', exact: true }).click();
    await expect(page.locator('.prefix code')).toHaveText('BCL');
    expect(await getWorkspace(page.request, ws.id)).toMatchObject({
      description: 'Human islet biology.',
      research_context: 'Beta-cell identity in type 2 diabetes.',
      display_prefix: 'BCL',
    });
    await page.screenshot({ path: `${artifacts}/general.png` });
    await page
      .getByRole('button', { name: 'Delete workspace', exact: true })
      .scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${artifacts}/general-danger.png` });
    await page.getByRole('button', { name: 'Change prefix…', exact: true }).click();
    await page.getByRole('textbox', { name: 'Card prefix' }).fill('');
    await page.getByRole('button', { name: 'Save prefix', exact: true }).click();
    await expect(page.locator('.prefix code')).toHaveText('SETT');
    expect((await getWorkspace(page.request, ws.id)).card_prefix).toBe('');
    await page.goto('/#/settings?tab=workspace');
    await expect(page.getByRole('heading', { name: 'General', exact: true })).toBeVisible();
  } finally {
    await deleteOwn(page.request, ws.id);
  }
});

test('an owner deletes its disposable workspace from the Danger Zone', async ({ page }) => {
  const ws = await ownWorkspace(page.request, 'Scratch lab');
  try {
    await general(page, ws.name);
    page.once('dialog', (d) => d.dismiss());
    await page.getByRole('button', { name: 'Delete workspace', exact: true }).click();
    expect((await page.request.get(`/api/workspaces/${ws.id}`)).status()).toBe(200);
    page.once('dialog', (d) => d.accept());
    await page.getByRole('button', { name: 'Delete workspace', exact: true }).click();
    await expect(page).toHaveURL(/#\/studies$/);
    const list = await (await page.request.get('/api/workspaces')).json();
    expect(list.some((w: { id: string }) => w.id === ws.id)).toBe(false);
    await expect(page.locator('.workspace-switch')).toContainText('Beta-cell lab');
    await page.goto('/#/settings?tab=general');
    await expect(page.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
      'Beta-cell lab',
    );
  } finally {
    await deleteOwn(page.request, ws.id);
  }
});

const memberSessions = new Map<string, Awaited<ReturnType<BrowserContext['cookies']>>>();
async function memberContext(owner: APIRequestContext, browser: Browser, ws: string) {
  const email = 'settings-member@lab.test';
  const guest = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const session = memberSessions.get(email);
  if (session) await guest.addCookies(session);
  else {
    await labSignIn(guest.request, email);
    memberSessions.set(email, await guest.cookies());
  }
  const invitation = await owner.post(`/api/workspaces/${ws}/invitations`, {
    data: { email, role: 'member' },
  });
  expect(invitation.status()).toBe(201);
  expect(
    (
      await guest.request.post(`/api/invitations/${(await invitation.json()).token}/accept`)
    ).status(),
  ).toBe(200);
  const me = await account(guest.request);
  expect(me.email).toBe(email);
  expect(
    (
      await guest.request.patch('/api/me', { data: { name: 'Settings Member', onboarded: true } })
    ).status(),
  ).toBe(200);
  return { guest, id: me.id as string };
}
async function hold(
  page: Page,
  url: string,
  method: string,
  request?: APIRequestContext,
  before = false,
  firstOnly = false,
) {
  let release!: () => void, entered!: () => void, finished!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const started = new Promise<void>((resolve) => (entered = resolve));
  const done = new Promise<void>((resolve) => (finished = resolve));
  let status = 0,
    count = 0;
  const payloads: unknown[] = [];
  const handler = async (route: Route) => {
    if (route.request().method() !== method) return route.continue();
    count++;
    if (firstOnly && count > 1) return route.continue();
    if (method === 'PATCH' || (method === 'POST' && url.endsWith('/api/tokens')))
      payloads.push(route.request().postDataJSON());
    if (before) {
      entered();
      await gate;
    }
    const response = request
      ? await request.fetch(route.request().url(), {
          method,
          data: route.request().postDataBuffer() ?? undefined,
          headers: {
            'Content-Type': route.request().headers()['content-type'] ?? 'application/json',
          },
        })
      : await route.fetch();
    status = response.status();
    if (!before) {
      entered();
      await gate;
    }
    const received = page.waitForResponse((r) => r.request() === route.request());
    await route.fulfill({ response });
    await (await received).finished();
    await page.evaluate(() => new Promise(requestAnimationFrame));
    finished();
  };
  await page.route(url, handler);
  return {
    started,
    done,
    release,
    status: () => status,
    count: () => count,
    payloads,
    close: () => page.unroute(url, handler),
  };
}

test('General keeps edited fields and follows untouched fields through a real background update', async ({
  page,
}) => {
  const ws = await ownWorkspace(page.request, 'Hydration lab');
  try {
    await general(page, ws.name);
    await page
      .getByRole('textbox', { name: 'Workspace description' })
      .fill('My unsaved description');
    expect(
      (
        await page.request.patch(`/api/workspaces/${ws.id}`, {
          data: {
            name: 'Renamed remotely',
            description: 'Remote description',
            research_context: 'New remote context',
          },
        })
      ).status(),
    ).toBe(200);
    await expect(page.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
      'Renamed remotely',
    );
    await expect(page.getByRole('textbox', { name: 'Research context' })).toHaveValue(
      'New remote context',
    );
    await expect(page.getByRole('textbox', { name: 'Workspace description' })).toHaveValue(
      'My unsaved description',
    );
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.locator('.save-button')).toHaveCount(0);
    expect(await getWorkspace(page.request, ws.id)).toMatchObject({
      name: 'Renamed remotely',
      description: 'My unsaved description',
      research_context: 'New remote context',
    });
  } finally {
    await deleteOwn(page.request, ws.id);
  }
});

test('real errors keep Profile and General drafts; the last owner cannot leave', async ({
  page,
}) => {
  const original = await account(page.request);
  const ws = await ownWorkspace(page.request, 'Refusal lab');
  try {
    await page.goto('/#/settings?tab=profile');
    await page.getByRole('textbox', { name: 'Name', exact: true }).fill('  ');
    await page.getByRole('textbox', { name: 'Name', exact: true }).blur();
    await expect(page.getByRole('alert')).toHaveText('name cannot be empty');
    await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('  ');
    expect((await account(page.request)).name).toBe(original.name);
    await page.screenshot({ path: `${artifacts}/profile-refusal.png` });
    await general(page, ws.name);
    await page.getByRole('button', { name: 'Change prefix…', exact: true }).click();
    await page.getByRole('textbox', { name: 'Card prefix' }).fill('X');
    await page.getByRole('button', { name: 'Save prefix', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('two to six capital letters');
    await expect(page.getByRole('textbox', { name: 'Card prefix' })).toHaveValue('X');
    const bad = page.waitForResponse(
      (r) => r.request().method() === 'PUT' && r.url().endsWith('/logo'),
    );
    await page
      .locator('input[type=file]')
      .setInputFiles({ name: 'bad.png', mimeType: 'image/png', buffer: Buffer.from('not a PNG') });
    expect((await bad).status()).toBe(400);
    await expect(page.getByRole('alert')).toHaveText(
      'logo content does not match its Content-Type',
    );
    for (const file of [
      { name: 'large.png', mimeType: 'image/png', buffer: Buffer.alloc(512 * 1024 + 1) },
      { name: 'logo.gif', mimeType: 'image/gif', buffer: PNG },
    ]) {
      await page.locator('input[type=file]').setInputFiles(file);
      await expect(page.getByRole('alert')).toHaveText(
        'Choose a PNG, JPEG or WebP of at most 512 KB.',
      );
    }
    page.once('dialog', (d) => d.accept());
    await page.getByRole('button', { name: 'Leave workspace', exact: true }).click();
    await expect(page.getByRole('alert')).toHaveText('a workspace needs at least one owner');
    await expect(page.getByRole('textbox', { name: 'Card prefix' })).toHaveValue('X');
    await expect(page.getByRole('button', { name: 'Leave workspace', exact: true })).toBeEnabled();
    await page.screenshot({ path: `${artifacts}/general-refusal.png` });
    expect((await page.request.get(`/api/workspaces/${ws.id}`)).status()).toBe(200);
  } finally {
    await restoreAccount(page.request, original);
    await deleteOwn(page.request, ws.id);
  }
});

test('a member has read-only General and can leave with confirmation', async ({
  page,
  browser,
}) => {
  const ws = await ownWorkspace(page.request, 'Member leave lab');
  const { guest, id } = await memberContext(page.request, browser, ws.id);
  try {
    const other = await guest.newPage();
    await other.goto('/#/studies');
    // This member only belongs to its own disposable workspace.
    await other.goto('/#/settings?tab=general');
    await expect(
      other.getByRole('textbox', { name: 'Workspace name', exact: true }),
    ).toBeDisabled();
    await expect(
      other.getByRole('button', { name: 'Change workspace logo', exact: true }),
    ).toBeDisabled();
    await expect(other.getByRole('button', { name: 'Delete workspace', exact: true })).toHaveCount(
      0,
    );
    expect(
      (
        await guest.request.patch(`/api/workspaces/${ws.id}`, {
          data: { description: 'Forbidden' },
        })
      ).status(),
    ).toBe(403);
    expect((await guest.request.delete(`/api/workspaces/${ws.id}`)).status()).toBe(403);
    expect(
      (
        await guest.request.put(`/api/workspaces/${ws.id}/logo`, {
          data: PNG,
          headers: { 'Content-Type': 'image/png' },
        })
      ).status(),
    ).toBe(403);
    await other.screenshot({ path: `${artifacts}/member-general.png` });
    other.once('dialog', (d) => d.dismiss());
    await other.getByRole('button', { name: 'Leave workspace', exact: true }).click();
    expect((await guest.request.get(`/api/workspaces/${ws.id}`)).status()).toBe(200);
    other.once('dialog', (d) => d.accept());
    await other.getByRole('button', { name: 'Leave workspace', exact: true }).click();
    await expect
      .poll(async () => (await guest.request.get(`/api/workspaces/${ws.id}`)).status())
      .toBe(404);
    await expect(other).toHaveURL(/#\/onboarding\?step=workspace$/);
  } finally {
    await page.request.delete(`/api/workspaces/${ws.id}/members/${id}`);
    await guest.close();
    await deleteOwn(page.request, ws.id);
  }
});

for (const action of ['details', 'prefix', 'logo'] as const)
  for (const accepted of [true, false])
    for (const departure of ['tab', 'workspace', 'return'] as const) {
      test(`${accepted ? 'accepted' : 'refused'} ${action} response cannot alter a new ${departure} Settings visit`, async ({
        page,
        browser,
      }) => {
        const ws = await ownWorkspace(page.request, 'Original settings lab');
        const alternate = await ownWorkspace(page.request, 'Alternate settings lab');
        const actor = accepted ? undefined : await memberContext(page.request, browser, ws.id);
        let held: Awaited<ReturnType<typeof hold>> | undefined;
        try {
          await general(page, ws.name);
          const method = action === 'logo' ? 'PUT' : 'PATCH';
          held = await hold(
            page,
            `**/api/workspaces/${ws.id}${action === 'logo' ? '/logo' : ''}`,
            method,
            actor?.guest.request,
          );
          if (action === 'details') {
            await page
              .getByRole('textbox', { name: 'Workspace description' })
              .fill('Original submitted description');
            await page.getByRole('button', { name: 'Save changes', exact: true }).click();
          } else if (action === 'prefix') {
            await page.getByRole('button', { name: 'Change prefix…', exact: true }).click();
            await page.getByRole('textbox', { name: 'Card prefix' }).fill('OLD');
            await page.getByRole('button', { name: 'Save prefix', exact: true }).click();
          } else
            await page
              .locator('input[type=file]')
              .setInputFiles({ name: 'logo.png', mimeType: 'image/png', buffer: PNG });
          await held.started;
          expect(held.status()).toBe(accepted ? (action === 'logo' ? 204 : 200) : 403);
          if (departure === 'tab') {
            await page.getByRole('button', { name: 'Profile', exact: true }).click();
            await page
              .getByRole('textbox', { name: 'Name', exact: true })
              .fill('New profile draft');
          } else {
            await selectWorkspace(page, alternate.name);
            if (departure === 'return') await selectWorkspace(page, ws.name);
            await page.goto('/#/settings?tab=general');
            await expect(
              page.getByRole('textbox', { name: 'Workspace name', exact: true }),
            ).toHaveValue(departure === 'return' ? ws.name : alternate.name);
            await page
              .getByRole('textbox', { name: 'Workspace description' })
              .fill('New description draft');
            await page.getByRole('button', { name: 'Change prefix…', exact: true }).click();
            await page.getByRole('textbox', { name: 'Card prefix' }).fill('NEW');
          }
          held.release();
          await held.done;
          await expect(page.getByRole('alert')).toHaveCount(0);
          if (departure === 'tab')
            await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue(
              'New profile draft',
            );
          else {
            await expect(page.getByRole('textbox', { name: 'Workspace description' })).toHaveValue(
              'New description draft',
            );
            await expect(page.getByRole('textbox', { name: 'Card prefix' })).toHaveValue('NEW');
            await expect(
              page.getByRole('button', { name: 'Save changes', exact: true }),
            ).toBeEnabled();
            await expect(
              page.getByRole('button', { name: 'Save prefix', exact: true }),
            ).toBeEnabled();
          }
          expect(held.count()).toBe(1);
          const persisted = await getWorkspace(page.request, ws.id);
          expect(persisted.description).toBe(
            accepted && action === 'details' ? 'Original submitted description' : '',
          );
          expect(persisted.card_prefix).toBe(accepted && action === 'prefix' ? 'OLD' : '');
          expect(Boolean(persisted.logo_blob)).toBe(accepted && action === 'logo');
          expect(await getWorkspace(page.request, alternate.id)).toMatchObject({
            description: '',
            card_prefix: '',
            logo_blob: '',
          });
        } finally {
          held?.release();
          if (held) await held.close();
          if (actor) {
            await page.request.delete(`/api/workspaces/${ws.id}/members/${actor.id}`);
            await actor.guest.close();
          }
          await deleteOwn(page.request, ws.id);
          await deleteOwn(page.request, alternate.id);
        }
      });
    }

for (const kind of ['account', 'token'] as const)
  test(`late accepted ${kind} response refreshes canonical account data without contaminating a new visit`, async ({
    page,
  }) => {
    const original = await account(page.request);
    const tokenName = `late-token-${Date.now()}`;
    const path = kind === 'account' ? '**/api/me' : '**/api/tokens';
    let held: Awaited<ReturnType<typeof hold>> | undefined;
    try {
      await page.goto(`/#/settings?tab=${kind === 'account' ? 'profile' : 'tokens'}`);
      held = await hold(page, path, kind === 'account' ? 'PATCH' : 'POST', undefined, true);
      if (kind === 'account') {
        await page
          .getByRole('textbox', { name: 'Name', exact: true })
          .fill('Accepted old account name');
        await page.getByRole('textbox', { name: 'Name', exact: true }).blur();
      } else {
        await page.getByRole('textbox', { name: 'Token name' }).fill(tokenName);
        await page.getByRole('button', { name: 'Create', exact: true }).click();
      }
      await held.started;
      if (kind === 'token') {
        await page.goto('/#/studies');
        await page.goto('/#/settings?tab=tokens');
      } else {
        await page.getByRole('button', { name: 'General', exact: true }).click();
        await page.getByRole('button', { name: 'Profile', exact: true }).click();
      }
      const field = page.getByRole('textbox', {
        name: kind === 'account' ? 'Name' : 'Token name',
        exact: true,
      });
      await field.fill('New unsaved draft');
      const reread =
        kind === 'account'
          ? page.waitForResponse(
              (r) => r.request().method() === 'GET' && r.url().endsWith('/api/me'),
            )
          : undefined;
      held.release();
      await held.done;
      expect(held.status()).toBe(kind === 'account' ? 200 : 201);
      await expect(field).toHaveValue('New unsaved draft');
      await expect(page.locator('.new-token')).toHaveCount(0);
      if (kind === 'account') {
        await expect
          .poll(async () => (await account(page.request)).name)
          .toBe('Accepted old account name');
        expect((await (await reread!).json()).name).toBe('Accepted old account name');
        await expect(field).toHaveValue('New unsaved draft');
      } else await expect(page.getByRole('row').filter({ hasText: tokenName })).toBeVisible();
    } finally {
      held?.release();
      if (held) await held.close();
      if (kind === 'token') await page.goto('/#/settings?tab=profile');
      await restoreAccount(page.request, original);
      const tokens = await (await page.request.get('/api/tokens')).json();
      for (const token of tokens.filter(
        (t: { name: string; revoked_at: string | null }) => t.name === tokenName && !t.revoked_at,
      ))
        await page.request.delete(`/api/tokens/${token.id}`);
    }
  });

for (const kind of ['leave', 'delete'] as const)
  test(`late accepted ${kind} does not reset a new workspace selection or navigate its Settings`, async ({
    page,
    browser,
  }) => {
    const ws = await ownWorkspace(page.request, 'Old danger lab');
    const alternate = await ownWorkspace(page.request, 'New danger lab');
    const actor = kind === 'leave' ? await memberContext(page.request, browser, ws.id) : undefined;
    if (actor)
      expect(
        (
          await page.request.patch(`/api/workspaces/${ws.id}/members/${actor.id}`, {
            data: { role: 'owner' },
          })
        ).status(),
      ).toBe(204);
    let held: Awaited<ReturnType<typeof hold>> | undefined;
    try {
      await general(page, ws.name);
      held = await hold(
        page,
        `**/api/workspaces/${ws.id}${kind === 'leave' ? '/leave' : ''}`,
        kind === 'leave' ? 'POST' : 'DELETE',
        undefined,
        true,
      );
      page.once('dialog', (d) => d.accept());
      await page
        .getByRole('button', {
          name: kind === 'leave' ? 'Leave workspace' : 'Delete workspace',
          exact: true,
        })
        .click();
      await held.started;
      await selectWorkspace(page, alternate.name);
      await page.goto('/#/settings?tab=general');
      await expect(page.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
        alternate.name,
      );
      await page.getByRole('textbox', { name: 'Workspace description' }).fill('Keep this draft');
      held.release();
      await held.done;
      expect(held.status()).toBe(204);
      await expect(page).toHaveURL(/#\/settings\?tab=general$/);
      await expect(page.locator('.workspace-switch')).toContainText(alternate.name);
      await expect(page.getByRole('textbox', { name: 'Workspace description' })).toHaveValue(
        'Keep this draft',
      );
      await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeEnabled();
      expect((await page.request.get(`/api/workspaces/${ws.id}`)).status()).toBe(404);
    } finally {
      held?.release();
      if (held) await held.close();
      if (actor) {
        await deleteOwn(actor.guest.request, ws.id);
        await actor.guest.close();
      } else await deleteOwn(page.request, ws.id);
      await deleteOwn(page.request, alternate.id);
    }
  });

test('a career-stage edit is saved while an independent name change is pending', async ({
  page,
}) => {
  const original = await account(page.request);
  let held: Awaited<ReturnType<typeof hold>> | undefined;
  try {
    await page.goto('/#/settings?tab=profile');
    held = await hold(page, '**/api/me', 'PATCH', undefined, true, true);
    await page.getByRole('textbox', { name: 'Name', exact: true }).fill('Pending independent name');
    await page.getByRole('textbox', { name: 'Name', exact: true }).blur();
    await held.started;
    const stage = original.career_stage === 'phd' ? 'postdoc' : 'phd';
    await page.getByRole('combobox', { name: 'Your career stage' }).selectOption(stage);
    await expect
      .poll(async () => (await account(page.request)).career_stage, { timeout: 3000 })
      .toBe(stage);
    held.release();
    await held.done;
    await expect
      .poll(async () => (await account(page.request)).name)
      .toBe('Pending independent name');
    expect((await account(page.request)).career_stage).toBe(stage);
    expect(held.count()).toBe(2);
  } finally {
    held?.release();
    if (held) {
      await held.done;
      await held.close();
    }
    await restoreAccount(page.request, original);
  }
});

test('General stays busy until all current account writes finish', async ({ page }) => {
  const original = await account(page.request);
  let held: Awaited<ReturnType<typeof hold>> | undefined;
  try {
    await page.goto('/#/settings?tab=general');
    held = await hold(page, '**/api/me', 'PATCH', undefined, true, true);
    const stage = page.getByRole('combobox', { name: 'Your career stage' });
    await stage.selectOption('phd');
    await held.started;
    const second = page.waitForResponse(
      (r) =>
        r.request().method() === 'PATCH' &&
        r.url().endsWith('/api/me') &&
        r.request().postDataJSON().career_stage === 'postdoc',
    );
    await stage.selectOption('postdoc');
    expect((await second).status()).toBe(200);
    await expect(stage).toHaveValue('postdoc');
    await expect(
      page.getByRole('button', { name: 'Change workspace logo', exact: true }),
    ).toBeDisabled();
    held.release();
    await held.done;
    await expect(stage).toHaveValue('phd');
    await expect(
      page.getByRole('button', { name: 'Change workspace logo', exact: true }),
    ).toBeEnabled();
    expect(held.count()).toBe(2);
  } finally {
    held?.release();
    if (held) {
      await held.done;
      await held.close();
    }
    await restoreAccount(page.request, original);
  }
});

test('a saved workspace name uses the canonical trimmed value', async ({ page }) => {
  const ws = await ownWorkspace(page.request, 'Name trim lab');
  try {
    await general(page, ws.name);
    await page.getByRole('textbox', { name: 'Workspace name', exact: true }).fill('  Saved name  ');
    const saved = page.waitForResponse(
      (r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/workspaces/${ws.id}`),
    );
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    expect((await saved).status()).toBe(200);
    expect((await getWorkspace(page.request, ws.id)).name).toBe('Saved name');
    await expect(page.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
      'Saved name',
    );
    await expect(page.locator('.save-button')).toHaveCount(0);
  } finally {
    await deleteOwn(page.request, ws.id);
  }
});

test('a saved account name uses the canonical trimmed value', async ({ page }) => {
  const original = await account(page.request);
  try {
    await page.goto('/#/settings?tab=profile');
    await page.getByRole('textbox', { name: 'Name', exact: true }).fill('  Trimmed account name  ');
    await page.getByRole('textbox', { name: 'Name', exact: true }).blur();
    await expect.poll(async () => (await account(page.request)).name).toBe('Trimmed account name');
    await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue(
      'Trimmed account name',
    );
  } finally {
    await restoreAccount(page.request, original);
  }
});

test('many workspaces remain normally clickable in the workspace popup on a small viewport', async ({
  page,
}) => {
  const workspaces: { id: string; name: string }[] = [];
  try {
    await page.setViewportSize({ width: 1000, height: 600 });
    for (let i = 0; i < 20; i++)
      workspaces.push(await ownWorkspace(page.request, `Popup lab ${i}`));
    await page.goto('/#/studies');
    await page.locator('.workspace-switch').click();
    const menu = page.getByRole('menu', { name: 'Workspace', exact: true });
    const last = workspaces[workspaces.length - 1]!;
    await menu
      .getByRole('menuitem', { name: new RegExp(last.name + '$') })
      .click({ timeout: 3000 });
    await expect(page.locator('.workspace-switch')).toContainText(last.name);
    await page.locator('.workspace-switch').click();
    const box = await menu.boundingBox();
    expect(box?.y).toBe(98);
    expect(box?.width).toBe(224);
    expect(box!.y + box!.height).toBeLessThanOrEqual(600);
    expect(await menu.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
    await menu.getByRole('menuitem', { name: /Popup lab 0$/ }).click();
    await expect(page.locator('.workspace-switch')).toContainText('Popup lab 0');
    await page.locator('.workspace-switch').click();
    await page.screenshot({ path: `${artifacts}/workspace-popup-small.png` });
  } finally {
    for (const workspace of workspaces) await deleteOwn(page.request, workspace.id);
  }
});
